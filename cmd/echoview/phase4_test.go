package main

import (
	"bytes"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/discover"
	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/model"
	"github.com/dayflower/echoview/internal/scheduler"
)

func TestCLIOutputUsesBuffers(t *testing.T) {
	var output bytes.Buffer
	printUsage(&output)
	printDiscoverUsage(&output)
	printDumpUsage(&output)
	printGetUsage(&output)
	printWatchUsage(&output)
	printExporterUsage(&output)
	printMQTTUsage(&output)
	text := output.String()
	for _, expected := range []string{"Usage: echoview <command>", "Usage: echoview discover", "Usage: echoview dump", "Usage: echoview get", "Usage: echoview watch", "Usage: echoview exporter", "Usage: echoview mqtt"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("usage does not contain %q:\n%s", expected, text)
		}
	}
}

func TestTargetFlagValues(t *testing.T) {
	var direct addresses
	if err := direct.Set("192.0.2.10"); err != nil || direct.String() != "192.0.2.10" {
		t.Fatalf("direct target = %q, %v", direct.String(), err)
	}
	if err := direct.Set("2001:db8::1"); err == nil {
		t.Fatal("IPv6 target was accepted")
	}
	var targets dumpTargets
	if err := targets.Set("192.0.2.10/0x013001"); err != nil {
		t.Fatal(err)
	}
	if got := targets.String(); got != "192.0.2.10/0x013001" {
		t.Fatalf("dump target = %q", got)
	}
	if err := targets.Set("192.0.2.10/not-an-eoj"); err == nil {
		t.Fatal("malformed EOJ was accepted")
	}
	var assignments profileAssignments
	if err := assignments.Set("192.0.2.10/0x013001=test"); err != nil {
		t.Fatal(err)
	}
	if got := assignments.String(); got != "192.0.2.10/0x013001=test" {
		t.Fatalf("assignment = %q", got)
	}
	if err := assignments.Set("192.0.2.10=test"); err == nil {
		t.Fatal("malformed assignment was accepted")
	}
}

func TestTextOutputHelpers(t *testing.T) {
	loadedCatalog, err := catalog.Load(writeCatalogFixture(t, `
catalog_format: 1
classes:
  "0x0130":
    name: {ja: test}
    short_name: test
    properties:
      "0x80":
        name: {ja: operation_status}
        short_name: operation_status
        codec: {kinds: [{kind: enum, size: 1, values: {"0x30": {label: {en: On, ja: ON}}}}]}
      "0x81":
        name: {ja: installation_location}
        short_name: installation_location
        codec: {kinds: [{kind: uint, bytes: 1}]}
`))
	if err != nil {
		t.Fatal(err)
	}
	eoj := echonet.EOJ{0x01, 0x30, 0x01}
	var output bytes.Buffer
	printNodes(&output, netip.MustParseAddr("192.0.2.1"), []discover.Node{{
		Address: netip.MustParseAddr("192.0.2.10"), NodeProfileEOJ: echonet.NodeProfileEOJ,
		Instances:          map[echonet.EOJ]struct{}{eoj: {}},
		NodeProperties:     map[byte]discover.Property{0x83: {EPC: 0x83, EDT: []byte{1}}, 0xD3: {EPC: 0xD3}, 0xD4: {EPC: 0xD4}},
		InstanceProperties: map[echonet.EOJ]map[byte]discover.Property{eoj: {0x80: {EPC: 0x80, EDT: []byte{0x30}}, 0x81: {EPC: 0x81}}},
	}}, loadedCatalog, catalog.LocaleEnglish)
	raw := "0x30"
	printDumpResults(&output, netip.MustParseAddr("192.0.2.1"), []model.NodeResult{{Address: "192.0.2.10", Instances: []model.InstanceResult{{EOJ: eoj, GetProperties: []model.PropertyResult{{EPC: "0x80", RawEDT: &raw, Value: "On", Status: model.PropertyOK}}}}}}, true, catalog.LocaleEnglish)
	if text := output.String(); !strings.Contains(text, "Nodes: 1") || !strings.Contains(text, "0x013001") {
		t.Fatalf("text output = %s", text)
	}
}

