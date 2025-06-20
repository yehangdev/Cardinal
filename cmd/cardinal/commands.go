package main

import (
	"cardinal/internal/cardinal"
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/redis/go-redis/v9"
	"io"
	"time"
)

func runCommand(ctx context.Context, client redis.UniversalClient, config cardinal.Config, format cardinal.OutputFormat, args []string, writer io.Writer) error {
	switch args[0] {
	case "ping":
		return runPing(ctx, client, config, format, args[1:], writer)
	case "get":
		return runGet(ctx, client, format, args[1:], writer)
	case "set":
		return runSet(ctx, client, format, args[1:], writer)
	case "ttl":
		return runTTL(ctx, client, format, args[1:], writer)
	case "scan":
		return runScan(ctx, client, format, args[1:], writer)
	case "latency":
		return runLatency(ctx, client, format, args[1:], writer)
	case "pipeline":
		return runPipeline(ctx, client, format, args[1:], writer)
	case "cache-aside":
		return runCacheAside(ctx, client, format, args[1:], writer)
	default:
		return cardinal.NewUsageError("unknown command %q", args[0])
	}
}

func runConfigCommand(config cardinal.Config, args []string, format cardinal.OutputFormat, writer io.Writer) error {
	action := "show"
	if len(args) > 0 {
		action = args[0]
	}
	if len(args) > 1 {
		return cardinal.NewUsageError("config takes at most one action")
	}

	switch action {
	case "show":
		switch format {
		case cardinal.FormatJSON:
			return cardinal.WriteJSON(writer, config.Redacted())
		case cardinal.FormatPrometheus:
			return cardinal.WritePrometheus(writer, []cardinal.Metric{
				labeledGauge("cardinal_config_info", "Static configuration summary.", 1,
					map[string]string{"mode": config.Mode, "tls": fmt.Sprintf("%t", config.TLS.Enabled)}),
			})
		default:
			_, err := fmt.Fprintf(writer,
				"mode=%s\ntimeout=%s\ntls=%t\ndb=%d\n",
				config.Mode,
				config.Timeout,
				config.TLS.Enabled,
				config.DB,
			)
			return err
		}
	case "validate":
		if err := config.Validate(); err != nil {
			return err
		}
		switch format {
		case cardinal.FormatJSON:
			return cardinal.WriteJSON(writer, map[string]interface{}{
				"ok":   true,
				"mode": config.Mode,
			})
		case cardinal.FormatPrometheus:
			return cardinal.WritePrometheus(writer, []cardinal.Metric{
				gauge("cardinal_config_valid", "Whether the supplied configuration is valid.", 1),
			})
		default:
			_, err := fmt.Fprintln(writer, "valid")
			return err
		}
	default:
		return cardinal.NewUsageError("unknown config action %q", action)
	}
}

func runPing(ctx context.Context, client redis.UniversalClient, config cardinal.Config, format cardinal.OutputFormat, args []string, writer io.Writer) error {
	if len(args) != 0 {
		return cardinal.NewUsageError("ping takes no arguments")
	}

	startedAt := time.Now()
	value, err := client.Ping(ctx).Result()
	elapsed := time.Since(startedAt)
	if err != nil {
		return err
	}

	switch format {
	case cardinal.FormatJSON:
		return cardinal.WriteJSON(writer, map[string]interface{}{
			"ok":         true,
			"mode":       config.Mode,
			"response":   value,
			"latency_ns": elapsed,
		})
	case cardinal.FormatPrometheus:
		return cardinal.WritePrometheus(writer, []cardinal.Metric{
			gauge("cardinal_up", "Whether the last connect and ping succeeded.", 1),
			gauge("cardinal_ping_duration_seconds", "Duration of the PING command.", elapsed.Seconds()),
		})
	default:
		_, err := fmt.Fprintln(writer, value)
		return err
	}
}

func usageErrorFromFlag(command string, err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return cardinal.NewUsageError("%s: help requested", command)
	}
	return cardinal.NewUsageError("%s: %v", command, err)
}

type latencyReportJSON struct {
	Command    string                `json:"command"`
	Mode       string                `json:"mode"`
	Count      int                   `json:"count"`
	DurationMS float64               `json:"duration_ms"`
	Throughput float64               `json:"operations_per_second"`
	MinMS      float64               `json:"min_ms"`
	P50MS      float64               `json:"p50_ms"`
	P90MS      float64               `json:"p90_ms"`
	P95MS      float64               `json:"p95_ms"`
	P99MS      float64               `json:"p99_ms"`
	MaxMS      float64               `json:"max_ms"`
	AverageMS  float64               `json:"average_ms"`
	StdDevMS   float64               `json:"stddev_ms"`
	Histogram  []histogramBucketJSON `json:"histogram"`
}

type histogramBucketJSON struct {
	UpperBoundMS float64 `json:"upper_bound_ms"`
	Count        uint64  `json:"count"`
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func mergeLabels(labels map[string]string, key, value string) map[string]string {
	result := make(map[string]string, len(labels)+1)
	for labelKey, labelValue := range labels {
		result[labelKey] = labelValue
	}
	result[key] = value
	return result
}

func gauge(name, help string, value float64) cardinal.Metric {
	return cardinal.Metric{Name: name, Help: help, Type: cardinal.MetricGauge, Value: value}
}

func counter(name, help string, value float64) cardinal.Metric {
	return cardinal.Metric{Name: name, Help: help, Type: cardinal.MetricCounter, Value: value}
}

func labeledGauge(name, help string, value float64, labels map[string]string) cardinal.Metric {
	return cardinal.Metric{Name: name, Help: help, Type: cardinal.MetricGauge, Labels: labels, Value: value}
}

func labeledCounter(name, help string, value float64, labels map[string]string) cardinal.Metric {
	return cardinal.Metric{Name: name, Help: help, Type: cardinal.MetricCounter, Labels: labels, Value: value}
}

func runCommand(ctx context.Context, client redis.UniversalClient, config cardinal.Config, format cardinal.OutputFormat, args []string, writer io.Writer) error {
	switch args[0] {
	case "ping":
		return runPing(ctx, client, config, format, args[1:], writer)
	default:
		return cardinal.NewUsageError("unknown command %q", args[0])
	}
}
