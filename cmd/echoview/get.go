package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/discover"
	getcmd "github.com/dayflower/echoview/internal/get"
	"github.com/dayflower/echoview/internal/metricsconfig"
)

func runGetWithDependencies(args []string, stdout, stderr io.Writer, dependencies cliDependencies) int {
	for _, argument := range args {
		if argument == "--help" || argument == "-h" {
			printGetUsage(stdout)
			return 0
		}
	}
	flags := flag.NewFlagSet("get", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { printGetUsage(stderr) }
	var interfaceAddress, configPath, outputFormat string
	var catalogDirs catalogDirectories
	var dataTimeout time.Duration
	var dataAttempts int
	var quiet, debug, showRaw bool
	flags.StringVar(&interfaceAddress, "interface", "", "local IPv4 address used for sending and socket binding")
	flags.StringVar(&configPath, "config", "", "format-2 metrics instance configuration path")
	flags.Var(&catalogDirs, "catalog-dir", "catalog directory; repeatable")
	flags.DurationVar(&dataTimeout, "data-timeout", 20*time.Second, "timeout for one property GET")
	flags.IntVar(&dataAttempts, "data-attempts", 1, "total property GET attempts")
	flags.StringVar(&outputFormat, "format", "text", "text or json")
	flags.BoolVar(&showRaw, "show-raw", false, "append raw EDT to text property values")
	flags.BoolVar(&quiet, "quiet", false, "suppress non-fatal waiting, info, and warning logs")
	flags.BoolVar(&debug, "debug", false, "write detailed diagnostics to standard error")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "error: unexpected argument %q\n", flags.Arg(0))
		return 2
	}
	if configPath == "" {
		fmt.Fprintln(stderr, "error: --config is required")
		return 2
	}
	if dataTimeout <= 0 || dataAttempts <= 0 {
		fmt.Fprintln(stderr, "error: data timeout and attempt count must be greater than zero")
		return 2
	}
	if outputFormat != "text" && outputFormat != "json" {
		fmt.Fprintln(stderr, "error: --format must be text or json")
		return 2
	}
	logger := newStderrLoggerAt(stderr, quiet, debug, dependencies.now)
	set, err := loadCatalogSet(catalogDirs, logger)
	if err != nil {
		fmt.Fprintf(stderr, "error: invalid catalog: %v\n", err)
		return 3
	}
	loadedConfig, err := metricsconfig.Load(configPath, set.Profiles)
	if err != nil {
		fmt.Fprintf(stderr, "error: invalid metrics configuration: %v\n", err)
		return 3
	}
	localAddress, err := dependencies.localIPv4(interfaceAddress)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	conn, err := dependencies.listenUDP("udp4", &net.UDPAddr{IP: net.IP(localAddress.AsSlice()), Port: discover.Port})
	if err != nil {
		fmt.Fprintf(stderr, "error: open ECHONET Lite socket on %s:%d: %v\n", localAddress, discover.Port, err)
		return 4
	}
	defer conn.Close()
	locale := catalog.LocaleFromEnvironment()
	config := getcmd.Config{DataTimeout: dataTimeout, DataAttempts: dataAttempts, InitialTID: uint16(dependencies.now().UnixNano()), Instances: loadedConfig.Instances, Profiles: set.Profiles, Catalog: set.Catalog, Locale: locale, Waitf: logger.Waitf}
	if debug {
		config.Debugf = logger.Debugf
	}
	results, err := getcmd.Run(context.Background(), conn, config)
	if err != nil {
		fmt.Fprintf(stderr, "error: ECHONET Lite get failed: %v\n", err)
		return 4
	}
	if outputFormat == "json" {
		if err := json.NewEncoder(stdout).Encode(results); err != nil {
			fmt.Fprintf(stderr, "error: write JSON: %v\n", err)
			return 4
		}
	} else {
		printDumpResults(stdout, localAddress, results, showRaw, locale)
	}
	return 0
}

func printGetUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: echoview get --config PATH [options]")
	fmt.Fprintln(output, "\nOptions:")
	fmt.Fprintln(output, "  --interface ADDRESS")
	fmt.Fprintln(output, "  --config PATH (required)")
	fmt.Fprintln(output, "  --catalog-dir DIRECTORY (repeatable; replaces default catalog directories)")
	fmt.Fprintln(output, "  --data-timeout DURATION (default 20s)")
	fmt.Fprintln(output, "  --data-attempts COUNT (default 1)")
	fmt.Fprintln(output, "  --format text|json (default text)")
	fmt.Fprintln(output, "  --show-raw (append raw EDT to text values)")
	fmt.Fprintln(output, "  --quiet (suppress non-fatal waiting, info, and warning logs)")
	fmt.Fprintln(output, "  --debug")
}
