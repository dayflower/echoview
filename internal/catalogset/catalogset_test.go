package catalogset

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dayflower/echoview/internal/echonet"
)

func TestLoadMergesOrderedDirectoriesBeforeResolvingInheritance(t *testing.T) {
	base := t.TempDir()
	override := t.TempDir()
	writeFixture(t, base, "20-parent.yaml", `
catalog_format: 1
classes:
  "0x0000":
    properties:
      "0x80":
        name: { en: Parent status }
        codec: { kinds: [{ kind: enum, values: { "0x30": { value: true } } }] }
profiles:
  device:
    class: "0x0130"
    properties:
      "0x80": { metric_name: status }
`)
	writeFixture(t, override, "10-child.yml", `
catalog_format: 1
classes:
  "0x0130":
    extends: ["0x0000"]
    properties:
      "0x80":
        name: { en: Replacement status }
        codec: { kinds: [{ kind: enum, values: { "0x31": { value: false } } }] }
profiles:
  device:
    properties:
      "0x80": { prometheus: { export: raw_uint, raw_uint: { bytes: 1 } } }
`)
	loaded, err := Load(Options{Directories: []string{base, override}})
	if err != nil {
		t.Fatal(err)
	}
	property, ok := loaded.Catalog.PropertyForEOJ(echonet.EOJ{0x01, 0x30, 0x01}, 0x80)
	if !ok || property.NameEN != "Replacement status" || len(property.Codecs) != 1 || property.Codecs[0].Values["0x31"].Value != false {
		t.Fatalf("unexpected merged class property: %#v, found=%v", property, ok)
	}
	device, ok := loaded.Profiles.Get("device")
	if !ok {
		t.Fatal("merged profile was not resolved")
	}
	if name, ok := device.MetricName(0x80); !ok || name != "status" {
		t.Fatalf("metric-name patch was not retained: %q, found=%v", name, ok)
	}
	if policy, ok := device.PrometheusPolicy(0x80); !ok || policy.Export != "raw_uint" || policy.RawUint == nil || policy.RawUint.Bytes != 1 {
		t.Fatalf("prometheus patch was not merged: %#v, found=%v", policy, ok)
	}
	if got := loaded.Classes["0x0130"].File; got != filepath.Join(override, "10-child.yml") {
		t.Fatalf("class provenance = %q", got)
	}
}

func TestLoadIgnoresUnknownKeysAndWarnsForUnavailableDirectories(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "catalog.yaml", "catalog_format: 1\nunknown_root: ignored\nclasses: {}\nprofiles: {}\n")
	missing := filepath.Join(t.TempDir(), "missing")
	loaded, err := Load(Options{Directories: []string{missing, directory}})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Warnings) != 1 || loaded.Warnings[0].Directory != missing {
		t.Fatalf("warnings = %#v", loaded.Warnings)
	}
	if len(loaded.Files) != 1 {
		t.Fatalf("files = %#v", loaded.Files)
	}
}

func TestLoadSortsFilesAndDoesNotRecurse(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "z.yml", "catalog_format: 1\nclasses: {}\nprofiles: {}\n")
	writeFixture(t, directory, "a.yaml", "catalog_format: 1\nclasses: {}\nprofiles: {}\n")
	if err := os.Mkdir(filepath.Join(directory, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(directory, "nested"), "ignored.yaml", "catalog_format: 1\nclasses: {}\nprofiles: {}\n")
	loaded, err := Load(Options{Directories: []string{directory}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(directory, "a.yaml"), filepath.Join(directory, "z.yml")}
	if !reflect.DeepEqual(loaded.Files, want) {
		t.Fatalf("files = %#v, want %#v", loaded.Files, want)
	}
}

func TestDefaultDirectoriesUseExecutableInsteadOfWorkingDirectory(t *testing.T) {
	got, err := DefaultDirectories(Options{OS: "darwin", ExecutablePath: "/opt/echoview/bin/echoview", UserConfigDir: "/Users/test/Library/Application Support"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/Library/Application Support/echoview/catalog",
		"/opt/echoview/bin/catalog",
		"/Users/test/Library/Application Support/echoview/catalog",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("directories = %#v, want %#v", got, want)
	}
}

func TestLoadRejectsMultipleDocumentsAndReportsDefinitionSource(t *testing.T) {
	directory := t.TempDir()
	multiple := writeFixture(t, directory, "multiple.yaml", "catalog_format: 1\n---\ncatalog_format: 1\n")
	if _, err := Load(Options{Directories: []string{directory}}); err == nil || !strings.Contains(err.Error(), multiple) {
		t.Fatalf("multiple documents error = %v", err)
	}
	if err := os.Remove(multiple); err != nil {
		t.Fatal(err)
	}
	invalid := writeFixture(t, directory, "invalid.yaml", "catalog_format: 1\nclasses:\n  \"0x0130\": { properties: { \"invalid\": {} } }\n")
	if _, err := Load(Options{Directories: []string{directory}}); err == nil || !strings.Contains(err.Error(), invalid) {
		t.Fatalf("definition error = %v", err)
	}
}

func writeFixture(t *testing.T, directory, name, contents string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
