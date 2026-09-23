package prometheus

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/metricsconfig"
	"github.com/dayflower/echoview/internal/model"
	"github.com/dayflower/echoview/internal/profile"
	"github.com/dayflower/echoview/internal/scheduler"
)

func TestExporterWritesNumericValuesAndCollectionHealth(t *testing.T) {
	target := scheduler.Target{Instance: metricsconfig.Instance{
		Address: netip.MustParseAddr("192.0.2.10"), EOJ: echonet.EOJ{0x01, 0x30, 0x01}, MetricPrefix: "echonet_air_", Labels: map[string]string{"node": "kitchen"},
		Properties: []metricsconfig.Property{{EPC: 0xB0, MetricName: "temperature"}, {EPC: 0xB1, MetricName: "mode"}, {EPC: 0xB2, MetricName: "unavailable"}, {EPC: 0xB4, MetricName: "sna"}},
	}}
	collectedAt := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	store := staticReader{snapshot: scheduler.Snapshot{Instances: []scheduler.InstanceSnapshot{{
		Address: target.Instance.Address.String(), EOJ: target.Instance.EOJ, CollectedAt: collectedAt, LastSucceededAt: collectedAt, Completed: true, Succeeded: true,
		Result: model.InstanceResult{EOJ: target.Instance.EOJ, GetProperties: []model.PropertyResult{
			{EPC: "0xB0", Value: "23.5", ValueKind: "uint", Status: model.PropertyOK},
			{EPC: "0xB1", Value: "cool", ValueKind: "enum", Status: model.PropertyOK},
			{EPC: "0xB2", Value: nil, Status: model.PropertyTimeout},
			{EPC: "0xB3", Value: 1, Status: model.PropertyOK},
			{EPC: "0xB4", Value: "7", ValueKind: "uint", Status: model.PropertySNA},
		}},
	}}}}
	exporter, err := New(Config{Snapshots: store, Instances: []metricsconfig.Instance{target.Instance}, Now: func() time.Time { return collectedAt.Add(90 * time.Second) }})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	exporter.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	text := response.Body.String()
	for _, expected := range []string{
		"# TYPE echonet_air_temperature gauge", "echonet_air_temperature{node=\"kitchen\"} 23.5",
		"echonet_lite_collection_success{node=\"kitchen\"} 1",
		"echonet_lite_collection_last_success_timestamp_seconds{node=\"kitchen\"} 1.7896032e+09",
		"echonet_lite_collection_age_seconds{node=\"kitchen\"} 90",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("metrics do not contain %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "echonet_air_mode") || strings.Contains(text, "echonet_air_unavailable") || strings.Contains(text, "echonet_air_sna") || strings.Contains(text, "0xB3") || strings.Contains(text, "timeout") {
		t.Fatalf("non-numeric or unavailable property was exported:\n%s", text)
	}
}

func TestExporterRetainsLastSuccessTimeAfterCollectionFailure(t *testing.T) {
	target := scheduler.Target{Instance: metricsconfig.Instance{Address: netip.MustParseAddr("192.0.2.10"), EOJ: echonet.EOJ{0x01, 0x30, 0x01}, MetricPrefix: "echonet_air_", IntervalSeconds: 60}}
	first := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	store := staticReader{snapshot: scheduler.Snapshot{Instances: []scheduler.InstanceSnapshot{{
		Address: target.Instance.Address.String(), EOJ: target.Instance.EOJ, CollectedAt: first.Add(time.Minute), LastSucceededAt: first, Completed: true, Succeeded: false, Error: "connection closed",
	}}}}
	exporter, err := New(Config{Snapshots: store, Instances: []metricsconfig.Instance{target.Instance}, Now: func() time.Time { return first.Add(2 * time.Minute) }})
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := exporter.WriteMetrics(&output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "echonet_lite_collection_success 0") || !strings.Contains(text, "echonet_lite_collection_age_seconds 120") || !strings.Contains(text, "echonet_lite_collection_last_success_timestamp_seconds 1.7896032e+09") {
		t.Fatalf("failure health metrics =\n%s", text)
	}
}

func TestExporterDoesNotExposeTimestampOrAgeBeforeInitialCollection(t *testing.T) {
	target := scheduler.Target{Instance: metricsconfig.Instance{Address: netip.MustParseAddr("192.0.2.10"), EOJ: echonet.EOJ{0x01, 0x30, 0x01}, MetricPrefix: "echonet_air_", IntervalSeconds: 60}}
	store := staticReader{snapshot: scheduler.Snapshot{Instances: []scheduler.InstanceSnapshot{{Address: target.Instance.Address.String(), EOJ: target.Instance.EOJ}}}}
	exporter, err := New(Config{Snapshots: store, Instances: []metricsconfig.Instance{target.Instance}})
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := exporter.WriteMetrics(&output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "echonet_lite_collection_success 0") || strings.Contains(text, "last_success") || strings.Contains(text, "age_seconds") {
		t.Fatalf("initial metrics =\n%s", text)
	}
}

func TestExporterProvidesHealthAndDoesNotCallCollector(t *testing.T) {
	target := scheduler.Target{Instance: metricsconfig.Instance{Address: netip.MustParseAddr("192.0.2.10"), EOJ: echonet.EOJ{0x01, 0x30, 0x01}, MetricPrefix: "echonet_air_"}}
	store := staticReader{snapshot: scheduler.Snapshot{Instances: []scheduler.InstanceSnapshot{{Address: target.Instance.Address.String(), EOJ: target.Instance.EOJ}}}}
	exporter, err := New(Config{Snapshots: store, Instances: []metricsconfig.Instance{target.Instance}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	exporter.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK || response.Body.String() != "ok\n" {
		t.Fatalf("health response = %d %q", response.Code, response.Body.String())
	}
}

func TestExporterUsesPrometheusPoliciesAndNumericArrays(t *testing.T) {
	directory := t.TempDir()
	catalogPath := filepath.Join(directory, "catalog.yaml")
	if err := os.WriteFile(catalogPath, []byte(`
catalog_format: 1
classes:
  "0x0130":
    name: {ja: test}
    short_name: test
    properties:
      "0xB0":
        name: {ja: mode}
        short_name: mode
        prometheus:
          export: enum_map
          enum_map:
            values: {"0x30": 0}
        codec: {kinds: [{kind: enum, size: 1, values: {"0x30": {value: off}}}]}
      "0xB1":
        name: {ja: unknown_mode}
        short_name: unknown_mode
        prometheus:
          export: enum_map
          enum_map:
            values: {"0x30": 0}
        codec: {kinds: [{kind: enum, size: 1, values: {"0x30": {value: off}}}]}
      "0xB2":
        name: {ja: raw_code}
        short_name: raw_code
        prometheus:
          export: raw_uint
          raw_uint: {bytes: 2}
        codec: {kinds: [{kind: raw}]}
      "0xB3":
        name: {ja: samples}
        short_name: samples
        codec: {kinds: [{kind: array, itemSize: 1, items: {kinds: [{kind: uint, bytes: 1}]}}]}
      "0xB4":
        name: {ja: text}
        short_name: text
        codec: {kinds: [{kind: text}]}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	loadedCatalog, err := catalog.Load(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(directory, "profiles.yaml")
	if err := os.WriteFile(profilePath, []byte(`
catalog_format: 1
profiles:
  test:
    class: "0x0130"
    properties:
      "0xB0":
        prometheus:
          export: enum_map
          enum_map:
            values: {"0x31": 7}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	loadedProfiles, err := profile.Load(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	instance := metricsconfig.Instance{
		Address: netip.MustParseAddr("192.0.2.10"), EOJ: echonet.EOJ{0x01, 0x30, 0x01}, ProfileID: "test", MetricPrefix: "echonet_test_",
		Labels: map[string]string{"node": "kitchen"},
		Properties: []metricsconfig.Property{
			{EPC: 0xB0, MetricName: "mode"}, {EPC: 0xB1, MetricName: "unknown_mode"}, {EPC: 0xB2, MetricName: "raw_code"}, {EPC: 0xB3, MetricName: "samples"}, {EPC: 0xB4, MetricName: "text"},
		},
	}
	modeRaw, unknownRaw, rawCode := "0x31", "0x7F", "0x000A"
	collectedAt := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	store := staticReader{snapshot: scheduler.Snapshot{Instances: []scheduler.InstanceSnapshot{{
		Address: instance.Address.String(), EOJ: instance.EOJ, CollectedAt: collectedAt, LastSucceededAt: collectedAt, Completed: true, Succeeded: true,
		Result: model.InstanceResult{EOJ: instance.EOJ, GetProperties: []model.PropertyResult{
			{EPC: "0xB0", RawEDT: &modeRaw, Value: "ON", ValueKind: "enum", Status: model.PropertyOK},
			{EPC: "0xB1", RawEDT: &unknownRaw, Value: "unknown", ValueKind: "enum", Status: model.PropertyOK},
			{EPC: "0xB2", RawEDT: &rawCode, Value: rawCode, ValueKind: "raw", Status: model.PropertyOK},
			{EPC: "0xB3", Value: []any{uint64(2), uint64(3)}, ValueKind: "array", Status: model.PropertyOK},
			{EPC: "0xB4", Value: "12", ValueKind: "text", Status: model.PropertyOK},
		}},
	}}}}
	exporter, err := New(Config{Snapshots: store, Instances: []metricsconfig.Instance{instance}, Profiles: loadedProfiles, Catalog: loadedCatalog})
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := exporter.WriteMetrics(&output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{
		"echonet_test_mode{node=\"kitchen\"} 7",
		"echonet_test_unknown_mode{node=\"kitchen\"} -1",
		"echonet_test_raw_code{node=\"kitchen\"} 10",
		"echonet_test_samples{item_index=\"0\",node=\"kitchen\"} 2",
		"echonet_test_samples{item_index=\"1\",node=\"kitchen\"} 3",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("metrics do not contain %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "echonet_test_text") {
		t.Fatalf("text value was exported:\n%s", text)
	}
}

func TestExporterWritesConfiguredCounters(t *testing.T) {
	directory := t.TempDir()
	catalogPath := filepath.Join(directory, "catalog.yaml")
	if err := os.WriteFile(catalogPath, []byte(`
catalog_format: 1
classes:
  "0x0279":
    name: {ja: solar}
    short_name: solar
    properties:
      "0xE1":
        name: {ja: generated energy}
        short_name: generated_energy
        prometheus: {metric_type: counter}
        codec: {kinds: [{kind: uint, bytes: 4, scale: "0.001", unit: kWh}]}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Load(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	instance := metricsconfig.Instance{
		Address: netip.MustParseAddr("192.0.2.10"), EOJ: echonet.EOJ{0x02, 0x79, 0x01}, MetricPrefix: "echonet_pv_",
		Properties: []metricsconfig.Property{{EPC: 0xE1, MetricName: "generated_energy"}},
	}
	store := staticReader{snapshot: scheduler.Snapshot{Instances: []scheduler.InstanceSnapshot{{
		Address: instance.Address.String(), EOJ: instance.EOJ, Completed: true, Succeeded: true,
		Result: model.InstanceResult{EOJ: instance.EOJ, GetProperties: []model.PropertyResult{{
			EPC: "0xE1", Value: "19.578", ValueKind: "uint", Status: model.PropertyOK,
		}}},
	}}}}
	exporter, err := New(Config{Snapshots: store, Instances: []metricsconfig.Instance{instance}, Catalog: loaded})
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := exporter.WriteMetrics(&output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "# TYPE echonet_pv_generated_energy counter") {
		t.Fatalf("counter metric type missing:\n%s", output.String())
	}
}

func TestExporterRejectsConflictingConfiguredMetricTypes(t *testing.T) {
	directory := t.TempDir()
	catalogPath := filepath.Join(directory, "catalog.yaml")
	if err := os.WriteFile(catalogPath, []byte(`
catalog_format: 1
classes:
  "0x0130":
    name: {ja: test}
    short_name: test
    properties:
      "0xB0":
        name: {ja: gauge}
        short_name: gauge
        codec: {kinds: [{kind: uint, bytes: 1}]}
      "0xB1":
        name: {ja: counter}
        short_name: counter
        prometheus: {metric_type: counter}
        codec: {kinds: [{kind: uint, bytes: 1}]}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Load(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	instance := metricsconfig.Instance{
		Address: netip.MustParseAddr("192.0.2.10"), EOJ: echonet.EOJ{0x01, 0x30, 0x01}, MetricPrefix: "echonet_test_",
		Properties: []metricsconfig.Property{{EPC: 0xB0, MetricName: "value"}, {EPC: 0xB1, MetricName: "value"}},
	}
	if _, err := New(Config{Snapshots: staticReader{}, Instances: []metricsconfig.Instance{instance}, Catalog: loaded}); err == nil {
		t.Fatal("New() succeeded with conflicting metric types")
	}
}

func TestExporterRejectsItemIndexInstanceLabelForArrays(t *testing.T) {
	instance := metricsconfig.Instance{
		Address: netip.MustParseAddr("192.0.2.10"), EOJ: echonet.EOJ{0x02, 0xA4, 0x01}, Labels: map[string]string{"item_index": "configured"},
		Properties: []metricsconfig.Property{{EPC: 0xC2, MetricName: "samples"}},
	}
	catalogPath := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := os.WriteFile(catalogPath, []byte(`
catalog_format: 1
classes:
  "0x02A4":
    name: {ja: test}
    short_name: test
    properties:
      "0xC2":
        name: {ja: samples}
        short_name: samples
        codec:
          kinds:
            - kind: array
              itemSize: 1
              items: {kind: uint, bytes: 1}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Load(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{Snapshots: staticReader{}, Instances: []metricsconfig.Instance{instance}, Catalog: loaded}); err == nil {
		t.Fatal("New() succeeded with an array item_index collision")
	}
}

func TestExporterMetricNameFallsBackToProfileShortNameThenEPC(t *testing.T) {
	directory := t.TempDir()
	profilePath := filepath.Join(directory, "profiles.yaml")
	if err := os.WriteFile(profilePath, []byte(`
catalog_format: 1
profiles:
  example:
    class: "0x0130"
    properties:
      "0xF2":
        name: {ja: test}
        short_name: profile_power
        codec: {kinds: [{kind: uint, bytes: 4}]}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	loadedProfiles, err := profile.Load(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	instance := metricsconfig.Instance{EOJ: echonet.EOJ{0x01, 0x30, 0x01}, ProfileID: "example", MetricPrefix: "echonet_test_", Properties: []metricsconfig.Property{{EPC: 0xF2}, {EPC: 0xF3}}}
	exporter, err := New(Config{Snapshots: staticReader{}, Instances: []metricsconfig.Instance{instance}, Profiles: loadedProfiles})
	if err != nil {
		t.Fatal(err)
	}
	if got := exporter.metricName(instance, model.PropertyResult{EPC: "0xF2"}); got != "echonet_test_profile_power" {
		t.Fatalf("profile metric name = %q", got)
	}
	if got := exporter.metricName(instance, model.PropertyResult{EPC: "0xF3"}); got != "echonet_test_epc_0xf3" {
		t.Fatalf("EPC fallback metric name = %q", got)
	}
}

type staticReader struct{ snapshot scheduler.Snapshot }

func (r staticReader) Snapshot() scheduler.Snapshot { return r.snapshot }
