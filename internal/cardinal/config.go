package cardinal

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"strconv"
	"strings"
	"time"
)

// Duration accepts Go duration strings and bare numbers in configuration files.
// A bare number is interpreted as seconds.
type Duration time.Duration

// Std returns the duration in its standard library representation.
func (d Duration) Std() time.Duration {
	return time.Duration(d)
}

// String implements fmt.Stringer.
func (d Duration) String() string {
	return time.Duration(d).String()
}

// MarshalJSON emits the duration as a string.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON accepts a JSON string or number.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		parsed, err := ParseDuration(text)
		if err != nil {
			return err
		}
		*d = Duration(parsed)
		return nil
	}

	var seconds float64
	if err := json.Unmarshal(data, &seconds); err != nil {
		return fmt.Errorf("duration must be a string or number: %w", err)
	}
	*d = Duration(time.Duration(seconds * float64(time.Second)))
	return nil
}

// MarshalYAML emits the duration as a string.
func (d Duration) MarshalYAML() (interface{}, error) {
	return time.Duration(d).String(), nil
}

// UnmarshalYAML accepts a YAML string or number.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return errors.New("duration must be a scalar")
	}
	parsed, err := ParseDuration(value.Value)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// ParseDuration parses standard Go durations, day/week suffixes, and bare
// second counts. Examples: 250ms, 30s, 5m, 7d, 2w, 15.
func ParseDuration(input string) (time.Duration, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return 0, errors.New("duration is empty")
	}

	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		if seconds < 0 {
			return 0, errors.New("duration must not be negative")
		}
		return time.Duration(seconds * float64(time.Second)), nil
	}

	multiplier := time.Duration(0)
	switch {
	case strings.HasSuffix(value, "d"):
		multiplier = 24 * time.Hour
		value = strings.TrimSuffix(value, "d")
	case strings.HasSuffix(value, "w"):
		multiplier = 7 * 24 * time.Hour
		value = strings.TrimSuffix(value, "w")
	}

	if multiplier > 0 {
		count, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, fmt.Errorf("parse duration %q: %w", input, err)
		}
		if count < 0 {
			return 0, errors.New("duration must not be negative")
		}
		return time.Duration(count * float64(multiplier)), nil
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse duration %q: %w", input, err)
	}
	if parsed < 0 {
		return 0, errors.New("duration must not be negative")
	}
	return parsed, nil
}

// TLSConfig describes TLS setup for Redis connections.
type TLSConfig struct {
	Enabled            bool   `yaml:"enabled" json:"enabled"`
	CAFile             string `yaml:"ca_file" json:"ca_file"`
	CertFile           string `yaml:"cert_file" json:"cert_file"`
	KeyFile            string `yaml:"key_file" json:"key_file"`
	ServerName         string `yaml:"server_name" json:"server_name"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify" json:"insecure_skip_verify"`
}

// SentinelConfig describes a Sentinel-managed Redis deployment.
type SentinelConfig struct {
	MasterName       string   `yaml:"master_name" json:"master_name"`
	Addresses        []string `yaml:"addresses" json:"addresses"`
	Username         string   `yaml:"username" json:"username"`
	Password         string   `yaml:"password" json:"password"`
	SentinelUsername string   `yaml:"sentinel_username" json:"sentinel_username"`
	SentinelPassword string   `yaml:"sentinel_password" json:"sentinel_password"`
	DB               int      `yaml:"db" json:"db"`
}

// ClusterConfig describes a Redis Cluster deployment.
type ClusterConfig struct {
	Addresses      []string `yaml:"addresses" json:"addresses"`
	MaxRedirects   int      `yaml:"max_redirects" json:"max_redirects"`
	RouteByLatency bool     `yaml:"route_by_latency" json:"route_by_latency"`
	RouteRandomly  bool     `yaml:"route_randomly" json:"route_randomly"`
	ReadOnly       bool     `yaml:"read_only" json:"read_only"`
}

// Config is the complete connection configuration for Cardinal.
type Config struct {
	Mode       string         `yaml:"mode" json:"mode"`
	Address    string         `yaml:"address" json:"address"`
	UnixSocket string         `yaml:"unix_socket" json:"unix_socket"`
	DB         int            `yaml:"db" json:"db"`
	Username   string         `yaml:"username" json:"username"`
	Password   string         `yaml:"password" json:"password"`
	Timeout    Duration       `yaml:"timeout" json:"timeout"`
	TLS        TLSConfig      `yaml:"tls" json:"tls"`
	Sentinel   SentinelConfig `yaml:"sentinel" json:"sentinel"`
	Cluster    ClusterConfig  `yaml:"cluster" json:"cluster"`
}

// DefaultConfig returns a standalone localhost configuration.
func DefaultConfig() Config {
	return Config{
		Mode:    "standalone",
		Address: "127.0.0.1:6379",
		Timeout: Duration(10 * time.Second),
	}
}

// Validate checks mode-specific settings and authentication consistency.
func (c Config) Validate() error {
	c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
	if c.Mode == "" {
		c.Mode = "standalone"
	}
	if c.Timeout < 0 {
		return errors.New("timeout must not be negative")
	}
	if c.TLS.Enabled && (c.TLS.CertFile == "") != (c.TLS.KeyFile == "") {
		return errors.New("tls.cert_file and tls.key_file must be specified together")
	}

	switch c.Mode {
	case "standalone":
		if c.UnixSocket == "" && strings.TrimSpace(c.Address) == "" {
			return errors.New("standalone mode requires address or unix_socket")
		}
	case "sentinel":
		if strings.TrimSpace(c.Sentinel.MasterName) == "" {
			return errors.New("sentinel mode requires sentinel.master_name")
		}
		if len(c.Sentinel.Addresses) == 0 {
			return errors.New("sentinel mode requires at least one sentinel address")
		}
		if c.UnixSocket != "" {
			return errors.New("unix_socket is only supported in standalone mode")
		}
	case "cluster":
		if len(c.Cluster.Addresses) == 0 {
			return errors.New("cluster mode requires at least one cluster address")
		}
		if c.UnixSocket != "" {
			return errors.New("unix_socket is only supported in standalone mode")
		}
	default:
		return fmt.Errorf("unsupported mode %q", c.Mode)
	}
	return nil
}
