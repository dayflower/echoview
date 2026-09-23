package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var version = "0.1.0"

// cliDependencies contains the operating-system boundaries used by commands.
// Keeping them here lets flag and output tests run without sockets or signals.
type cliDependencies struct {
	listenUDP     func(string, *net.UDPAddr) (*net.UDPConn, error)
	listenTCP     func(string, string) (net.Listener, error)
	localIPv4     func(string) (netip.Addr, error)
	now           func() time.Time
	signalContext func(context.Context) (context.Context, context.CancelFunc)
}

func defaultCLIDependencies() cliDependencies {
	return cliDependencies{
		listenUDP: net.ListenUDP,
		listenTCP: net.Listen,
		localIPv4: localIPv4,
		now:       time.Now,
		signalContext: func(parent context.Context) (context.Context, context.CancelFunc) {
			return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
		},
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, defaultCLIDependencies())
}

func runWithDependencies(args []string, stdout, stderr io.Writer, dependencies cliDependencies) int {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(stdout, version)
		return 0
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printUsage(stdout)
		return 0
	}
	switch args[0] {
	case "discover":
		return runDiscoverWithDependencies(args[1:], stdout, stderr, dependencies)
	case "dump":
		return runDumpWithDependencies(args[1:], stdout, stderr, dependencies)
	case "get":
		return runGetWithDependencies(args[1:], stdout, stderr, dependencies)
	case "watch":
		return runWatchWithDependencies(args[1:], stdout, stderr, dependencies)
	case "exporter", "prometheus":
		return runExporterWithDependencies(args[1:], stdout, stderr, dependencies)
	case "mqtt", "publisher":
		return runMQTTWithDependencies(args[1:], stdout, stderr, dependencies)
	default:
		fmt.Fprintf(stderr, "error: unknown subcommand %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: echoview <command> [options]")
	fmt.Fprintln(output, "\nCommands:")
	fmt.Fprintln(output, "  discover  discover ECHONET Lite node and device instances")
	fmt.Fprintln(output, "  dump      read the advertised Get properties of selected instances")
	fmt.Fprintln(output, "  get       read configured ECHONET Lite properties once")
	fmt.Fprintln(output, "  watch     continuously print configured properties after each collection")
	fmt.Fprintln(output, "  exporter  expose periodic collection snapshots for Prometheus")
	fmt.Fprintln(output, "  mqtt      publish periodic collection snapshots to MQTT")
}
