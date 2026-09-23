package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	prometheuscmd "github.com/dayflower/echoview/internal/prometheus"
)

func runExporterWithDependencies(args []string, stdout, stderr io.Writer, dependencies cliDependencies) int {
	for _, argument := range args {
		if argument == "--help" || argument == "-h" {
			printExporterUsage(stdout)
			return 0
		}
	}
	flags := flag.NewFlagSet("exporter", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { printExporterUsage(stderr) }
	options := periodicOptions{dataTimeout: 20 * time.Second, dataAttempts: 1}
	var listenAddress string
	var listenPort int
	flags.StringVar(&options.interfaceAddress, "interface", "", "local IPv4 address used for ECHONET Lite traffic")
	flags.StringVar(&options.configPath, "config", "", "format-2 metrics instance configuration path")
	flags.Var(&options.catalogDirs, "catalog-dir", "catalog directory; repeatable")
	flags.DurationVar(&options.dataTimeout, "data-timeout", options.dataTimeout, "timeout for one property GET")
	flags.IntVar(&options.dataAttempts, "data-attempts", options.dataAttempts, "total property GET attempts")
	flags.BoolVar(&options.keepBinding, "keep-binding", false, "keep UDP port 3610 bound between collections")
	flags.StringVar(&listenAddress, "listen-address", "127.0.0.1", "HTTP listen address")
	flags.IntVar(&listenPort, "listen-port", 13610, "HTTP listen port")
	flags.BoolVar(&options.quiet, "quiet", false, "suppress non-fatal waiting, info, and warning logs")
	flags.BoolVar(&options.debug, "debug", false, "write detailed diagnostics to standard error")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "error: unexpected argument %q\n", flags.Arg(0))
		return 2
	}
	if options.configPath == "" {
		fmt.Fprintln(stderr, "error: --config is required")
		return 2
	}
	if !options.validDataCollection() {
		fmt.Fprintln(stderr, "error: data timeout and attempt count must be greater than zero")
		return 2
	}
	if listenAddress == "" || listenPort < 1 || listenPort > 65535 {
		fmt.Fprintln(stderr, "error: listen address must be non-empty and listen port must be between 1 and 65535")
		return 2
	}
	logger := newStderrLoggerAt(stderr, options.quiet, options.debug, dependencies.now)
	configuration, err := loadPeriodicConfiguration(options, logger)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 3
	}
	localAddress, err := dependencies.localIPv4(options.interfaceAddress)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	runtime, err := newPeriodicRuntime(localAddress, options, configuration, stderr, dependencies)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 4
	}
	defer runtime.Close()
	runner, err := runtime.NewScheduler()
	if err != nil {
		fmt.Fprintf(stderr, "error: initialize collection scheduler: %v\n", err)
		return 4
	}
	exporter, err := prometheuscmd.New(prometheuscmd.Config{Snapshots: runner.Store(), Instances: configuration.metrics.Instances, Profiles: configuration.profiles, Catalog: configuration.catalog})
	if err != nil {
		fmt.Fprintf(stderr, "error: initialize Prometheus exporter: %v\n", err)
		return 4
	}
	listener, err := dependencies.listenTCP("tcp", net.JoinHostPort(listenAddress, strconv.Itoa(listenPort)))
	if err != nil {
		fmt.Fprintf(stderr, "error: listen on %s: %v\n", net.JoinHostPort(listenAddress, strconv.Itoa(listenPort)), err)
		return 4
	}
	defer listener.Close()

	signalContext, stop := dependencies.signalContext(context.Background())
	defer stop()
	ctx, cancel := context.WithCancel(signalContext)
	defer cancel()
	stopConnectionWatcher := runtime.CloseOnCancel(ctx)
	defer stopConnectionWatcher()
	server := &http.Server{Handler: exporter, ReadHeaderTimeout: 5 * time.Second}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(ctx) }()

	select {
	case err := <-serveDone:
		cancel()
		<-runDone
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "error: Prometheus HTTP server failed: %v\n", err)
			return 4
		}
	case err := <-runDone:
		if err != nil {
			cancel()
			_ = server.Close()
			<-serveDone
			fmt.Fprintf(stderr, "error: collection scheduler failed: %v\n", err)
			return 4
		}
	case <-signalContext.Done():
		cancel()
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = server.Shutdown(shutdownContext)
		shutdownCancel()
		<-serveDone
		<-runDone
	}
	return 0
}

func printExporterUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: echoview exporter --config PATH [options]")
	fmt.Fprintln(output, "\nThe exporter listens on 127.0.0.1:13610 by default and serves /metrics and /healthz.")
	fmt.Fprintln(output, "\nOptions:")
	fmt.Fprintln(output, "  --interface ADDRESS")
	fmt.Fprintln(output, "  --config PATH (required)")
	fmt.Fprintln(output, "  --catalog-dir DIRECTORY (repeatable; replaces default catalog directories)")
	fmt.Fprintln(output, "  --data-timeout DURATION (default 20s)")
	fmt.Fprintln(output, "  --data-attempts COUNT (default 1)")
	fmt.Fprintln(output, "  --keep-binding (keep UDP port 3610 bound between collections)")
	fmt.Fprintln(output, "  --listen-address ADDRESS (default 127.0.0.1)")
	fmt.Fprintln(output, "  --listen-port PORT (default 13610)")
	fmt.Fprintln(output, "  --quiet (suppress non-fatal waiting, info, and warning logs)")
	fmt.Fprintln(output, "  --debug")
}
