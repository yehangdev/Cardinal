package cardinal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// PipelineOperation describes one command in a pipeline.
type PipelineOperation struct {
	Command string
	Key     string
	Value   string
	TTL     time.Duration
}

// PipelineOperationResult reports one command result.
type PipelineOperationResult struct {
	Index    int
	Command  string
	Key      string
	Status   string
	Value    interface{}
	Error    string
	Duration time.Duration
}

// PipelineReport summarizes a complete pipeline.
type PipelineReport struct {
	Operations []PipelineOperationResult
	Duration   time.Duration
	Succeeded  int
	Misses     int
	Failed     int
}

// RepeatPipelineOperations expands a command template across keys.
func RepeatPipelineOperations(
	command string,
	keys []string,
	value string,
	ttl time.Duration,
	repeat int,
) ([]PipelineOperation, error) {
	normalized, err := normalizePipelineCommand(command)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, errors.New("pipeline requires at least one key")
	}
	if repeat < 1 {
		return nil, errors.New("repeat must be positive")
	}
	if normalized == "SET" && value == "" {
		return nil, errors.New("pipeline SET requires --value")
	}

	operations := make([]PipelineOperation, 0, len(keys)*repeat)
	for iteration := 0; iteration < repeat; iteration++ {
		for _, key := range keys {
			if strings.TrimSpace(key) == "" {
				return nil, errors.New("pipeline keys must not be empty")
			}
			operations = append(operations, PipelineOperation{
				Command: normalized,
				Key:     key,
				Value:   value,
				TTL:     ttl,
			})
		}
	}
	return operations, nil
}

// ExecutePipeline sends operations in one round trip and records per-command
// results. Redis logical misses are reported separately from failures.
func ExecutePipeline(
	ctx context.Context,
	client redis.UniversalClient,
	operations []PipelineOperation,
) (PipelineReport, error) {
	if client == nil {
		return PipelineReport{}, errors.New("redis client is nil")
	}
	if len(operations) == 0 {
		return PipelineReport{}, errors.New("pipeline has no operations")
	}
	for index, operation := range operations {
		if _, err := normalizePipelineCommand(operation.Command); err != nil {
			return PipelineReport{}, fmt.Errorf("operation %d: %w", index, err)
		}
		if operation.Key == "" {
			return PipelineReport{}, fmt.Errorf("operation %d: key is empty", index)
		}
	}

	report := PipelineReport{
		Operations: make([]PipelineOperationResult, len(operations)),
	}
	startedAt := time.Now()
	commands := make([]redis.Cmder, len(operations))

	_, executeErr := client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for index, operation := range operations {
			command := strings.ToUpper(strings.TrimSpace(operation.Command))
			switch command {
			case "GET":
				commands[index] = pipe.Get(ctx, operation.Key)
			case "SET":
				commands[index] = pipe.Set(ctx, operation.Key, operation.Value, operation.TTL)
			case "DEL":
				commands[index] = pipe.Del(ctx, operation.Key)
			case "EXPIRE":
				commands[index] = pipe.Expire(ctx, operation.Key, operation.TTL)
			case "INCR":
				commands[index] = pipe.Incr(ctx, operation.Key)
			default:
				return fmt.Errorf("unsupported pipeline command %q", operation.Command)
			}
		}
		return nil
	})
	report.Duration = time.Since(startedAt)

	commandErrors := 0
	for index, command := range commands {
		if command == nil {
			continue
		}
		operation := operations[index]
		result := PipelineOperationResult{
			Index:    index,
			Command:  strings.ToUpper(strings.TrimSpace(operation.Command)),
			Key:      operation.Key,
			Status:   "ok",
			Duration: report.Duration,
		}

		value, err := commandResult(command)
		switch {
		case errors.Is(err, redis.Nil):
			result.Status = "miss"
			report.Misses++
		case err != nil:
			result.Status = "error"
			result.Error = err.Error()
			report.Failed++
			commandErrors++
		default:
			result.Value = value
			report.Succeeded++
		}
		report.Operations[index] = result
	}

	if executeErr != nil && commandErrors == 0 && report.Misses == 0 {
		return report, fmt.Errorf("execute pipeline: %w", executeErr)
	}
	return report, nil
}

func normalizePipelineCommand(command string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(command))
	switch normalized {
	case "GET", "SET", "DEL", "EXPIRE", "INCR":
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported pipeline command %q", command)
	}
}

func commandResult(command redis.Cmder) (interface{}, error) {
	switch typed := command.(type) {
	case *redis.StringCmd:
		return typed.Result()
	case *redis.StatusCmd:
		return typed.Result()
	case *redis.IntCmd:
		return typed.Result()
	case *redis.BoolCmd:
		return typed.Result()
	default:
		return command, command.Err()
	}
}
