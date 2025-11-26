package cardinal

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewRedisClient returns a standalone client for backward-compatible callers.
func NewRedisClient(addr string, db int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: addr,
		DB:   db,
	})
}

// NewConfiguredClient builds a standalone, Sentinel, or Cluster client from
// validated configuration.
func NewConfiguredClient(config Config) (redis.UniversalClient, error) {
	config.Mode = strings.ToLower(strings.TrimSpace(config.Mode))
	if config.Mode == "" {
		config.Mode = "standalone"
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}

	tlsConfig, err := BuildTLSConfig(config.TLS)
	if err != nil {
		return nil, err
	}

	timeout := config.Timeout.Std()
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	switch config.Mode {
	case "standalone":
		return newStandalone(config, tlsConfig, timeout), nil
	case "sentinel":
		return newSentinelClient(config, tlsConfig, timeout), nil
	case "cluster":
		return newClusterClient(config, tlsConfig, timeout), nil
	default:
		return nil, fmt.Errorf("unsupported mode %q", config.Mode)
	}
}

func newStandalone(config Config, tlsConfig *tls.Config, timeout time.Duration) *redis.Client {
	network := "tcp"
	address := strings.TrimSpace(config.Address)
	if config.UnixSocket != "" {
		network = "unix"
		address = config.UnixSocket
	}

	return redis.NewClient(&redis.Options{
		Network:               network,
		Addr:                  address,
		DB:                    config.DB,
		Username:              config.Username,
		Password:              config.Password,
		TLSConfig:             tlsConfig,
		DialTimeout:           timeout,
		ReadTimeout:           timeout,
		WriteTimeout:          timeout,
		ContextTimeoutEnabled: true,
	})
}

func newSentinelClient(config Config, tlsConfig *tls.Config, timeout time.Duration) *redis.Client {
	username := config.Sentinel.Username
	if username == "" {
		username = config.Username
	}
	password := config.Sentinel.Password
	if password == "" {
		password = config.Password
	}
	db := config.Sentinel.DB
	if db == 0 {
		db = config.DB
	}

	return redis.NewFailoverClient(&redis.FailoverOptions{
		MasterName:            config.Sentinel.MasterName,
		SentinelAddrs:         append([]string(nil), config.Sentinel.Addresses...),
		SentinelUsername:      config.Sentinel.SentinelUsername,
		SentinelPassword:      config.Sentinel.SentinelPassword,
		Username:              username,
		Password:              password,
		DB:                    db,
		TLSConfig:             tlsConfig,
		DialTimeout:           timeout,
		ReadTimeout:           timeout,
		WriteTimeout:          timeout,
		ContextTimeoutEnabled: true,
	})
}

func newClusterClient(config Config, tlsConfig *tls.Config, timeout time.Duration) *redis.ClusterClient {
	return redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:                 append([]string(nil), config.Cluster.Addresses...),
		Username:              config.Username,
		Password:              config.Password,
		TLSConfig:             tlsConfig,
		MaxRedirects:          config.Cluster.MaxRedirects,
		RouteByLatency:        config.Cluster.RouteByLatency,
		RouteRandomly:         config.Cluster.RouteRandomly,
		ReadOnly:              config.Cluster.ReadOnly,
		DialTimeout:           timeout,
		ReadTimeout:           timeout,
		WriteTimeout:          timeout,
		ContextTimeoutEnabled: true,
	})
}

// RedisStore adapts any Redis client to the cache-aside Store interface.
type RedisStore struct {
	client redis.UniversalClient
}

// NewRedisStore wraps a Redis client.
func NewRedisStore(client redis.UniversalClient) *RedisStore {
	return &RedisStore{client: client}
}

// Get returns a cached value.
func (s *RedisStore) Get(ctx context.Context, key string) (string, error) {
	if s == nil || s.client == nil {
		return "", errors.New("redis store has no client")
	}
	return s.client.Get(ctx, key).Result()
}

// Set writes a cached value with an optional expiration.
func (s *RedisStore) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if s == nil || s.client == nil {
		return errors.New("redis store has no client")
	}
	return s.client.Set(ctx, key, value, ttl).Err()
}
