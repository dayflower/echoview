package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/model"
	"github.com/dayflower/echoview/internal/scheduler"
)

func runWatchWithDependencies(args []string, stdout, stderr io.Writer, dependencies cliDependencies) int {
	for _, argument := range args {
		if argument == "--help" || argument == "-h" {
			printWatchUsage(stdout)
			return 0
		}
	}
	flags := flag.NewFlagSet("watch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { printWatchUsage(stderr) }
	options := periodicOptions{dataTimeout: 20 * time.Second, dataAttempts: 1}
	var outputFormat string
	var showRaw bool
	flags.StringVar(&options.interfaceAddress, "interface", "", "local IPv4 address used for sending and socket binding")
	flags.StringVar(&options.configPath, "config", "", "format-2 metrics instance configuration path")
	flags.Var(&options.catalogDirs, "catalog-dir", "catalog directory; repeatable")
	flags.DurationVar(&options.dataTimeout, "data-timeout", options.dataTimeout, "timeout for one property GET")
	flags.IntVar(&options.dataAttempts, "data-attempts", options.dataAttempts, "total property GET attempts")
	flags.BoolVar(&options.keepBinding, "keep-binding", false, "keep UDP port 3610 bound between collections")
	flags.StringVar(&outputFormat, "format", "text", "text or jsonl")
	flags.BoolVar(&showRaw, "show-raw", false, "append raw EDT to text property values")
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
	if outputFormat != "text" && outputFormat != "jsonl" {
		fmt.Fprintln(stderr, "error: --format must be text or jsonl")
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
	signalContext, stop := dependencies.signalContext(context.Background())
	defer stop()
	ctx, cancel := context.WithCancel(signalContext)
	defer cancel()
	stopConnectionWatcher := runtime.CloseOnCancel(ctx)
	defer stopConnectionWatcher()
	printer := &watchPrinter{output: stdout, format: outputFormat, showRaw: showRaw, locale: runtime.locale, cancel: cancel}
	runner, err := runtime.NewScheduler(printer)
	if err != nil {
		fmt.Fprintf(stderr, "error: initialize collection scheduler: %v\n", err)
		return 4
	}
	if err := runner.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "error: ECHONET Lite watch failed: %v\n", err)
		return 4
	}
	if err := printer.Err(); err != nil {
		fmt.Fprintf(stderr, "error: write watch output: %v\n", err)
		return 4
	}
	return 0
}

type watchPrinter struct {
	mu      sync.Mutex
	output  io.Writer
	format  string
	showRaw bool
	locale  catalog.Locale
	cancel  context.CancelFunc
	err     error
}

func (p *watchPrinter) ObserveCollection(event scheduler.CollectionEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return
	}
	if p.format == "jsonl" {
		p.err = json.NewEncoder(p.output).Encode(watchJSONEvent(event))
	} else {
		p.err = printWatchText(p.output, event, p.showRaw, p.locale)
	}
	if p.err != nil && p.cancel != nil {
		p.cancel()
	}
}

// Err returns the first output error encountered by the printer.
func (p *watchPrinter) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

type watchJSON struct {
	CollectedAt time.Time             `json:"collected_at"`
	Address     string                `json:"address"`
	EOJ         echonet.EOJ           `json:"eoj"`
	Succeeded   bool                  `json:"succeeded"`
	Error       string                `json:"error,omitempty"`
	Result      *model.InstanceResult `json:"result"`
}

func watchJSONEvent(event scheduler.CollectionEvent) watchJSON {
	item := event.Instance
	result := &item.Result
	if !item.Succeeded {
		result = nil
	}
	return watchJSON{CollectedAt: item.CollectedAt, Address: item.Address, EOJ: item.EOJ, Succeeded: item.Succeeded, Error: item.Error, Result: result}
}

func printWatchText(output io.Writer, event scheduler.CollectionEvent, showRaw bool, locale catalog.Locale) error {
	item := event.Instance
	if _, err := fmt.Fprintf(output, "%s %s / %s\n", item.CollectedAt.Format(time.RFC3339Nano), item.Address, item.EOJ); err != nil {
		return err
	}
	if !item.Succeeded {
		_, err := fmt.Fprintf(output, "  collection: failed (%s)\n\n", item.Error)
		return err
	}
	if err := printWatchInstance(output, item.Result, showRaw, locale); err != nil {
		return err
	}
	_, err := fmt.Fprintln(output)
	return err
}

func printWatchInstance(output io.Writer, instance model.InstanceResult, showRaw bool, locale catalog.Locale) error {
	if _, err := fmt.Fprintf(output, "  - %s\n", instance.EOJ); err != nil {
		return err
	}
	for _, property := range instance.GetProperties {
		name := property.EPC
		if localized := localizedText(locale, property.NameJA, property.NameEN, ""); localized != "" {
			name = localized
		}
		if property.RawEDT == nil || property.Value == nil {
			line := fmt.Sprintf("      %s (%s): unavailable (%s)", name, property.EPC, property.Status)
			if showRaw && property.RawEDT != nil {
				line += " (raw: " + *property.RawEDT + ")"
			}
			if _, err := fmt.Fprintln(output, line); err != nil {
				return err
			}
			continue
		}
		value := fmt.Sprint(property.Value)
		if showRaw {
			value += " (raw: " + *property.RawEDT + ")"
		}
		if property.Unit != nil {
			value += " " + *property.Unit
		}
		if property.Status != model.PropertyOK {
			value += " (" + string(property.Status) + ")"
		}
		if _, err := fmt.Fprintf(output, "      %s (%s): %s\n", name, property.EPC, value); err != nil {
			return err
		}
	}
	return nil
}

func printWatchUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: echoview watch --config PATH [options]")
	fmt.Fprintln(output, "\nOptions:")
	fmt.Fprintln(output, "  --interface ADDRESS")
	fmt.Fprintln(output, "  --config PATH (required)")
	fmt.Fprintln(output, "  --catalog-dir DIRECTORY (repeatable; replaces default catalog directories)")
	fmt.Fprintln(output, "  --data-timeout DURATION (default 20s)")
	fmt.Fprintln(output, "  --data-attempts COUNT (default 1)")
	fmt.Fprintln(output, "  --keep-binding (keep UDP port 3610 bound between collections)")
	fmt.Fprintln(output, "  --format text|jsonl (default text)")
	fmt.Fprintln(output, "  --show-raw (append raw EDT to text values)")
	fmt.Fprintln(output, "  --quiet (suppress non-fatal waiting, info, and warning logs)")
	fmt.Fprintln(output, "  --debug")
}
