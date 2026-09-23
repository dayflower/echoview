package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/discover"
)

func runDiscoverWithDependencies(args []string, stdout, stderr io.Writer, dependencies cliDependencies) int {
	for _, argument := range args {
		if argument == "--help" || argument == "-h" {
			printDiscoverUsage(stdout)
			return 0
		}
	}
	flags := flag.NewFlagSet("discover", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { printDiscoverUsage(stderr) }
	var interfaceAddress string
	var catalogDirs catalogDirectories
	var targets addresses
	var discoveryTimeout, dataTimeout time.Duration
	var discoveryAttempts, dataAttempts int
	var quiet, debug bool
	flags.StringVar(&interfaceAddress, "interface", "", "local IPv4 address used for sending and socket binding")
	flags.Var(&catalogDirs, "catalog-dir", "catalog directory; repeatable")
	flags.Var(&targets, "target", "direct IPv4 destination; repeatable")
	flags.DurationVar(&discoveryTimeout, "discovery-timeout", 20*time.Second, "listen duration after each discovery request")
	flags.IntVar(&discoveryAttempts, "discovery-attempts", 1, "total multicast discovery attempts")
	flags.DurationVar(&dataTimeout, "data-timeout", 20*time.Second, "timeout for one basic-property GET")
	flags.IntVar(&dataAttempts, "data-attempts", 1, "total basic-property GET attempts")
	flags.BoolVar(&quiet, "quiet", false, "suppress non-fatal waiting, info, and warning logs")
	flags.BoolVar(&debug, "debug", false, "write detailed diagnostics to standard error")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "error: unexpected argument %q\n", flags.Arg(0))
		return 2
	}
	if discoveryTimeout <= 0 || dataTimeout <= 0 {
		fmt.Fprintln(stderr, "error: timeouts must be greater than zero")
		return 2
	}
	if discoveryAttempts <= 0 || dataAttempts <= 0 {
		fmt.Fprintln(stderr, "error: attempt counts must be greater than zero")
		return 2
	}
	logger := newStderrLoggerAt(stderr, quiet, debug, dependencies.now)
	set, err := loadCatalogSet(catalogDirs, logger)
	if err != nil {
		fmt.Fprintf(stderr, "error: invalid catalog: %v\n", err)
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
	config := discover.Config{Targets: []netip.Addr(targets), DiscoveryTimeout: discoveryTimeout, DiscoveryAttempts: discoveryAttempts, DataTimeout: dataTimeout, DataAttempts: dataAttempts, InitialTID: uint16(dependencies.now().UnixNano()), Waitf: logger.Waitf}
	if debug {
		config.Debugf = logger.Debugf
	}
	nodes, err := discover.Run(context.Background(), conn, config)
	if err != nil {
		fmt.Fprintf(stderr, "error: ECHONET Lite discovery failed: %v\n", err)
		return 4
	}
	printNodes(stdout, localAddress, nodes, set.Catalog, catalog.LocaleFromEnvironment())
	return 0
}

type addresses []netip.Addr

func (value *addresses) String() string {
	items := make([]string, len(*value))
	for index, address := range *value {
		items[index] = address.String()
	}
	return strings.Join(items, ",")
}

func (value *addresses) Set(input string) error {
	address, err := netip.ParseAddr(input)
	if err != nil || !address.Is4() {
		return fmt.Errorf("--target must be an IPv4 address: %q", input)
	}
	*value = append(*value, address.Unmap())
	return nil
}

func printDiscoverUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: echoview discover [options]")
	fmt.Fprintln(output, "\nOptions:")
	fmt.Fprintln(output, "  --interface ADDRESS")
	fmt.Fprintln(output, "  --catalog-dir DIRECTORY (repeatable; replaces default catalog directories)")
	fmt.Fprintln(output, "  --target ADDRESS (repeatable)")
	fmt.Fprintln(output, "  --discovery-timeout DURATION (default 20s)")
	fmt.Fprintln(output, "  --discovery-attempts COUNT (default 1)")
	fmt.Fprintln(output, "  --data-timeout DURATION (default 20s)")
	fmt.Fprintln(output, "  --data-attempts COUNT (default 1)")
	fmt.Fprintln(output, "  --quiet (suppress non-fatal waiting, info, and warning logs)")
	fmt.Fprintln(output, "  --debug")
}
