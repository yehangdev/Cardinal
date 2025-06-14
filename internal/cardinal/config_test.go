package cardinal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{input: "15", want: 15 * time.Second},
		{input: "1.5", want: 1500 * time.Millisecond},
		{input: "250ms", want: 250 * time.Millisecond},
		{input: "2d", want: 48 * time.Hour},
		{input: "1w", want: 168 * time.Hour},
	}
	for _, test := range tests {
		got, err := ParseDuration(test.input)
		if err != nil {
			t.Fatalf("ParseDuration(%q): %v", test.input, err)
		}
		if got != test.want {
			t.Errorf("ParseDuration(%q) = %s, want %s", test.input, got, test.want)
		}
	}

	for _, input := range []string{"", "-1s", "tomorrow"} {
		if _, err := ParseDuration(input); err == nil {
			t.Errorf("ParseDuration(%q) returned no error", input)
		}
	}
}

func TestLoadConfigYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache-probe.yaml")
	content := `
mode: sentinel
timeout: 2.5s
tls:
  enabled: true
  server_name: redis.internal
sentinel:
  master_name: primary
  addresses:
    - 127.0.0.1:26379
    - 127.0.0.1:26380
  username: app
  password: secret
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	config, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if config.Mode != "sentinel" || config.Timeout.Std() != 2500*time.Millisecond {
		t.Fatalf("unexpected config: %+v", config)
	}
	if config.Sentinel.MasterName != "primary" || len(config.Sentinel.Addresses) != 2 {
		t.Fatalf("unexpected sentinel config: %+v", config.Sentinel)
	}
	if config.TLS.ServerName != "redis.internal" {
		t.Fatalf("TLS server name = %q", config.TLS.ServerName)
	}
}

func TestLoadConfigRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(path, []byte("mode: standalone\nunknown: true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("LoadConfig returned no error")
	}
}

func TestConfigValidateModes(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		ok     bool
	}{
		{
			name:   "standalone",
			config: DefaultConfig(),
			ok:     true,
		},
		{
			name: "unix",
			config: Config{
				Mode:       "standalone",
				UnixSocket: "/tmp/redis.sock",
			},
			ok: true,
		},
		{
			name: "sentinel",
			config: Config{
				Mode: "sentinel",
				Sentinel: SentinelConfig{
					MasterName: "primary",
					Addresses:  []string{"127.0.0.1:26379"},
				},
			},
			ok: true,
		},
		{
			name: "cluster",
			config: Config{
				Mode:    "cluster",
				Cluster: ClusterConfig{Addresses: []string{"127.0.0.1:7000"}},
			},
			ok: true,
		},
		{name: "missing address", config: Config{Mode: "standalone"}},
		{name: "missing sentinel master", config: Config{Mode: "sentinel"}},
		{name: "bad mode", config: Config{Mode: "ring", Address: "localhost:6379"}},
		{
			name: "partial certificate",
			config: Config{
				Mode:    "standalone",
				Address: "localhost:6379",
				TLS:     TLSConfig{Enabled: true, CertFile: "client.pem"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.config.Validate()
			if test.ok && err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if !test.ok && err == nil {
				t.Fatal("Validate returned no error")
			}
		})
	}
}

func TestBuildTLSConfigDisabled(t *testing.T) {
	config, err := BuildTLSConfig(TLSConfig{})
	if err != nil {
		t.Fatalf("BuildTLSConfig: %v", err)
	}
	if config != nil {
		t.Fatalf("BuildTLSConfig = %#v, want nil", config)
	}
}

func TestRedactedConfig(t *testing.T) {
	config := DefaultConfig()
	config.Password = "top-secret"
	config.Sentinel.Password = "sentinel-secret"
	config.Sentinel.SentinelPassword = "observer-secret"

	redacted := config.Redacted()
	if strings.Contains(redacted.Password, "secret") ||
		strings.Contains(redacted.Sentinel.Password, "secret") ||
		strings.Contains(redacted.Sentinel.SentinelPassword, "secret") {
		t.Fatalf("redaction failed: %+v", redacted)
	}
}
