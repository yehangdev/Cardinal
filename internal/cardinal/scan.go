package cardinal

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ScanOptions controls one cursor-based SCAN session.
type ScanOptions struct {
	Pattern       string
	Count         int64
	Cursor        uint64
	MaxIterations int
	Limit         int
}

// ScanClient is the command subset required for cursor walking.
type ScanClient interface {
	Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd
}

// ScanIteration records one SCAN call and its cursor transition.
type ScanIteration struct {
	Index       int
	StartCursor uint64
	NextCursor  uint64
	Keys        []string
	Duration    time.Duration
}

// ScanReport summarizes a cursor-based SCAN session.
type ScanReport struct {
	Pattern     string
	CountHint   int64
	StartCursor uint64
	NextCursor  uint64
	Complete    bool
	Truncated   bool
	Keys        []string
	Iterations  []ScanIteration
	Duration    time.Duration
}

// ScanKeys returns all keys matching a literal prefix.
func ScanKeys(ctx context.Context, client redis.UniversalClient, prefix string, count int64) ([]string, error) {
	report, err := Scan(ctx, client, ScanOptions{
		Pattern: scanPattern(prefix),
		Count:   count,
	})
	if err != nil {
		return nil, err
	}

	keys := append([]string(nil), report.Keys...)
	sort.Strings(keys)
	return keys, nil
}

// Scan walks a Redis cursor and reports every transition. A non-zero
// NextCursor permits a later call to resume from exactly that point.
func Scan(ctx context.Context, client ScanClient, options ScanOptions) (ScanReport, error) {
	if client == nil {
		return ScanReport{}, errors.New("redis client is nil")
	}
	if options.Count < 1 {
		options.Count = 100
	}
	if options.MaxIterations < 0 {
		return ScanReport{}, errors.New("max iterations must not be negative")
	}
	if options.Limit < 0 {
		return ScanReport{}, errors.New("limit must not be negative")
	}
	if options.Pattern == "" {
		options.Pattern = "*"
	}
	options.Pattern = scanPattern(options.Pattern)

	report := ScanReport{
		Pattern:     options.Pattern,
		CountHint:   options.Count,
		StartCursor: options.Cursor,
	}
	startedAt := time.Now()
	cursor := options.Cursor

	for {
		if options.MaxIterations > 0 && len(report.Iterations) >= options.MaxIterations {
			report.Truncated = cursor != 0
			break
		}

		iterationStarted := time.Now()
		batch, next, err := client.Scan(ctx, cursor, options.Pattern, options.Count).Result()
		if err != nil {
			return report, err
		}
		report.Iterations = append(report.Iterations, ScanIteration{
			Index:       len(report.Iterations),
			StartCursor: cursor,
			NextCursor:  next,
			Keys:        append([]string(nil), batch...),
			Duration:    time.Since(iterationStarted),
		})
		report.Keys = append(report.Keys, batch...)
		cursor = next
		if cursor == 0 {
			report.Complete = true
			break
		}
		if options.Limit > 0 && len(report.Keys) >= options.Limit {
			report.Truncated = true
			break
		}
	}

	report.NextCursor = cursor
	report.Duration = time.Since(startedAt)
	return report, nil
}

func scanPattern(prefix string) string {
	if strings.HasSuffix(prefix, "*") {
		return prefix
	}
	return prefix + "*"
}