func TestInjectedDependenciesHandleSocketFailures(t *testing.T) {
	dependencies := defaultCLIDependencies()
	dependencies.now = func() time.Time { return time.Unix(0, 123) }
	dependencies.localIPv4 = func(string) (netip.Addr, error) { return netip.MustParseAddr("192.0.2.1"), nil }
	dependencies.listenUDP = func(string, *net.UDPAddr) (*net.UDPConn, error) { return nil, errors.New("denied") }
	var stdout, stderr bytes.Buffer
	catalogDirectory := writeCatalogDirectoryFixture(t, "catalog_format: 1\nclasses: {}\n")
	code := runWithDependencies([]string{"discover", "--catalog-dir=" + catalogDirectory}, &stdout, &stderr, dependencies)
	if code != 4 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "open ECHONET Lite socket") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestCatalogDirectoryWarningsAndDebugDiagnostics(t *testing.T) {
	dependencies := defaultCLIDependencies()
	dependencies.now = func() time.Time { return time.Unix(0, 123) }
	dependencies.localIPv4 = func(string) (netip.Addr, error) { return netip.MustParseAddr("192.0.2.1"), nil }
	dependencies.listenUDP = func(string, *net.UDPAddr) (*net.UDPConn, error) { return nil, errors.New("denied") }
	missingDirectory := filepath.Join(t.TempDir(), "missing")
	catalogDirectory := writeCatalogDirectoryFixture(t, "catalog_format: 1\nclasses: {}\n")

	var stdout, stderr bytes.Buffer
	code := runWithDependencies([]string{"discover", "--catalog-dir=" + missingDirectory}, &stdout, &stderr, dependencies)
	if code != 4 || !strings.Contains(stderr.String(), "catalog directory") || !strings.Contains(stderr.String(), "does not exist") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runWithDependencies([]string{"discover", "--quiet", "--catalog-dir=" + missingDirectory}, &stdout, &stderr, dependencies)
	if code != 4 || strings.Contains(stderr.String(), "catalog directory") {
		t.Fatalf("quiet code=%d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runWithDependencies([]string{"discover", "--debug", "--catalog-dir=" + catalogDirectory}, &stdout, &stderr, dependencies)
	if code != 4 || !strings.Contains(stderr.String(), "loaded catalog file") {
		t.Fatalf("debug code=%d stderr=%q", code, stderr.String())
	}
}

func TestRemovedCatalogPathFlagsAreRejected(t *testing.T) {
	for _, command := range []string{"discover", "dump", "get", "watch", "exporter", "mqtt"} {
		t.Run(command, func(t *testing.T) {
			code, _, stderr := runForTest(t, []string{command, "--catalog=legacy.yaml"})
			if code != 2 || !strings.Contains(stderr, "flag provided but not defined") {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
		})
	}
}

func TestDumpLoadsOnlyExplicitProfileFromCatalogDirectory(t *testing.T) {
	dependencies := defaultCLIDependencies()
	dependencies.localIPv4 = func(string) (netip.Addr, error) { return netip.Addr{}, errors.New("network disabled") }
	catalogDirectory := writeCatalogDirectoryFixture(t, `
catalog_format: 1
profiles:
  test_device:
    class: "0x0130"
`)
	code, _, stderr := runForDependencies(t, []string{
		"dump",
		"--target=192.0.2.10/0x013001",
		"--catalog-dir=" + catalogDirectory,
		"--instance-profile=192.0.2.10/0x013001=test_device",
	}, dependencies)
	if code != 2 || !strings.Contains(stderr, "network disabled") || strings.Contains(stderr, "unknown profile") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func writeCatalogFixture(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeCatalogDirectoryFixture(t *testing.T, source string) string {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "catalog.yaml")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestLocalIPv4AndMQTTTLSDefaults(t *testing.T) {
	if _, err := localIPv4("2001:db8::1"); err == nil {
		t.Fatal("IPv6 interface was accepted")
	}
	if address, err := localIPv4("192.0.2.1"); err != nil || address.String() != "192.0.2.1" {
		t.Fatalf("local IPv4 = %s, %v", address, err)
	}
	config, err := mqttTLSConfig("", "", "", false)
	if err != nil || config != nil {
		t.Fatalf("default TLS config = %#v, %v", config, err)
	}
}

func TestWatchOutputAcceptsBuffer(t *testing.T) {
	var output bytes.Buffer
	err := printWatchText(&output, scheduler.CollectionEvent{Instance: scheduler.InstanceSnapshot{Address: "192.0.2.10", EOJ: echonet.EOJ{0x01, 0x30, 0x01}, CollectedAt: time.Unix(0, 0).UTC(), Succeeded: true, Result: model.InstanceResult{EOJ: echonet.EOJ{0x01, 0x30, 0x01}}}}, false, catalog.LocaleEnglish)
	if err != nil || !strings.Contains(output.String(), "192.0.2.10") {
		t.Fatalf("watch output = %q, %v", output.String(), err)
	}
}
