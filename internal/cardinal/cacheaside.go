package cardinal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Store is the subset of cache operations used by the comparison flow.
type Store interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
}

// CacheAsideResult records timings from one cache-aside comparison.
type CacheAsideResult struct {
	CacheHit    bool          `json:"cache_hit"`
	Value       string        `json:"value"`
	CacheLookup time.Duration `json:"cache_lookup_ns"`
	OriginFetch time.Duration `json:"origin_fetch_ns"`
	Fill        time.Duration `json:"fill_ns"`
	HitLookup   time.Duration `json:"hit_lookup_ns"`
}

// OriginSaved reports the follow-up cache hit advantage after a miss and fill.
func (r CacheAsideResult) OriginSaved() time.Duration {
	if r.CacheHit {
		return 0
	}
	return r.OriginFetch - r.HitLookup
}

// Total reports the observed request-path cost in this comparison.
func (r CacheAsideResult) Total() time.Duration {
	if r.CacheHit {
		return r.CacheLookup
	}
	return r.CacheLookup + r.OriginFetch + r.Fill + r.HitLookup
}

// CompareCacheAside performs a lookup, fills on miss, and measures a follow-up hit.
func CompareCacheAside(
	ctx context.Context,
	store Store,
	key string,
	ttl time.Duration,
	originDelay time.Duration,
	origin func() string,
) (CacheAsideResult, error) {
	lookupStart := time.Now()
	value, err := store.Get(ctx, key)
	cacheLookup := time.Since(lookupStart)

	if err == nil {
		return CacheAsideResult{
			CacheHit:    true,
			Value:       value,
			CacheLookup: cacheLookup,
		}, nil
	}
	if !errors.Is(err, redis.Nil) {
		return CacheAsideResult{}, err
	}
	if origin == nil {
		return CacheAsideResult{}, errors.New("origin function is nil")
	}

	originStart := time.Now()
	if err := wait(ctx, originDelay); err != nil {
		return CacheAsideResult{}, err
	}
	value = origin()
	originFetch := time.Since(originStart)

	fillStart := time.Now()
	if err := store.Set(ctx, key, value, ttl); err != nil {
		return CacheAsideResult{}, err
	}
	fill := time.Since(fillStart)

	hitStart := time.Now()
	filledValue, err := store.Get(ctx, key)
	hitLookup := time.Since(hitStart)
	if err != nil {
		return CacheAsideResult{}, fmt.Errorf("read filled cache entry: %w", err)
	}

	return CacheAsideResult{
		Value:       filledValue,
		CacheLookup: cacheLookup,
		OriginFetch: originFetch,
		Fill:        fill,
		HitLookup:   hitLookup,
	}, nil
}

func wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
