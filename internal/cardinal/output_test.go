package cardinal

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWriteJSON(t *testing.T) {
	var output bytes.Buffer
	if err := WriteJSON(&output, map[string]interface{}{"ok": true, "count": 2}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if decoded["ok"] != true || decoded["count"] != float64(2) {
		t.Fatalf("unexpected JSON: %s", output.String())
	}
}

func TestWritePrometheusHistogram(t *testing.T) {
	histogram := NewHistogram(
		[]time.Duration{500 * time.Microsecond, 2 * time.Millisecond, 20 * time.Millisecond},
		[]time.Duration{time.Millisecond, 10 * time.Millisecond},
	)
	var output bytes.Buffer
	err := WritePrometheus(&output, histogram.Metrics(
		"cardinal_latency_seconds",
		"Latency samples.",
		map[string]string{"command": "GET"},
	))
	if err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}

	for _, fragment := range []string{
		"# TYPE cardinal_latency_seconds histogram",
		`cardinal_latency_seconds_bucket{command="GET",le="0.001"} 1`,
		`cardinal_latency_seconds_bucket{command="GET",le="0.01"} 2`,
		`cardinal_latency_seconds_bucket{command="GET",le="+Inf"} 3`,
		"cardinal_latency_seconds_count",
	} {
		if !strings.Contains(output.String(), fragment) {
			t.Errorf("Prometheus output missing %q:\n%s", fragment, output.String())
		}
	}
}

func TestWritePrometheusEscapesLabels(t *testing.T) {
	var output bytes.Buffer
	err := WritePrometheus(&output, []Metric{{
		Name:   "cardinal_probe_info",
		Help:   "Probe details.",
		Type:   MetricGauge,
		Labels: map[string]string{"note": "line\n\"quoted\""},
		Value:  1,
	}})
	if err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	if !strings.Contains(output.String(), `note="line\n\"quoted\""`) {
		t.Fatalf("labels were not escaped:\n%s", output.String())
	}
}

func TestParseOutputFormat(t *testing.T) {
	for _, input := range []string{"text", "JSON", " prometheus "} {
		if _, err := ParseOutputFormat(input); err != nil {
			t.Errorf("ParseOutputFormat(%q): %v", input, err)
		}
	}
	if _, err := ParseOutputFormat("xml"); err == nil {
		t.Fatal("ParseOutputFormat(xml) returned no error")
	}
}

func TestRepeatPipelineOperations(t *testing.T) {
	operations, err := RepeatPipelineOperations("set", []string{"a", "b"}, "value", time.Minute, 2)
	if err != nil {
		t.Fatalf("RepeatPipelineOperations: %v", err)
	}
	if len(operations) != 4 {
		t.Fatalf("operations = %d, want 4", len(operations))
	}
	if operations[2].Key != "a" || operations[2].Command != "SET" {
		t.Fatalf("unexpected repeated operation: %+v", operations[2])
	}
	if _, err := RepeatPipelineOperations("set", []string{"a"}, "", 0, 1); err == nil {
		t.Fatal("SET without a value returned no error")
	}
}
