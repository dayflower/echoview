package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/discover"
	dumpcmd "github.com/dayflower/echoview/internal/dump"
	"github.com/dayflower/echoview/internal/echonet"
)

func runDumpWithDependencies(args []string, stdout, stderr io.Writer, dependencies cliDependencies) int {
	for _, argument := range args {
		if argument == "--help" || argument == "-h" {
			printDumpUsage(stdout)
			return 0
		}
	}
	flags := flag.NewFlagSet("dump", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { printDumpUsage(stderr) }
	var interfaceAddress, outputFormat string
	var catalogDirs catalogDirectories
	var targets dumpTargets
	var assignments profileAssignments
	var discoveryTimeout, dataTimeout, instanceDelay time.Duration
	var discoveryAttempts, dataAttempts int
	var batchSize optionalInt
	var quiet, debug, showRaw bool
	flags.StringVar(&interfaceAddress, "interface", "", "local IPv4 address used for sending and socket binding")
	flags.Var(&catalogDirs, "catalog-dir", "catalog directory; repeatable")
	flags.Var(&targets, "target", "IPv4 address or IPv4/0xGGCCII object target; repeatable")
	flags.DurationVar(&discoveryTimeout, "discovery-timeout", 20*time.Second, "listen duration after each discovery request")
	flags.IntVar(&discoveryAttempts, "discovery-attempts", 1, "total multicast discovery attempts")
	flags.DurationVar(&dataTimeout, "data-timeout", 20*time.Second, "timeout for one property GET")
	flags.IntVar(&dataAttempts, "data-attempts", 1, "total property GET attempts")
	flags.Var(&batchSize, "batch-size", "maximum EPCs in a batch GET; a negative value requests all compatible EPCs together")
	flags.DurationVar(&instanceDelay, "instance-delay", 0, "delay before reading another instance at the same address")
	flags.Var(&assignments, "instance-profile", "ADDRESS/0xGGCCII=PROFILE_ID; repeatable")
	flags.StringVar(&outputFormat, "format", "text", "text, json, or metrics-config")
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
	if len(targets) == 0 {
		fmt.Fprintln(stderr, "error: at least one --target is required")
		return 2
	}
	if discoveryTimeout <= 0 || dataTimeout <= 0 || instanceDelay < 0 {
		fmt.Fprintln(stderr, "error: timeouts must be greater than zero and instance delay must not be negative")
		return 2
	}
	if discoveryAttempts <= 0 || dataAttempts <= 0 || (batchSize.set && batchSize.value == 0) {
		fmt.Fprintln(stderr, "error: attempt counts must be greater than zero and batch size must not be zero")
		return 2
	}
	if outputFormat != "text" && outputFormat != "json" && outputFormat != "metrics-config" {
		fmt.Fprintln(stderr, "error: --format must be text, json, or metrics-config")
		return 2
	}
	logger := newStderrLoggerAt(stderr, quiet, debug, dependencies.now)
	set, err := loadCatalogSet(catalogDirs, logger)
	if err != nil {
		fmt.Fprintf(stderr, "error: invalid catalog: %v\n", err)
		return 3
	}
	for _, assignment := range assignments {
		selected, ok := set.Profiles.Get(assignment.ProfileID)
		if !ok {
			fmt.Fprintf(stderr, "error: unknown profile %q\n", assignment.ProfileID)
			return 2
		}
		if !selected.ClassMatches(assignment.EOJ) {
			fmt.Fprintf(stderr, "error: profile %q class does not match %s\n", assignment.ProfileID, assignment.EOJ)
			return 2
		}
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
	config := dumpcmd.Config{DiscoveryTimeout: discoveryTimeout, DiscoveryAttempts: discoveryAttempts, DataTimeout: dataTimeout, DataAttempts: dataAttempts, BatchSize: batchSize.pointer(), InstanceDelay: instanceDelay, InitialTID: uint16(dependencies.now().UnixNano()), Targets: []dumpcmd.Target(targets), Assignments: []dumpcmd.Assignment(assignments), Profiles: set.Profiles, Catalog: set.Catalog, Locale: locale, Waitf: logger.Waitf, Warnf: logger.Warnf}
	if debug {
		config.Debugf = logger.Debugf
	}
	results, err := dumpcmd.Run(context.Background(), conn, config)
	if err != nil {
		fmt.Fprintf(stderr, "error: ECHONET Lite dump failed: %v\n", err)
		return 4
	}
	switch outputFormat {
	case "json":
		if err := json.NewEncoder(stdout).Encode(results); err != nil {
			fmt.Fprintf(stderr, "error: write JSON: %v\n", err)
			return 4
		}
	case "metrics-config":
		if err := writeMetricsConfig(stdout, metricsConfig(results, set.Profiles)); err != nil {
			fmt.Fprintf(stderr, "error: write metrics configuration: %v\n", err)
			return 4
		}
	default:
		printDumpResults(stdout, localAddress, results, showRaw, locale)
	}
	return 0
}

type dumpTargets []dumpcmd.Target

func (value *dumpTargets) String() string {
	items := make([]string, len(*value))
	for i, target := range *value {
		items[i] = target.Address.String()
		if target.EOJ != nil {
			items[i] += "/" + target.EOJ.String()
		}
	}
	return strings.Join(items, ",")
}
func (value *dumpTargets) Set(input string) error {
	parts := strings.Split(input, "/")
	if len(parts) > 2 || parts[0] == "" {
		return fmt.Errorf("--target must be an IPv4 address or IPv4/0xGGCCII: %q", input)
	}
	address, err := netip.ParseAddr(parts[0])
	if err != nil || !address.Is4() {
		return fmt.Errorf("--target must be an IPv4 address or IPv4/0xGGCCII: %q", input)
	}
	target := dumpcmd.Target{Address: address.Unmap()}
	if len(parts) == 2 {
		eoj, err := echonet.ParseEOJ(parts[1])
		if err != nil {
			return fmt.Errorf("--target must be an IPv4 address or IPv4/0xGGCCII: %q", input)
		}
		target.EOJ = &eoj
	}
	*value = append(*value, target)
	return nil
}

type profileAssignments []dumpcmd.Assignment

func (value *profileAssignments) String() string {
	items := make([]string, len(*value))
	for i, assignment := range *value {
		items[i] = assignment.Address.String() + "/" + assignment.EOJ.String() + "=" + assignment.ProfileID
	}
	return strings.Join(items, ",")
}
func (value *profileAssignments) Set(input string) error {
	parts := strings.SplitN(input, "=", 2)
	if len(parts) != 2 || parts[1] == "" {
		return fmt.Errorf("--instance-profile must be ADDRESS/0xGGCCII=PROFILE_ID")
	}
	target := strings.Split(parts[0], "/")
	if len(target) != 2 {
		return fmt.Errorf("--instance-profile must be ADDRESS/0xGGCCII=PROFILE_ID")
	}
	address, err := netip.ParseAddr(target[0])
	if err != nil || !address.Is4() {
		return fmt.Errorf("--instance-profile must be ADDRESS/0xGGCCII=PROFILE_ID")
	}
	eoj, err := echonet.ParseEOJ(target[1])
	if err != nil {
		return fmt.Errorf("--instance-profile must be ADDRESS/0xGGCCII=PROFILE_ID")
	}
	*value = append(*value, dumpcmd.Assignment{Address: address.Unmap(), EOJ: eoj, ProfileID: parts[1]})
	return nil
}

func printDumpUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: echoview dump --target ADDRESS[/0xGGCCII] [options]")
	fmt.Fprintln(output, "\nOptions:")
	fmt.Fprintln(output, "  --interface ADDRESS")
	fmt.Fprintln(output, "  --catalog-dir DIRECTORY (repeatable; replaces default catalog directories)")
	fmt.Fprintln(output, "  --target ADDRESS[/EOJ] (repeatable, required)")
	fmt.Fprintln(output, "  --discovery-timeout DURATION (default 20s)")
	fmt.Fprintln(output, "  --discovery-attempts COUNT (default 1)")
	fmt.Fprintln(output, "  --data-timeout DURATION (default 20s)")
	fmt.Fprintln(output, "  --data-attempts COUNT (default 1)")
	fmt.Fprintln(output, "  --batch-size COUNT (negative means one batch per instance; 0 is invalid)")
	fmt.Fprintln(output, "  --instance-delay DURATION")
	fmt.Fprintln(output, "  --instance-profile ADDRESS/EOJ=PROFILE_ID (repeatable)")
	fmt.Fprintln(output, "  --format text|json|metrics-config (default text)")
	fmt.Fprintln(output, "  --show-raw (append raw EDT to text values)")
	fmt.Fprintln(output, "  --quiet (suppress non-fatal waiting, info, and warning logs)")
	fmt.Fprintln(output, "  --debug")
}
