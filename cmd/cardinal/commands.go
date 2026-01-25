package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"cardinal/internal/cardinal"

	"github.com/redis/go-redis/v9"
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

func runPipeline(ctx context.Context, client redis.UniversalClient, format cardinal.OutputFormat, args []string, writer io.Writer) error {
	flags := flag.NewFlagSet("pipeline", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	value := flags.String("value", "", "value for SET operations")
	ttl := flags.String("ttl", "0s", "TTL for SET or EXPIRE operations")
	repeat := flags.Int("repeat", 1, "repeat the key list")
	if err := flags.Parse(args); err != nil {
		return usageErrorFromFlag("pipeline", err)
	}
	if flags.NArg() < 2 {
		return cardinal.NewUsageError("pipeline requires OP KEY [KEY...]")
	}

	parsedTTL, err := cardinal.ParseDuration(*ttl)
	if err != nil {
		return durationFlagError("ttl", err)
	}
	operations, err := cardinal.RepeatPipelineOperations(
		flags.Arg(0),
		flags.Args()[1:],
		*value,
		parsedTTL,
		*repeat,
	)
	if err != nil {
		return cardinal.NewUsageError("%v", err)
	}

	report, err := cardinal.ExecutePipeline(ctx, client, operations)
	if err != nil {
		return err
	}
	switch format {
	case cardinal.FormatJSON:
		return cardinal.WriteJSON(writer, pipelineJSON(report))
	case cardinal.FormatPrometheus:
		return writePipelinePrometheus(writer, report)
	default:
		for _, result := range report.Operations {
			if result.Error != "" {
				if _, err := fmt.Fprintf(writer, "index=%d command=%s key=%s status=%s error=%s\n",
					result.Index, result.Command, result.Key, result.Status, result.Error); err != nil {
					return err
				}
				continue
			}
			if _, err := fmt.Fprintf(writer, "index=%d command=%s key=%s status=%s value=%v\n",
				result.Index, result.Command, result.Key, result.Status, result.Value); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintf(writer,
			"count=%d succeeded=%d misses=%d failed=%d duration=%s\n",
			len(report.Operations),
			report.Succeeded,
			report.Misses,
			report.Failed,
			report.Duration,
		)
		return err
	}
}

func runCacheAside(ctx context.Context, client redis.UniversalClient, format cardinal.OutputFormat, args []string, writer io.Writer) error {
	flags := flag.NewFlagSet("cache-aside", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	ttl := flags.String("ttl", "1m", "cache fill TTL")
	originDelay := flags.String("origin-delay", "25ms", "simulated origin latency")
	value := flags.String("value", "", "origin value; generated when empty")
	if err := flags.Parse(args); err != nil {
		return usageErrorFromFlag("cache-aside", err)
	}
	if flags.NArg() != 1 {
		return cardinal.NewUsageError("cache-aside requires KEY")
	}

	parsedTTL, err := cardinal.ParseDuration(*ttl)
	if err != nil {
		return durationFlagError("ttl", err)
	}
	parsedDelay, err := cardinal.ParseDuration(*originDelay)
	if err != nil {
		return durationFlagError("origin-delay", err)
	}

	key := flags.Arg(0)
	origin := func() string {
		if *value != "" {
			return *value
		}
		return fmt.Sprintf("%s:%d", key, time.Now().UnixNano())
	}

	result, err := cardinal.CompareCacheAside(
		ctx,
		cardinal.NewRedisStore(client),
		key,
		parsedTTL,
		parsedDelay,
		origin,
	)
	if err != nil {
		return err
	}

	switch format {
	case cardinal.FormatJSON:
		return cardinal.WriteJSON(writer, cacheAsideJSON(result))
	case cardinal.FormatPrometheus:
		return writeCacheAsidePrometheus(writer, result)
	default:
		if result.CacheHit {
			_, err := fmt.Fprintf(writer,
				"result=hit\nvalue=%s\ncache_lookup=%s\ntotal=%s\norigin=skipped\n",
				result.Value,
				result.CacheLookup,
				result.Total(),
			)
			return err
		}
		_, err := fmt.Fprintf(writer,
			"result=miss\ncache_lookup=%s\norigin_fetch=%s\nfill=%s\nfollow_up_hit=%s\norigin_saved=%s\ntotal=%s\nvalue=%s\n",
			result.CacheLookup,
			result.OriginFetch,
			result.Fill,
			result.HitLookup,
			result.OriginSaved(),
			result.Total(),
			result.Value,
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

func pipelineJSON(report cardinal.PipelineReport) map[string]interface{} {
	operations := make([]map[string]interface{}, 0, len(report.Operations))
	for _, result := range report.Operations {
		operations = append(operations, map[string]interface{}{
			"index":       result.Index,
			"command":     result.Command,
			"key":         result.Key,
			"status":      result.Status,
			"value":       result.Value,
			"error":       result.Error,
			"duration_ns": result.Duration,
		})
	}
	return map[string]interface{}{
		"ok":          report.Failed == 0,
		"count":       len(report.Operations),
		"succeeded":   report.Succeeded,
		"misses":      report.Misses,
		"failed":      report.Failed,
		"duration_ns": report.Duration,
		"operations":  operations,
	}
}

func writePipelinePrometheus(writer io.Writer, report cardinal.PipelineReport) error {
	counts := make(map[string]int)
	for _, result := range report.Operations {
		key := result.Command + "\x00" + result.Status
		counts[key]++
	}

	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	metrics := make([]cardinal.Metric, 0, len(keys)+2)
	for _, key := range keys {
		parts := strings.SplitN(key, "\x00", 2)
		metrics = append(metrics, labeledCounter("cardinal_pipeline_operations_total",
			"Pipeline command results by command and status.",
			float64(counts[key]),
			map[string]string{"command": parts[0], "status": parts[1]}))
	}
	metrics = append(metrics,
		gauge("cardinal_pipeline_duration_seconds", "Elapsed time for the pipeline round trip.", report.Duration.Seconds()),
		gauge("cardinal_pipeline_failed", "Number of failed pipeline operations.", float64(report.Failed)),
	)
	return cardinal.WritePrometheus(writer, metrics)
}

func cacheAsideJSON(result cardinal.CacheAsideResult) map[string]interface{} {
	resultName := "miss"
	if result.CacheHit {
		resultName = "hit"
	}
	return map[string]interface{}{
		"ok":               true,
		"result":           resultName,
		"cache_hit":        result.CacheHit,
		"value":            result.Value,
		"cache_lookup_ms":  milliseconds(result.CacheLookup),
		"origin_fetch_ms":  milliseconds(result.OriginFetch),
		"fill_ms":          milliseconds(result.Fill),
		"follow_up_hit_ms": milliseconds(result.HitLookup),
		"origin_saved_ms":  milliseconds(result.OriginSaved()),
		"total_ms":         milliseconds(result.Total()),
	}
}

func writeCacheAsidePrometheus(writer io.Writer, result cardinal.CacheAsideResult) error {
	resultName := "miss"
	if result.CacheHit {
		resultName = "hit"
	}
	labels := map[string]string{"result": resultName}
	return cardinal.WritePrometheus(writer, []cardinal.Metric{
		labeledGauge("cardinal_cache_aside_lookup_seconds", "Initial cache lookup duration.", result.CacheLookup.Seconds(), labels),
		labeledGauge("cardinal_cache_aside_origin_seconds", "Simulated origin fetch duration.", result.OriginFetch.Seconds(), labels),
		labeledGauge("cardinal_cache_aside_fill_seconds", "Cache fill duration after a miss.", result.Fill.Seconds(), labels),
		labeledGauge("cardinal_cache_aside_follow_up_hit_seconds", "Follow-up cache hit duration after a fill.", result.HitLookup.Seconds(), labels),
		labeledGauge("cardinal_cache_aside_origin_saved_seconds", "Origin time avoided by a follow-up cache hit.", result.OriginSaved().Seconds(), labels),
		labeledGauge("cardinal_cache_aside_total_seconds", "Total duration of the comparison sequence.", result.Total().Seconds(), labels),
	})
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
