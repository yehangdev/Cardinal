package cardinal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type cursorScanner struct {
	pages map[uint64]struct {
		keys []string
		next uint64
	}
}

func (s *cursorScanner) Scan(_ context.Context, cursor uint64, _ string, _ int64) *redis.ScanCmd {
	page := s.pages[cursor]
	return redis.NewScanCmdResult(page.keys, page.next, nil)
}

func testRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	server := miniredis.RunT(t)
	return server
}

func TestScanReportTracksCursorTransitions(t *testing.T) {
	client := &cursorScanner{pages: map[uint64]struct {
		keys []string
		next uint64
	}{
		0: {keys: []string{"user:1"}, next: 7},
		7: {keys: []string{"user:2"}, next: 9},
		9: {keys: []string{"user:3"}},
	}}

	report, err := Scan(context.Background(), client, ScanOptions{
		Pattern: "user:",
		Count:   1,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !report.Complete || report.NextCursor != 0 {
		t.Fatalf("unexpected completion state: %+v", report)
	}
	if len(report.Keys) != 3 || len(report.Iterations) != 3 {
		t.Fatalf("keys=%d iterations=%d", len(report.Keys), len(report.Iterations))
	}
	if report.Iterations[0].StartCursor != 0 || report.Iterations[0].NextCursor != 7 {
		t.Fatalf("first transition: %+v", report.Iterations[0])
	}
	if report.Iterations[2].NextCursor != 0 {
		t.Fatalf("last transition: %+v", report.Iterations[2])
	}
}

func TestScanCanResumeFromReportedCursor(t *testing.T) {
	client := &cursorScanner{pages: map[uint64]struct {
		keys []string
		next uint64
	}{
		0: {keys: []string{"a", "b"}, next: 2},
		2: {keys: []string{"c", "d"}},
	}}

	first, err := Scan(context.Background(), client, ScanOptions{
		Pattern:       "*",
		Count:         2,
		MaxIterations: 1,
	})
	if err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	if !first.Truncated || first.Complete || first.NextCursor == 0 {
		t.Fatalf("first report did not stop at a cursor: %+v", first)
	}

	second, err := Scan(context.Background(), client, ScanOptions{
		Pattern: "*",
		Count:   2,
		Cursor:  first.NextCursor,
	})
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if !second.Complete || len(second.Keys) != 2 {
		t.Fatalf("unexpected resumed report: %+v", second)
	}
}

func TestExecutePipelineReportsValuesAndMisses(t *testing.T) {
	server := testRedis(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	operations := []PipelineOperation{
		{Command: "set", Key: "a", Value: "one"},
		{Command: "set", Key: "b", Value: "two"},
		{Command: "get", Key: "a"},
		{Command: "get", Key: "missing"},
		{Command: "del", Key: "a"},
		{Command: "incr", Key: "counter"},
		{Command: "expire", Key: "b", TTL: time.Minute},
	}
	report, err := ExecutePipeline(context.Background(), client, operations)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if report.Succeeded != 6 || report.Misses != 1 || report.Failed != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Operations[2].Value != "one" {
		t.Fatalf("GET value = %#v", report.Operations[2].Value)
	}
	if report.Operations[3].Status != "miss" {
		t.Fatalf("missing GET status = %q", report.Operations[3].Status)
	}
}

func TestConfiguredClientUsesACL(t *testing.T) {
	server := testRedis(t)
	server.RequireUserAuth("app", "secret")
	client, err := NewConfiguredClient(Config{
		Mode:     "standalone",
		Address:  server.Addr(),
		Username: "app",
		Password: "secret",
		Timeout:  Duration(time.Second),
	})
	if err != nil {
		t.Fatalf("NewConfiguredClient: %v", err)
	}
	defer client.Close()

	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("Ping with ACL: %v", err)
	}

	badClient, err := NewConfiguredClient(Config{
		Mode:     "standalone",
		Address:  server.Addr(),
		Username: "app",
		Password: "wrong",
		Timeout:  Duration(time.Second),
	})
	if err != nil {
		t.Fatalf("NewConfiguredClient with wrong password: %v", err)
	}
	defer badClient.Close()
	if err := badClient.Ping(context.Background()).Err(); err == nil {
		t.Fatal("Ping with wrong password returned no error")
	}
}

func TestMeasureWithOptionsBuildsHistogram(t *testing.T) {
	server := testRedis(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	report, err := MeasureWithOptions(
		context.Background(),
		client,
		[]string{"PING"},
		MeasureOptions{
			Count:     5,
			Pipeline:  true,
			BatchSize: 2,
			Buckets:   []time.Duration{time.Millisecond, 10 * time.Millisecond},
		},
	)
	if err != nil {
		t.Fatalf("MeasureWithOptions: %v", err)
	}
	if report.Stats.Count != 5 || report.Histogram.Count != 5 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Stats.P99 < report.Stats.P95 || report.Stats.StdDev < 0 {
		t.Fatalf("invalid percentile data: %+v", report.Stats)
	}
}

func TestExecutePipelineRejectsUnknownCommand(t *testing.T) {
	_, err := ExecutePipeline(context.Background(), redis.NewClient(&redis.Options{}), []PipelineOperation{{
		Command: "FLUSHALL",
	}})
	if err == nil || errors.Is(err, redis.Nil) {
		t.Fatalf("ExecutePipeline error = %v", err)
	}
}
