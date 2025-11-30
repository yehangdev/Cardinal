package cardinal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// OutputFormat controls command rendering.
type OutputFormat string

const (
	FormatText       OutputFormat = "text"
	FormatJSON       OutputFormat = "json"
	FormatPrometheus OutputFormat = "prometheus"
)

// ParseOutputFormat validates a user-supplied format name.
func ParseOutputFormat(input string) (OutputFormat, error) {
	format := OutputFormat(strings.ToLower(strings.TrimSpace(input)))
	switch format {
	case FormatText, FormatJSON, FormatPrometheus:
		return format, nil
	default:
		return "", fmt.Errorf("unsupported output format %q (want text, json, or prometheus)", input)
	}
}

// WriteJSON writes an indented JSON value followed by a newline.
func WriteJSON(writer io.Writer, value interface{}) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode JSON output: %w", err)
	}
	return nil
}

// MetricType identifies the Prometheus metric family type.
type MetricType string

const (
	MetricCounter   MetricType = "counter"
	MetricGauge     MetricType = "gauge"
	MetricHistogram MetricType = "histogram"
)

// Metric is one Prometheus text exposition sample or histogram.
type Metric struct {
	Name    string
	Help    string
	Type    MetricType
	Labels  map[string]string
	Value   float64
	Buckets []HistogramBucket
	Count   uint64
	Sum     float64
}

// HistogramBucket stores one cumulative upper-bounded bucket.
type HistogramBucket struct {
	UpperBound time.Duration
	Count      uint64
}

// Histogram aggregates latency samples into cumulative buckets.
type Histogram struct {
	Buckets []HistogramBucket
	Count   uint64
	Sum     time.Duration
}

// DefaultLatencyBuckets returns practical cache-probe latency boundaries.
func DefaultLatencyBuckets() []time.Duration {
	return []time.Duration{
		50 * time.Microsecond, 100 * time.Microsecond, 250 * time.Microsecond,
		500 * time.Microsecond, time.Millisecond, 2 * time.Millisecond,
		5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond,
		50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond,
		500 * time.Millisecond, time.Second, 5 * time.Second, 10 * time.Second,
	}
}

// NewHistogram calculates cumulative buckets for the supplied boundaries.
func NewHistogram(samples []time.Duration, boundaries []time.Duration) Histogram {
	histogram := Histogram{
		Buckets: make([]HistogramBucket, len(boundaries)),
		Count:   uint64(len(samples)),
	}
	for index, boundary := range boundaries {
		histogram.Buckets[index].UpperBound = boundary
	}

	for _, sample := range samples {
		histogram.Sum += sample
		for index := range histogram.Buckets {
			if sample <= histogram.Buckets[index].UpperBound {
				histogram.Buckets[index].Count++
			}
		}
	}
	return histogram
}

// Metrics converts a histogram into a Prometheus histogram family.
func (h Histogram) Metrics(name, help string, labels map[string]string) []Metric {
	return []Metric{{Name: name, Help: help, Type: MetricHistogram, Labels: labels, Buckets: h.Buckets, Count: h.Count, Sum: h.Sum.Seconds()}}
}

// WritePrometheus writes metrics in the Prometheus text exposition format.
func WritePrometheus(writer io.Writer, metrics []Metric) error {
	seenFamilies := make(map[string]bool)
	for _, metric := range metrics {
		if err := validateMetric(metric); err != nil {
			return err
		}

		if !seenFamilies[metric.Name] && metric.Help != "" {
			if _, err := fmt.Fprintf(writer, "# HELP %s %s\n", metric.Name, escapeHelp(metric.Help)); err != nil {
				return err
			}
			seenFamilies[metric.Name] = true
		}
		if !seenFamilies[metric.Name+"\x00type"] {
			if _, err := fmt.Fprintf(writer, "# TYPE %s %s\n", metric.Name, metric.Type); err != nil {
				return err
			}
			seenFamilies[metric.Name+"\x00type"] = true
		}

		switch metric.Type {
		case MetricHistogram:
			if err := writeHistogram(writer, metric); err != nil {
				return err
			}
		case MetricCounter, MetricGauge:
			if _, err := fmt.Fprintf(writer, "%s%s %s\n",
				metric.Name,
				formatLabels(metric.Labels),
				formatFloat(metric.Value),
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMetric(metric Metric) error {
	if metric.Name == "" {
		return errors.New("metric name is empty")
	}
	for _, character := range metric.Name {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == ':' {
			continue
		}
		return fmt.Errorf("invalid metric name %q", metric.Name)
	}
	switch metric.Type {
	case MetricCounter, MetricGauge, MetricHistogram:
		return nil
	default:
		return fmt.Errorf("invalid metric type %q", metric.Type)
	}
}

func writeHistogram(writer io.Writer, metric Metric) error {
	for _, bucket := range metric.Buckets {
		labels := cloneLabels(metric.Labels)
		labels["le"] = bucketLabel(bucket.UpperBound)
		if _, err := fmt.Fprintf(writer, "%s_bucket%s %d\n",
			metric.Name,
			formatLabels(labels),
			bucket.Count,
		); err != nil {
			return err
		}
	}

	labels := cloneLabels(metric.Labels)
	labels["le"] = "+Inf"
	if _, err := fmt.Fprintf(writer, "%s_bucket%s %d\n",
		metric.Name,
		formatLabels(labels),
		metric.Count,
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "%s_sum%s %s\n",
		metric.Name,
		formatLabels(metric.Labels),
		formatFloat(metric.Sum),
	); err != nil {
		return err
	}
	_, err := fmt.Fprintf(writer, "%s_count%s %d\n",
		metric.Name,
		formatLabels(metric.Labels),
		metric.Count,
	)
	return err
}

func cloneLabels(labels map[string]string) map[string]string {
	result := make(map[string]string, len(labels)+1)
	for key, value := range labels {
		result[key] = value
	}
	return result
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	builder.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(key)
		builder.WriteString("=\"")
		builder.WriteString(escapeLabel(labels[key]))
		builder.WriteByte('"')
	}
	builder.WriteByte('}')
	return builder.String()
}

func escapeLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func escapeHelp(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, "\n", `\n`)
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func bucketLabel(value time.Duration) string {
	if value == 0 {
		return "0"
	}
	return strconv.FormatFloat(value.Seconds(), 'g', -1, 64)
}
