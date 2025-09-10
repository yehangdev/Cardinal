package cardinal

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type memoryStore struct {
	values map[string]string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{values: make(map[string]string)}
}

func (s *memoryStore) Get(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", redis.Nil
	}
	return value, nil
}

func (s *memoryStore) Set(_ context.Context, key, value string, _ time.Duration) error {
	s.values[key] = value
	return nil
}

func TestCompareCacheAsideFillsAfterMiss(t *testing.T) {
	store := newMemoryStore()
	originCalls := 0

	result, err := CompareCacheAside(
		context.Background(),
		store,
		"user:42",
		time.Minute,
		0,
		func() string {
			originCalls++
			return "active"
		},
	)
	if err != nil {
		t.Fatalf("CompareCacheAside returned error: %v", err)
	}

	if result.CacheHit {
		t.Fatal("CacheHit = true, want false")
	}
	if result.Value != "active" {
		t.Errorf("Value = %q, want active", result.Value)
	}
	if originCalls != 1 {
		t.Errorf("origin calls = %d, want 1", originCalls)
	}
	if store.values["user:42"] != "active" {
		t.Errorf("stored value = %q, want active", store.values["user:42"])
	}
}

func TestCompareCacheAsideUsesExistingValue(t *testing.T) {
	store := newMemoryStore()
	store.values["user:42"] = "cached"
	originCalls := 0

	result, err := CompareCacheAside(
		context.Background(),
		store,
		"user:42",
		time.Minute,
		0,
		func() string {
			originCalls++
			return "origin"
		},
	)
	if err != nil {
		t.Fatalf("CompareCacheAside returned error: %v", err)
	}

	if !result.CacheHit {
		t.Fatal("CacheHit = false, want true")
	}
	if result.Value != "cached" {
		t.Errorf("Value = %q, want cached", result.Value)
	}
	if originCalls != 0 {
		t.Errorf("origin calls = %d, want 0", originCalls)
	}
}
