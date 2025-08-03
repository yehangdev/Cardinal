package cardinal

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
)

// Stats summarizes a set of command timings.
type Stats struct {
	Count  int
	Min    time.Duration
	P50    time.Duration
	P90    time.Duration
	P95    time.Duration
	P99    time.Duration
	Max    time.Duration
	Avg    time.Duration
	StdDev time.Duration
}

// MeasureOptions configures latency probing.
type MeasureOptions struct {
	Count     int
	Pipeline  bool
	BatchSize int
	Buckets   []time.Duration
}

// LatencyReport combines percentile statistics and a histogram.
type LatencyReport struct {
	Command    string
	Mode       string
	Stats      Stats
	Histogram  Histogram
	Duration   time.Duration
	Throughput float64
}

// Measure runs a Redis command repeatedly and returns timing statistics.
func Measure(ctx context.Context, client redis.UniversalClient, command []string, count int) (Stats, error) {
	report, err := MeasureWithOptions(ctx, client, command, MeasureOptions{Count: count})
	if err != nil {
		return Stats{}, err
	}
	return report.Stats, nil
}

// MeasureWithOptions runs a command sequentially or in pipeline batches.
func MeasureWithOptions(
	ctx context.Context,
	client redis.UniversalClient,
	command []string,
	options MeasureOptions,
) (LatencyReport, error) {
	if len(command) == 0 {
		return LatencyReport{}, errors.New("command is empty")
	}
	if client == nil {
		return LatencyReport{}, errors.New("redis client is nil")
	}
	if options.Count < 1 {
		return LatencyReport{}, errors.New("count must be positive")
	}
	if options.BatchSize < 1 {
		options.BatchSize = 100
	}
	if len(options.Buckets) == 0 {
		options.Buckets = DefaultLatencyBuckets()
	}

	commandArgs := make([]interface{}, len(command))
	for i, arg := range command {
		commandArgs[i] = arg
	}

	mode := "sequential"
	samples := make([]time.Duration, 0, options.Count)
	startedAt := time.Now()

	if !options.Pipeline {
		for index := 0; index < options.Count; index++ {
			start := time.Now()
			if err := client.Do(ctx, commandArgs...).Err(); err != nil {
				return LatencyReport{}, fmt.Errorf("%s: %w", command[0], err)
			}
			samples = append(samples, time.Since(start))
		}
	} else {
		mode = "pipeline"
		samples = make([]time.Duration, 0, options.Count)
		for remaining := options.Count; remaining > 0; {
			batchSize := options.BatchSize
			if batchSize > remaining {
				batchSize = remaining
			}

			batchStarted := time.Now()
			commands := make([]redis.Cmder, batchSize)
			_, err := client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
				for index := range commands {
					commands[index] = pipe.Do(ctx, commandArgs...)
				}
				return nil
			})
			batchDuration := time.Since(batchStarted)
			if err != nil {
				for _, queued := range commands {
					if commandErr := queued.Err(); commandErr != nil {
						return LatencyReport{}, fmt.Errorf("%s pipeline: %w", command[0], commandErr)
					}
				}
				return LatencyReport{}, fmt.Errorf("%s pipeline: %w", command[0], err)
			}
			for _, queued := range commands {
				if err := queued.Err(); err != nil {
					return LatencyReport{}, fmt.Errorf("%s pipeline: %w", command[0], err)
				}
			}

			perCommand := batchDuration / time.Duration(batchSize)
			for index := 0; index < batchSize; index++ {
				samples = append(samples, perCommand)
			}
			remaining -= batchSize
		}
	}

	stats, err := Summarize(samples)
	if err != nil {
		return LatencyReport{}, err
	}
	elapsed := time.Since(startedAt)
	throughput := 0.0
	if elapsed > 0 {
		throughput = float64(stats.Count) / elapsed.Seconds()
	}

	return LatencyReport{
		Command:    command[0],
		Mode:       mode,
		Stats:      stats,
		Histogram:  NewHistogram(samples, options.Buckets),
		Duration:   elapsed,
		Throughput: throughput,
	}, nil
}

// Summarize calculates nearest-rank percentiles for the supplied samples.
func Summarize(samples []time.Duration) (Stats, error) {
	if len(samples) == 0 {
		return Stats{}, errors.New("no samples")
	}

	ordered := append([]time.Duration(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i] < ordered[j]
	})

	var total time.Duration
	var totalSquared float64
	for _, sample := range ordered {
		total += sample
		value := float64(sample)
		totalSquared += value * value
	}
	average := total / time.Duration(len(ordered))
	variance := totalSquared/float64(len(ordered)) - math.Pow(float64(average), 2)
	if variance < 0 {
		variance = 0
	}

	return Stats{
		Count:  len(ordered),
		Min:    ordered[0],
		P50:    percentile(ordered, 50),
		P90:    percentile(ordered, 90),
		P95:    percentile(ordered, 95),
		P99:    percentile(ordered, 99),
		Max:    ordered[len(ordered)-1],
		Avg:    average,
		StdDev: time.Duration(math.Sqrt(variance)),
	}, nil
}

func percentile(ordered []time.Duration, percent int) time.Duration {
	index := (percent*len(ordered) + 99) / 100
	if index < 1 {
		index = 1
	}
	return ordered[index-1]
}
