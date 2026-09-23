package metricsconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/profile"
)

func TestHexIdentifierParsingCharacterization(t *testing.T) {
	eojCases := []struct {
		input string
		ok    bool
	}{
		{"0x000000", true},
		{"0xFFFFFF", true},
		{"0x027D01", true},
		{"0x027d01", false},
		{"027D01", false},
		{"0X027D01", false},
		{"0x027D0", false},
		{"0x027D010", false},
	}
	for _, tc := range eojCases {
		t.Run("EOJ "+tc.input, func(t *testing.T) {
			_, err := echonet.ParseCanonicalEOJ(tc.input)
			if (err == nil) != tc.ok {
				t.Fatalf("parseEOJ(%q) error = %v, want ok=%t", tc.input, err, tc.ok)
			}
		})
	}

	epcCases := []struct {
		input string
		want  byte
		ok    bool
	}{
		{"0x00", 0x00, true},
		{"0xFF", 0xFF, true},
		{"0xE0", 0xE0, true},
		{"0xe0", 0, false},
		{"E0", 0, false},
		{"0XE0", 0, false},
		{"0xE", 0, false},
		{"0xE00", 0, false},
		{"0xGG", 0, false},
	}
	for _, tc := range epcCases {
		t.Run("EPC "+tc.input, func(t *testing.T) {
			got, err := echonet.ParseCanonicalEPC(tc.input)
			if (err == nil) != tc.ok || (tc.ok && got != tc.want) {
				t.Fatalf("parseEPC(%q) = 0x%02X, %v; want 0x%02X, ok=%t", tc.input, got, err, tc.want, tc.ok)
			}
		})
	}
}

func TestLoadResolvesAndSortsConfiguredInstances(t *testing.T) {
	path := writeConfig(t, `
instances_format: 2
instances:
  - address: 192.0.2.20
    eoj: "0x027D02"
    metric_prefix: "echonet_b_"
    interval_seconds: 60
    properties:
      - epc: "0xE1"
      - epc: "0xE0"
  - address: 192.0.2.10
    eoj: "0x027D01"
    metric_prefix: "echonet_a_"
    interval_seconds: 30
    properties: []
`)
	config, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Instances) != 2 || config.Instances[0].Address.String() != "192.0.2.10" {
		t.Fatalf("instances = %#v", config.Instances)
	}
	properties := config.Instances[1].Properties
	if len(properties) != 2 || properties[0].EPC != 0xE0 || properties[1].EPC != 0xE1 {
		t.Fatalf("properties = %#v", properties)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	cases := []struct {
		name, source, want string
	}{
		{"format 1 unsupported", strings.Replace(baseConfig(""), "instances_format: 2", "instances_format: 1", 1), "unsupported metrics configuration format"},
		{"unknown key", baseConfig("    unknown: true\n"), "field unknown"},
		{"legacy collection defaults", "instances_format: 2\ncollection_defaults: {interval_seconds: 60}\ninstances: []\n", "field collection_defaults"},
		{"duplicate object", baseConfig("  - address: 192.0.2.10\n    eoj: \"0x027D01\"\n    metric_prefix: \"echonet_b_\"\n    interval_seconds: 1\n    properties: []\n"), "duplicate address/EOJ"},
		{"lowercase EOJ", strings.Replace(baseConfig(""), "0x027D01", "0x027d01", 1), "invalid EOJ"},
		{"duplicate EPC", strings.Replace(baseConfig(""), "properties: []", "properties:\n      - epc: \"0xE0\"\n      - epc: \"0xE0\"", 1), "duplicate EPC"},
		{"bad prefix", strings.Replace(baseConfig(""), "echonet_a_", "echonet_a", 1), "metric_prefix must end"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.source), nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadValidatesConfiguredProfile(t *testing.T) {
	profilePath := filepath.Join(t.TempDir(), "profiles.yaml")
	if err := os.WriteFile(profilePath, []byte(`
catalog_format: 1
profiles:
  example:
    class: "0x027D"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	profiles, err := profile.Load(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(baseConfig(""), "metric_prefix:", "profile: example\n    metric_prefix:", 1)
	if _, err := Load(writeConfig(t, source), profiles); err != nil {
		t.Fatal(err)
	}
	wrongClass := strings.Replace(source, "0x027D01", "0x027901", 1)
	if _, err := Load(writeConfig(t, wrongClass), profiles); err == nil || !strings.Contains(err.Error(), "class does not match") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadResolvesCollectionPolicyByPrecedence(t *testing.T) {
	profilePath := filepath.Join(t.TempDir(), "profiles.yaml")
	if err := os.WriteFile(profilePath, []byte(`
catalog_format: 1
profiles:
  example:
    class: "0x027D"
    collection:
      interval_seconds: 30
      batch_size: 8
`), 0o600); err != nil {
		t.Fatal(err)
	}
	profiles, err := profile.Load(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	config, err := Load(writeConfig(t, `
instances_format: 2
defaults:
  collection:
    interval_seconds: 120
    batch_size: 16
instances:
  - address: 192.0.2.10
    eoj: "0x027D01"
    profile: example
    metric_prefix: "echonet_a_"
    batch_size: 2
    properties: []
  - address: 192.0.2.11
    eoj: "0x027D01"
    metric_prefix: "echonet_b_"
    properties: []
`), profiles)
	if err != nil {
		t.Fatal(err)
	}
	if got := config.Instances[0]; got.IntervalSeconds != 30 || got.BatchSize != 2 {
		t.Fatalf("profile and instance policy = %#v", got)
	}
	if got := config.Instances[1]; got.IntervalSeconds != 120 || got.BatchSize != 16 {
		t.Fatalf("configuration policy = %#v", got)
	}
}

func TestLoadUsesFallbackCollectionPolicyAndRejectsZeroBatchSize(t *testing.T) {
	config, err := Load(writeConfig(t, `
instances_format: 2
instances:
  - address: 192.0.2.10
    eoj: "0x027D01"
    metric_prefix: "echonet_a_"
    properties: []
`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := config.Instances[0]; got.IntervalSeconds != 60 || got.BatchSize != -1 {
		t.Fatalf("fallback policy = %#v", got)
	}
	for _, source := range []string{
		strings.Replace(baseConfig(""), "interval_seconds: 60", "batch_size: 0\n    interval_seconds: 60", 1),
		"instances_format: 2\ndefaults: {collection: {batch_size: 0}}\ninstances: []\n",
	} {
		if _, err := Load(writeConfig(t, source), nil); err == nil || !strings.Contains(err.Error(), "batch_size must not be zero") {
			t.Fatalf("Load() error = %v", err)
		}
	}
}

func baseConfig(extra string) string {
	return "instances_format: 2\ninstances:\n  - address: 192.0.2.10\n    eoj: \"0x027D01\"\n    metric_prefix: \"echonet_a_\"\n    interval_seconds: 60\n    properties: []\n" + extra
}

func writeConfig(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "instances.yaml")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
