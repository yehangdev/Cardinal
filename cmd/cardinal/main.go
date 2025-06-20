package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"cardinal/internal/cardinal"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

type globalOptions struct {
	configPath string
	format     string
	mode       string
	addr       string
	unixSocket string
	addresses  string
	db         int
	username   string
	password   string
	timeout    string

	tlsEnabled            bool
	tlsCAFile             string
	tlsCertFile           string
	tlsKeyFile            string
	tlsServerName         string
	tlsInsecureSkipVerify bool

	masterName       string
	sentinelAddrs    string
	sentinelUsername string
	sentinelPassword string

	clusterAddrs   string
	maxRedirects   int
	routeByLatency bool
	routeRandomly  bool
	readOnly       bool
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}

func execute(args []string, stdout, stderr io.Writer) int {
	options := globalOptions{}
	flags := newGlobalFlagSet(&options, stderr)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(stderr)
			return exitOK
		}
		fmt.Fprintf(stderr, "error: %v\n", err)
		usage(stderr)
		return exitUsage
	}
	if flags.NArg() == 0 {
		usage(stderr)
		return exitUsage
	}

	format, err := cardinal.ParseOutputFormat(options.format)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitUsage
	}

	config, err := resolveConfig(options, flags)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitUsage
	}

	commandArgs := flags.Args()
	if commandArgs[0] == "help" {
		usage(stdout)
		return exitOK
	}
	if commandArgs[0] == "config" {
		if err := runConfigCommand(config, commandArgs[1:], format, stdout); err != nil {
			return reportError(stderr, format, "config", err)
		}
		return exitOK
	}

	client, err := cardinal.NewConfiguredClient(config)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitFailure
	}
	defer client.Close()

	timeout := config.Timeout.Std()
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := runCommand(ctx, client, config, format, commandArgs, stdout); err != nil {
		return reportError(stderr, format, commandArgs[0], err)
	}
	return exitOK
}

func newGlobalFlagSet(options *globalOptions, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("cardinal", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.configPath, "config", "", "YAML or JSON configuration file")
	flags.StringVar(&options.format, "format", "text", "output format: text, json, or prometheus")
	flags.StringVar(&options.mode, "mode", "standalone", "connection mode: standalone, sentinel, or cluster")
	flags.StringVar(&options.addr, "addr", "127.0.0.1:6379", "standalone Redis address")
	flags.StringVar(&options.unixSocket, "unix-socket", "", "standalone Redis Unix socket path")
	flags.StringVar(&options.addresses, "addresses", "", "comma-separated addresses for the selected mode")
	flags.IntVar(&options.db, "db", 0, "Redis database number")
	flags.StringVar(&options.username, "username", "", "ACL username")
	flags.StringVar(&options.password, "password", "", "ACL password")
	flags.StringVar(&options.timeout, "timeout", "10s", "command timeout")

	flags.BoolVar(&options.tlsEnabled, "tls", false, "enable TLS")
	flags.StringVar(&options.tlsCAFile, "tls-ca", "", "TLS CA bundle")
	flags.StringVar(&options.tlsCertFile, "tls-cert", "", "TLS client certificate")
	flags.StringVar(&options.tlsKeyFile, "tls-key", "", "TLS client key")
	flags.StringVar(&options.tlsServerName, "tls-server-name", "", "TLS server name override")
	flags.BoolVar(&options.tlsInsecureSkipVerify, "tls-insecure-skip-verify", false, "skip TLS certificate verification")

	flags.StringVar(&options.masterName, "master-name", "", "Sentinel master name")
	flags.StringVar(&options.sentinelAddrs, "sentinel-addresses", "", "comma-separated Sentinel addresses")
	flags.StringVar(&options.sentinelUsername, "sentinel-username", "", "Sentinel ACL username")
	flags.StringVar(&options.sentinelPassword, "sentinel-password", "", "Sentinel ACL password")

	flags.StringVar(&options.clusterAddrs, "cluster-addresses", "", "comma-separated Cluster seed addresses")
	flags.IntVar(&options.maxRedirects, "max-redirects", 0, "maximum Cluster redirects")
	flags.BoolVar(&options.routeByLatency, "route-by-latency", false, "route Cluster reads by latency")
	flags.BoolVar(&options.routeRandomly, "route-randomly", false, "route Cluster reads randomly")
	flags.BoolVar(&options.readOnly, "read-only", false, "allow Cluster reads from replicas")
	flags.Usage = func() {
		usage(stderr)
	}
	return flags
}

