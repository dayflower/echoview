package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/echonet"
)

func TestParseEPCCharacterization(t *testing.T) {
	cases := []struct {
		input string
		want  byte
		ok    bool
	}{
		{"0x00", 0x00, true},
		{"0xFF", 0xFF, true},
		{"0xe0", 0xE0, true},
		{"E0", 0, false},
		{"0XE0", 0, false},
		{"0xE", 0, false},
		{"0xE00", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := echonet.ParseEPC(tc.input)
			if (err == nil) != tc.ok || (tc.ok && got != tc.want) {
				t.Fatalf("parseEPC(%q) = 0x%02X, %v; want 0x%02X, ok=%t", tc.input, got, err, tc.want, tc.ok)
			}
		})
	}
}

func TestProfileLoadsAndMatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	data := []byte(`
catalog_format: 1
profiles:
  example:
    class: "0x027D"
    match:
      required:
        "0x8A": ["0x000005"]
      optional:
        "0x8B": ["0x313132"]
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := loaded.Get("example")
	if !ok || !p.ClassMatches(echonet.EOJ{0x02, 0x7D, 0x01}) {
		t.Fatalf("profile = %#v", p)
	}
	state, detail := MatchState(p, map[byte][]byte{0x8A: {0, 0, 5}, 0x8B: []byte("112")})
	if state != "matched" || detail != "" {
		t.Fatalf("match = %q, %q", state, detail)
	}
	state, _ = MatchState(p, map[byte][]byte{0x8A: {0, 0, 5}})
	if state != "unknown" {
		t.Fatalf("optional absence state = %q", state)
	}
	for _, test := range []struct {
		name   string
		values map[byte][]byte
		want   string
	}{
		{"missing required", map[byte][]byte{}, "mismatched"},
		{"required mismatch", map[byte][]byte{0x8A: {0, 0, 6}}, "mismatched"},
		{"optional mismatch", map[byte][]byte{0x8A: {0, 0, 5}, 0x8B: []byte("999")}, "mismatched"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, _ := MatchState(p, test.values)
			if state != test.want {
				t.Fatalf("MatchState() = %q, want %q", state, test.want)
			}
		})
	}
}

func TestProfileInheritanceRetainsPropertyCodecs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	data := []byte(`
catalog_format: 1
profiles:
  parent:
    class: "0x05FF"
    properties:
      "0xE1":
        name: {ja: 親プロパティ, en: Parent property}
        codec: {kinds: [{kind: uint, bytes: 2, unit: W}]}
  child:
    extends: [parent]
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	child, ok := loaded.Get("child")
	if !ok || !child.ClassMatches(echonet.EOJ{0x05, 0xFF, 0x01}) {
		t.Fatalf("child = %#v", child)
	}
	definition, ok := child.Definition(0xE1)
	if !ok || definition.NameJA != "親プロパティ" {
		t.Fatalf("inherited definition = %#v, found = %t", definition, ok)
	}
}

func TestProfileMetricName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	data := []byte("catalog_format: 1\nprofiles:\n  example:\n    class: \"0x0130\"\n    properties:\n      \"0xE0\":\n        metric_name: remaining_capacity\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := loaded.Get("example")
	if name, found := p.MetricName(0xE0); !ok || !found || name != "remaining_capacity" {
		t.Fatalf("metric name = %q, %t", name, found)
	}
}

func TestProfilePropertyCodecAppliesDecimalScale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	data := []byte(`
catalog_format: 1
profiles:
  example:
    class: "0x0130"
    properties:
      "0xF2":
        name: {ja: 電圧}
        codec: {kinds: [{kind: uint, bytes: 2, scale: "0.1", unit: V}]}
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := loaded.Get("example")
	if !ok {
		t.Fatal("profile was not loaded")
	}
	definition, found := p.Definition(0xF2)
	if !found {
		t.Fatal("profile property was not loaded")
	}
	decoded := catalog.DecodePropertyDefinition(definition, []byte{0, 123}, catalog.LocaleJapanese)
	if !decoded.Decoded || decoded.Value != "12.3" || decoded.Unit != "V" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestProfileRejectsUnknownNestedCodecKindWithLocation(t *testing.T) {
	_, err := LoadYAML([]byte(`
catalog_format: 1
profiles:
  example:
    class: "0x0130"
    properties:
      "0xE0":
        codec:
          kinds:
            - kind: array
              itemSize: 1
              items: {kind: unknown_item_kind}
`), "profile-fixture.yaml")
	if err == nil {
		t.Fatal("LoadYAML() succeeded")
	}
	for _, expected := range []string{
		`profile catalog "profile-fixture.yaml"`, `profile "example"`, "properties 0xE0",
		`codec.kinds[0].items[0]: unknown codec kind "unknown_item_kind"`,
	} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error %q does not contain %q", err, expected)
		}
	}
}

func TestProfileCollectionBatchSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	data := []byte("catalog_format: 1\nprofiles:\n  parent:\n    class: \"0x0130\"\n    collection:\n      batch_size: -1\n  child:\n    extends: [parent]\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := loaded.Get("child")
	if !ok || p.Collection.BatchSize == nil || *p.Collection.BatchSize != -1 {
		t.Fatalf("collection policy = %#v", p.Collection)
	}
	if err := os.WriteFile(path, []byte("catalog_format: 1\nprofiles:\n  invalid:\n    class: \"0x0130\"\n    collection: { batch_size: 0 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted a zero batch_size")
	}
}

func TestProfileCollectionIntervalInheritsAndValidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	data := []byte("catalog_format: 1\nprofiles:\n  parent:\n    class: \"0x0130\"\n    collection:\n      interval_seconds: 45\n  child:\n    extends: [parent]\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := loaded.Get("child")
	if !ok || p.Collection.IntervalSeconds == nil || *p.Collection.IntervalSeconds != 45 {
		t.Fatalf("collection policy = %#v", p.Collection)
	}
	if err := os.WriteFile(path, []byte("catalog_format: 1\nprofiles:\n  invalid:\n    class: \"0x0130\"\n    collection: { interval_seconds: 0 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted interval_seconds: 0")
	}
}

func TestProfilePrometheusEnumMapMergesAndCanReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	data := []byte(`
catalog_format: 1
profiles:
  parent:
    class: "0x0130"
    properties:
      "0xB0":
        prometheus:
          metric_type: counter
          export: enum_map
          enum_map:
            values: {"0x30": 0, "0x31": 1}
  merged:
    extends: [parent]
    properties:
      "0xB0":
        prometheus:
          enum_map:
            values: {"0x32": 2}
  replaced:
    extends: [parent]
    properties:
      "0xB0":
        prometheus:
          enum_map:
            replace: true
            values: {"0x32": 2}
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		profile string
		values  map[string]int64
	}{
		{"merged", map[string]int64{"0x30": 0, "0x31": 1, "0x32": 2}},
		{"replaced", map[string]int64{"0x32": 2}},
	} {
		p, ok := loaded.Get(test.profile)
		policy, found := p.PrometheusPolicy(0xB0)
		if !ok || !found || policy.MetricType != "counter" || policy.EnumMap == nil || len(policy.EnumMap.Values) != len(test.values) {
			t.Fatalf("profile %s policy = %#v", test.profile, policy)
		}
		for edt, value := range test.values {
			if policy.EnumMap.Values[edt] != value {
				t.Fatalf("profile %s values = %#v", test.profile, policy.EnumMap.Values)
			}
		}
	}
}
