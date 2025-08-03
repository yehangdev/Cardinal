package main

import (
	"cardinal/internal/cardinal"
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/redis/go-redis/v9"
	"io"
	"strings"
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

func runGet(ctx context.Context, client redis.UniversalClient, format cardinal.OutputFormat, args []string, writer io.Writer) error {
	if len(args) != 1 {
		return cardinal.NewUsageError("get requires KEY")
	}

	value, err := client.Get(ctx, args[0]).Result()
	if err != nil {
		return err
	}
	switch format {
	case cardinal.FormatJSON:
		return cardinal.WriteJSON(writer, map[string]interface{}{
			"ok":    true,
			"key":   args[0],
			"value": value,
		})
	case cardinal.FormatPrometheus:
		return cardinal.WritePrometheus(writer, []cardinal.Metric{
			labeledGauge("cardinal_get_hit", "Whether GET found a value.", 1, map[string]string{"key": args[0]}),
			labeledGauge("cardinal_get_value_bytes", "Size of the value returned by GET.", float64(len(value)), map[string]string{"key": args[0]}),
		})
	default:
		_, err := fmt.Fprintln(writer, value)
		return err
	}
}

func runSet(ctx context.Context, client redis.UniversalClient, format cardinal.OutputFormat, args []string, writer io.Writer) error {
	if len(args) < 2 || len(args) > 3 {
		return cardinal.NewUsageError("set requires KEY VALUE [TTL]")
	}

	var ttl time.Duration
	if len(args) == 3 {
		parsed, err := cardinal.ParseDuration(args[2])
		if err != nil {
			return cardinal.NewUsageError("parse TTL: %v", err)
		}
		ttl = parsed
	}

	value, err := client.Set(ctx, args[0], args[1], ttl).Result()
	if err != nil {
		return err
	}
	switch format {
	case cardinal.FormatJSON:
		return cardinal.WriteJSON(writer, map[string]interface{}{
			"ok":         true,
			"key":        args[0],
			"result":     value,
			"ttl_ns":     ttl,
			"value_size": len(args[1]),
		})
	case cardinal.FormatPrometheus:
		return cardinal.WritePrometheus(writer, []cardinal.Metric{
			counter("cardinal_set_total", "Number of successful SET commands.", 1),
			labeledGauge("cardinal_set_ttl_seconds", "TTL supplied to the SET command.", ttl.Seconds(), map[string]string{"key": args[0]}),
		})
	default:
		if ttl > 0 {
			_, err := fmt.Fprintf(writer, "%s ttl=%s\n", value, ttl)
			return err
		}
		_, err := fmt.Fprintln(writer, value)
		return err
	}
}

func runTTL(ctx context.Context, client redis.UniversalClient, format cardinal.OutputFormat, args []string, writer io.Writer) error {
	if len(args) != 1 {
		return cardinal.NewUsageError("ttl requires KEY")
	}

	ttl, err := client.TTL(ctx, args[0]).Result()
	if err != nil {
		return err
	}

	status := "expiring"
	switch ttl {
	case -2 * time.Second:
		status = "missing"
	case -1 * time.Second:
		status = "persistent"
	}
	switch format {
	case cardinal.FormatJSON:
		return cardinal.WriteJSON(writer, map[string]interface{}{
			"ok":     status != "missing",
			"key":    args[0],
			"status": status,
			"ttl_ns": ttl,
		})
	case cardinal.FormatPrometheus:
		value := ttl.Seconds()
		if value < 0 {
			value = 0
		}
		return cardinal.WritePrometheus(writer, []cardinal.Metric{
			labeledGauge("cardinal_ttl_seconds", "Remaining key TTL. Missing and persistent keys report zero.", value,
				map[string]string{"key": args[0], "status": status}),
		})
	default:
		_, err := fmt.Fprintln(writer, status)
		return err
	}
}

func runScan(ctx context.Context, client redis.UniversalClient, format cardinal.OutputFormat, args []string, writer io.Writer) error {
	flags := flag.NewFlagSet("scan", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	count := flags.Int64("count", 100, "SCAN count hint")
	cursor := flags.Uint64("cursor", 0, "starting cursor")
	maxIterations := flags.Int("max-iterations", 0, "maximum SCAN calls")
	limit := flags.Int("limit", 0, "soft key limit")
	if err := flags.Parse(args); err != nil {
		return usageErrorFromFlag("scan", err)
	}
	if flags.NArg() != 1 {
		return cardinal.NewUsageError("scan requires PREFIX")
	}

	report, err := cardinal.Scan(ctx, client, cardinal.ScanOptions{
		Pattern:       flags.Arg(0),
		Count:         *count,
		Cursor:        *cursor,
		MaxIterations: *maxIterations,
		Limit:         *limit,
	})
	if err != nil {
		return err
	}

	switch format {
	case cardinal.FormatJSON:
		return cardinal.WriteJSON(writer, report)
	case cardinal.FormatPrometheus:
		complete := 0.0
		if report.Complete {
			complete = 1
		}
		return cardinal.WritePrometheus(writer, []cardinal.Metric{
			labeledGauge("cardinal_scan_keys", "Number of keys returned by SCAN.", float64(len(report.Keys)), map[string]string{"pattern": report.Pattern}),
			labeledCounter("cardinal_scan_iterations", "Number of SCAN cursor transitions.", float64(len(report.Iterations)), map[string]string{"pattern": report.Pattern}),
			labeledGauge("cardinal_scan_duration_seconds", "Elapsed time for the SCAN session.", report.Duration.Seconds(), map[string]string{"pattern": report.Pattern}),
			labeledGauge("cardinal_scan_next_cursor", "Cursor to resume a truncated SCAN.", float64(report.NextCursor), map[string]string{"pattern": report.Pattern}),
			labeledGauge("cardinal_scan_complete", "Whether the SCAN reached cursor zero.", complete, map[string]string{"pattern": report.Pattern}),
		})
	default:
		for _, iteration := range report.Iterations {
			if _, err := fmt.Fprintf(writer,
				"cursor=%d next_cursor=%d keys=%d duration=%s\n",
				iteration.StartCursor,
				iteration.NextCursor,
				len(iteration.Keys),
				iteration.Duration,
			); err != nil {
				return err
			}
		}
		for _, key := range report.Keys {
			if _, err := fmt.Fprintln(writer, key); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintf(writer,
			"count=%d complete=%t next_cursor=%d duration=%s\n",
			len(report.Keys),
			report.Complete,
			report.NextCursor,
			report.Duration,
		)
		return err
	}
}

func runLatency(ctx context.Context, client redis.UniversalClient, format cardinal.OutputFormat, args []string, writer io.Writer) error {
	flags := flag.NewFlagSet("latency", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	count := flags.Int("count", 100, "number of samples")
	pipeline := flags.Bool("pipeline", false, "use pipeline batches")
	batchSize := flags.Int("batch-size", 100, "commands per pipeline batch")
	if err := flags.Parse(args); err != nil {
		return usageErrorFromFlag("latency", err)
	}
	if flags.NArg() == 0 {
		return cardinal.NewUsageError("latency requires COMMAND [ARGS...]")
	}

	command := flags.Args()
	report, err := cardinal.MeasureWithOptions(ctx, client, command, cardinal.MeasureOptions{
		Count:     *count,
		Pipeline:  *pipeline,
		BatchSize: *batchSize,
	})
	if err != nil {
		return err
	}

	switch format {
	case cardinal.FormatJSON:
		return cardinal.WriteJSON(writer, latencyJSON(report))
	case cardinal.FormatPrometheus:
		return writeLatencyPrometheus(writer, report)
	default:
		_, err := fmt.Fprintf(writer,
			"command=%s mode=%s count=%d min=%s p50=%s p90=%s p95=%s p99=%s max=%s avg=%s stddev=%s throughput=%.2f/s duration=%s\n",
			strings.ToUpper(report.Command),
			report.Mode,
			report.Stats.Count,
			report.Stats.Min,
			report.Stats.P50,
			report.Stats.P90,
			report.Stats.P95,
			report.Stats.P99,
			report.Stats.Max,
			report.Stats.Avg,
			report.Stats.StdDev,
			report.Throughput,
			report.Duration,
		)
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

func latencyJSON(report cardinal.LatencyReport) latencyReportJSON {
	result := latencyReportJSON{
		Command:    strings.ToUpper(report.Command),
		Mode:       report.Mode,
		Count:      report.Stats.Count,
		DurationMS: milliseconds(report.Duration),
		Throughput: report.Throughput,
		MinMS:      milliseconds(report.Stats.Min),
		P50MS:      milliseconds(report.Stats.P50),
		P90MS:      milliseconds(report.Stats.P90),
		P95MS:      milliseconds(report.Stats.P95),
		P99MS:      milliseconds(report.Stats.P99),
		MaxMS:      milliseconds(report.Stats.Max),
		AverageMS:  milliseconds(report.Stats.Avg),
		StdDevMS:   milliseconds(report.Stats.StdDev),
	}
	for _, bucket := range report.Histogram.Buckets {
		result.Histogram = append(result.Histogram, histogramBucketJSON{
			UpperBoundMS: milliseconds(bucket.UpperBound),
			Count:        bucket.Count,
		})
	}
	return result
}

func writeLatencyPrometheus(writer io.Writer, report cardinal.LatencyReport) error {
	labels := map[string]string{
		"command": strings.ToUpper(report.Command),
		"mode":    report.Mode,
	}
	metrics := []cardinal.Metric{
		labeledCounter("cardinal_latency_commands_total", "Number of measured commands.", float64(report.Stats.Count), labels),
	}

	quantiles := []struct {
		name  string
		value time.Duration
	}{
		{name: "min", value: report.Stats.Min},
		{name: "p50", value: report.Stats.P50},
		{name: "p90", value: report.Stats.P90},
		{name: "p95", value: report.Stats.P95},
		{name: "p99", value: report.Stats.P99},
		{name: "max", value: report.Stats.Max},
		{name: "avg", value: report.Stats.Avg},
	}
	for _, quantile := range quantiles {
		metrics = append(metrics, labeledGauge("cardinal_latency_seconds",
			"Summary percentiles for command latency.",
			quantile.value.Seconds(),
			mergeLabels(labels, "quantile", quantile.name)))
	}
	metrics = append(metrics, labeledGauge("cardinal_latency_throughput_operations_per_second",
		"Measured command throughput.", report.Throughput, labels))
	metrics = append(metrics,
		report.Histogram.Metrics("cardinal_latency_histogram_seconds",
			"Histogram of measured command latency.", labels)...)
	return cardinal.WritePrometheus(writer, metrics)
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
	default:
		return cardinal.NewUsageError("unknown command %q", args[0])
	}
}