func resolveConfig(options globalOptions, flags *flag.FlagSet) (cardinal.Config, error) {
	visited := make(map[string]bool)
	flags.Visit(func(item *flag.Flag) {
		visited[item.Name] = true
	})

	config := cardinal.DefaultConfig()
	var err error
	if options.configPath != "" {
		config, err = cardinal.LoadConfig(options.configPath)
		if err != nil {
			return cardinal.Config{}, err
		}
	}

	if visited["mode"] {
		config.Mode = options.mode
	}
	if visited["addr"] {
		config.Address = options.addr
	}
	if visited["unix-socket"] {
		config.UnixSocket = options.unixSocket
	}
	if visited["db"] {
		config.DB = options.db
	}
	if visited["username"] {
		config.Username = options.username
	}
	if visited["password"] {
		config.Password = options.password
	}
	if visited["timeout"] {
		timeout, err := cardinal.ParseDuration(options.timeout)
		if err != nil {
			return cardinal.Config{}, err
		}
		config.Timeout = cardinal.Duration(timeout)
	}

	if visited["tls"] {
		config.TLS.Enabled = options.tlsEnabled
	}
	if visited["tls-ca"] {
		config.TLS.CAFile = options.tlsCAFile
	}
	if visited["tls-cert"] {
		config.TLS.CertFile = options.tlsCertFile
	}
	if visited["tls-key"] {
		config.TLS.KeyFile = options.tlsKeyFile
	}
	if visited["tls-server-name"] {
		config.TLS.ServerName = options.tlsServerName
	}
	if visited["tls-insecure-skip-verify"] {
		config.TLS.InsecureSkipVerify = options.tlsInsecureSkipVerify
	}

	if visited["master-name"] {
		config.Sentinel.MasterName = options.masterName
	}
	if visited["sentinel-username"] {
		config.Sentinel.SentinelUsername = options.sentinelUsername
	}
	if visited["sentinel-password"] {
		config.Sentinel.SentinelPassword = options.sentinelPassword
	}
	if visited["sentinel-addresses"] {
		config.Sentinel.Addresses = parseAddressList(options.sentinelAddrs)
	}
	if visited["cluster-addresses"] {
		config.Cluster.Addresses = parseAddressList(options.clusterAddrs)
	}
	if visited["max-redirects"] {
		config.Cluster.MaxRedirects = options.maxRedirects
	}
	if visited["route-by-latency"] {
		config.Cluster.RouteByLatency = options.routeByLatency
	}
	if visited["route-randomly"] {
		config.Cluster.RouteRandomly = options.routeRandomly
	}
	if visited["read-only"] {
		config.Cluster.ReadOnly = options.readOnly
	}

	if visited["addresses"] {
		addresses := parseAddressList(options.addresses)
		switch strings.ToLower(config.Mode) {
		case "sentinel":
			config.Sentinel.Addresses = addresses
		case "cluster":
			config.Cluster.Addresses = addresses
		default:
			if len(addresses) > 0 {
				config.Address = addresses[0]
			}
		}
	}

	config.Mode = strings.ToLower(strings.TrimSpace(config.Mode))
	if config.Mode == "" {
		config.Mode = "standalone"
	}
	if err := config.Validate(); err != nil {
		return cardinal.Config{}, err
	}
	return config, nil
}

func parseAddressList(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func reportError(stderr io.Writer, format cardinal.OutputFormat, command string, err error) int {
	if format == cardinal.FormatJSON {
		_ = cardinal.WriteJSON(stderr, map[string]interface{}{
			"ok":      false,
			"command": command,
			"error":   err.Error(),
		})
	} else {
		fmt.Fprintf(stderr, "error: %v\n", err)
	}
	if cardinal.IsUsageError(err) {
		return exitUsage
	}
	return exitFailure
}

func usage(writer io.Writer) {
	fmt.Fprint(writer, `usage: cardinal [global flags] <command> [command flags] [args]

commands:
  ping
  get KEY
  set KEY VALUE [TTL]
  ttl KEY
  scan [--count N] [--cursor N] [--max-iterations N] [--limit N] PREFIX
  latency [--count N] [--pipeline] [--batch-size N] COMMAND [ARGS...]
  pipeline [--value V] [--ttl D] [--repeat N] OP KEY [KEY...]
  cache-aside [--ttl D] [--origin-delay D] [--value V] KEY
  config show
  config validate
  help

global flags:
  --config PATH
  --format text|json|prometheus
  --mode standalone|sentinel|cluster
  --addr HOST:PORT
  --unix-socket PATH
  --addresses HOST:PORT[,HOST:PORT...]
  --db N
  --username USER --password PASS
  --timeout DURATION
  --tls --tls-ca PATH --tls-cert PATH --tls-key PATH
  --tls-server-name NAME --tls-insecure-skip-verify
  --master-name NAME --sentinel-addresses LIST
  --sentinel-username USER --sentinel-password PASS
  --cluster-addresses LIST --max-redirects N
  --route-by-latency --route-randomly --read-only
`)
}

func durationFlagError(name string, err error) error {
	return cardinal.NewUsageError("invalid --%s: %v", name, err)
}
