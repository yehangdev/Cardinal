package cardinal

import (
	"testing"
	"time"
)

func TestSummarize(t *testing.T) {
	stats, err := Summarize([]time.Duration{
		40 * time.Millisecond,
		10 * time.Millisecond,
		30 * time.Millisecond,
		20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Summarize returned error: %v", err)
	}

	if stats.Count != 4 {
		t.Fatalf("Count = %d, want 4", stats.Count)
	}
	if stats.Min != 10*time.Millisecond {
		t.Errorf("Min = %s, want 10ms", stats.Min)
	}
	if stats.P50 != 20*time.Millisecond {
		t.Errorf("P50 = %s, want 20ms", stats.P50)
	}
	if stats.P95 != 40*time.Millisecond {
		t.Errorf("P95 = %s, want 40ms", stats.P95)
	}
	if stats.Max != 40*time.Millisecond {
		t.Errorf("Max = %s, want 40ms", stats.Max)
	}
	if stats.Avg != 25*time.Millisecond {
		t.Errorf("Avg = %s, want 25ms", stats.Avg)
	}
}

func TestSummarizeRejectsEmptyInput(t *testing.T) {
	if _, err := Summarize(nil); err == nil {
		t.Fatal("Summarize(nil) returned no error")
	}
}
