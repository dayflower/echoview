package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/discover"
	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/model"
	"github.com/dayflower/echoview/internal/profile"
)

func TestVersionOutput(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })
	for _, test := range []struct {
		name    string
		version string
	}{
		{name: "local build", version: "0.1.0"},
		{name: "release build", version: "1.2.3"},
	} {
		t.Run(test.name, func(t *testing.T) {
			version = test.version
			code, stdout, stderr := runForTest(t, []string{"--version"})
			if code != 0 || stdout != test.version+"\n" || stderr != "" {
				t.Fatalf("--version: code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestPrintPropertyShowsSNAAnnotation(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "discover-output")
	if err != nil {
		t.Fatal(err)
	}
	printProperty(output, "", echonet.EOJ{0x01, 0x30, 0x01}, discover.Property{EPC: 0x80, EDT: []byte{0x31}, Status: discover.StatusSNA}, nil, catalog.LocaleEnglish)
	printProperty(output, "", echonet.EOJ{0x01, 0x30, 0x01}, discover.Property{EPC: 0x81, Status: discover.StatusSNA}, nil, catalog.LocaleEnglish)
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "sna") {
		t.Fatalf("discover output must show SNA annotations: %s", content)
	}
	if !strings.Contains(string(content), "unavailable") {
		t.Fatalf("missing values must still be reported: %s", content)
	}
}

func TestRunExposesDumpAndGet(t *testing.T) {
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"dump", "--help"}, stdout, stderr); code != 0 {
		t.Fatalf("dump --help exit code = %d", code)
	}
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Usage: echoview dump") || !strings.Contains(string(content), "--quiet") {
		t.Fatalf("dump help = %s", content)
	}
	if code := run([]string{"get", "--help"}, stdout, stderr); code != 0 {
		t.Fatalf("get --help exit code = %d", code)
	}
	if code := run([]string{"get"}, stdout, stderr); code != 2 {
		t.Fatalf("get without config exit code = %d, want 2", code)
	}
}

func TestRunExposesPrometheusExporter(t *testing.T) {
	stdout, err := os.CreateTemp(t.TempDir(), "exporter-stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	stderr, err := os.CreateTemp(t.TempDir(), "exporter-stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	if code := run([]string{"exporter", "--help"}, stdout, stderr); code != 0 {
		t.Fatalf("exporter --help exit code = %d", code)
	}
	content, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "127.0.0.1:13610") || !strings.Contains(string(content), "--listen-address") || !strings.Contains(string(content), "--keep-binding") || !strings.Contains(string(content), "--quiet") {
		t.Fatalf("exporter help = %s", content)
	}
}

func TestRunExposesMQTTPublisher(t *testing.T) {
	stdout, err := os.CreateTemp(t.TempDir(), "mqtt-stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	stderr, err := os.CreateTemp(t.TempDir(), "mqtt-stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	if code := run([]string{"mqtt", "--help"}, stdout, stderr); code != 0 {
		t.Fatalf("mqtt --help exit code = %d", code)
	}
	content, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "--broker URL") || !strings.Contains(string(content), "--reconnect-min") || !strings.Contains(string(content), "--tls-ca") || !strings.Contains(string(content), "--quiet") {
		t.Fatalf("mqtt help = %s", content)
	}
	if code := run([]string{"mqtt"}, stdout, stderr); code != 2 {
		t.Fatalf("mqtt without required options exit code = %d", code)
	}
}

func TestCommandUsageDocumentsQuiet(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "usage")
	if err != nil {
		t.Fatal(err)
	}
	printDiscoverUsage(output)
	printDumpUsage(output)
	printGetUsage(output)
	printWatchUsage(output)
	printExporterUsage(output)
	printMQTTUsage(output)
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(content), "--quiet (suppress non-fatal waiting, info, and warning logs)"); count != 6 {
		t.Fatalf("--quiet documented %d times, want 6:\n%s", count, content)
	}
}

func TestQuietDoesNotSuppressErrors(t *testing.T) {
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"get", "--quiet"}, stdout, stderr); code != 2 {
		t.Fatalf("get --quiet exit code = %d, want 2", code)
	}
	if err := stderr.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(stderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != "error: --config is required\n" {
		t.Fatalf("quiet error output = %q", got)
	}
}

func TestCommandFlagValidationAndExitCodes(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{"discover rejects positional arguments", []string{"discover", "extra"}, 2, `unexpected argument "extra"`},
		{"discover rejects zero timeout", []string{"discover", "--discovery-timeout=0s"}, 2, "timeouts must be greater than zero"},
		{"dump requires target", []string{"dump"}, 2, "at least one --target is required"},
		{"dump rejects malformed EOJ", []string{"dump", "--target=192.0.2.10/013001"}, 2, "--target must be an IPv4 address or IPv4/0xGGCCII"},
		{"dump accepts negative batch size", []string{"dump", "--target=192.0.2.10/0x013001", "--batch-size=-1", "--format=invalid"}, 2, "format must be text, json, or metrics-config"},
		{"dump rejects zero batch size", []string{"dump", "--target=192.0.2.10/0x013001", "--batch-size=0"}, 2, "batch size must not be zero"},
		{"get requires config", []string{"get"}, 2, "--config is required"},
		{"get rejects unsupported format", []string{"get", "--config=unused.yaml", "--format=jsonl"}, 2, "--format must be text or json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runForTest(t, tc.args)
			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d; stderr=%q", code, tc.wantCode, stderr)
			}
			if stdout != "" {
				t.Fatalf("stdout contains diagnostic output: %q", stdout)
			}
			if !strings.Contains(stderr, tc.wantStderr) {
				t.Fatalf("stderr = %q, want substring %q", stderr, tc.wantStderr)
			}
		})
	}
}

func TestPeriodicCommandsReportSharedConfigurationErrors(t *testing.T) {
	fixtureCatalog := writeCatalogDirectoryFixture(t, "catalog_format: 1\nclasses: {}\n")
	missingConfig := filepath.Join(t.TempDir(), "missing-instances.yaml")
	invalidCatalog := writeCatalogDirectoryFixture(t, "catalog_format: [\n")
	cases := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{"watch missing config", []string{"watch"}, 2, "--config is required"},
		{"exporter missing config", []string{"exporter"}, 2, "--config is required"},
		{"mqtt missing config and broker", []string{"mqtt"}, 2, "--config and --broker are required"},
		{"watch invalid catalog", []string{"watch", "--config=" + missingConfig, "--catalog-dir=" + invalidCatalog}, 3, "invalid catalog"},
		{"exporter invalid catalog", []string{"exporter", "--config=" + missingConfig, "--catalog-dir=" + invalidCatalog}, 3, "invalid catalog"},
		{"mqtt invalid catalog", []string{"mqtt", "--config=" + missingConfig, "--catalog-dir=" + invalidCatalog, "--broker=mqtt://127.0.0.1:1883"}, 3, "invalid catalog"},
		{"watch invalid metrics config", []string{"watch", "--config=" + missingConfig, "--catalog-dir=" + fixtureCatalog}, 3, "invalid metrics configuration"},
		{"exporter invalid metrics config", []string{"exporter", "--config=" + missingConfig, "--catalog-dir=" + fixtureCatalog}, 3, "invalid metrics configuration"},
		{"mqtt invalid metrics config", []string{"mqtt", "--config=" + missingConfig, "--catalog-dir=" + fixtureCatalog, "--broker=mqtt://127.0.0.1:1883"}, 3, "invalid metrics configuration"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runForTest(t, tc.args)
			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d; stderr=%q", code, tc.wantCode, stderr)
			}
			if stdout != "" {
				t.Fatalf("stdout contains diagnostic output: %q", stdout)
			}
			if !strings.Contains(stderr, tc.wantStderr) {
				t.Fatalf("stderr = %q, want substring %q", stderr, tc.wantStderr)
			}
		})
	}
}

func TestParseEOJCharacterization(t *testing.T) {
	cases := []struct {
		input string
		want  echonet.EOJ
		ok    bool
	}{
		{"0x027D01", echonet.EOJ{0x02, 0x7D, 0x01}, true},
		{"0x000000", echonet.EOJ{}, true},
		{"0xFFFFFF", echonet.EOJ{0xFF, 0xFF, 0xFF}, true},
		{"0x027d01", echonet.EOJ{0x02, 0x7D, 0x01}, true},
		{"027D01", echonet.EOJ{}, false},
		{"0X027D01", echonet.EOJ{}, false},
		{"0x027D0", echonet.EOJ{}, false},
		{"0x027D010", echonet.EOJ{}, false},
		{"0x027G01", echonet.EOJ{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := echonet.ParseEOJ(tc.input)
			if (err == nil) != tc.ok || (tc.ok && got != tc.want) {
				t.Fatalf("parseEOJ(%q) = %s, %v; want %s, ok=%t", tc.input, got, err, tc.want, tc.ok)
			}
		})
	}
}

func TestProfileAssignmentFlagNormalizesEOJ(t *testing.T) {
	var assignments profileAssignments
	if err := assignments.Set("192.0.2.10/0x027d01=sharp_storage_battery_jw_wb2521"); err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 1 || assignments[0].Address.String() != "192.0.2.10" || assignments[0].EOJ.String() != "0x027D01" || assignments[0].ProfileID != "sharp_storage_battery_jw_wb2521" {
		t.Fatalf("assignments = %#v", assignments)
	}
	for _, input := range []string{
		"192.0.2.10/0x027D01=",
		"192.0.2.10=profile",
		"invalid/0x027D01=profile",
		"192.0.2.10/invalid=profile",
	} {
		if err := assignments.Set(input); err == nil {
			t.Fatalf("Set(%q) succeeded", input)
		}
	}
}

func TestNodeResultsJSONOutputShape(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "results.json")
	if err != nil {
		t.Fatal(err)
	}
	results := []model.NodeResult{{
		Address:        "192.0.2.10",
		NodeProfileEOJ: echonet.NodeProfileEOJ,
		Instances: []model.InstanceResult{{
			EOJ:           echonet.EOJ{0x01, 0x30, 0x01},
			GetProperties: []model.PropertyResult{{EPC: "0x80", Status: model.PropertyNotReturned}},
		}},
	}}
	if err := json.NewEncoder(output).Encode(results); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	var decoded []map[string]any
	if err := json.NewDecoder(input).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	instance := decoded[0]["instances"].([]any)[0].(map[string]any)
	property := instance["get_properties"].([]any)[0].(map[string]any)
	if decoded[0]["address"] != "192.0.2.10" || decoded[0]["node_profile_eoj"] != "0x0EF001" || instance["eoj"] != "0x013001" || property["epc"] != "0x80" || property["status"] != "not_returned" || property["value"] != nil {
		t.Fatalf("JSON output = %#v", decoded)
	}
}

func TestStderrLoggerLevelsAndQuietMode(t *testing.T) {
	var output strings.Builder
	logger := newStderrLogger(&output, false, true)
	logger.now = func() time.Time { return time.Date(2026, time.September, 19, 1, 2, 3, 0, time.UTC) }
	logger.Waitf("for %s", "response")
	logger.Infof("collection complete")
	logger.Warnf("property %s unavailable", "0x80")
	logger.Debugf("packet %d", 1)
	want := "2026-09-19T01:02:03Z [waiting] for response\n" +
		"2026-09-19T01:02:03Z [info] collection complete\n" +
		"2026-09-19T01:02:03Z [warning] property 0x80 unavailable\n" +
		"2026-09-19T01:02:03Z [debug] packet 1\n"
	if got := output.String(); got != want {
		t.Fatalf("logger output = %q, want %q", got, want)
	}

	output.Reset()
	quiet := newStderrLogger(&output, true, true)
	quiet.now = logger.now
	quiet.Waitf("for response")
	quiet.Infof("collection complete")
	quiet.Warnf("property unavailable")
	quiet.Debugf("packet %d", 1)
	if got, want := output.String(), "2026-09-19T01:02:03Z [debug] packet 1\n"; got != want {
		t.Fatalf("quiet logger output = %q, want %q", got, want)
	}
}

func TestGetUsageDocumentsShowRaw(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "get-usage")
	if err != nil {
		t.Fatal(err)
	}
	printGetUsage(output)
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "--show-raw") {
		t.Fatalf("get usage = %s", content)
	}
}

func TestPrintDumpInstanceShowsRawOnlyWhenRequested(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "dump-output")
	if err != nil {
		t.Fatal(err)
	}
	raw := "0x4142"
	instance := model.InstanceResult{EOJ: echonet.EOJ{0x01, 0x30, 0x01}, GetProperties: []model.PropertyResult{{EPC: "0x80", RawEDT: &raw, Value: "AB", Status: model.PropertyOK}}}
	printDumpInstance(output, "  -", instance, true, catalog.LocaleEnglish)
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "AB (raw: 0x4142)") {
		t.Fatalf("raw output = %s", content)
	}
}

func TestPrintDumpInstanceUsesSelectedLocale(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "locale-output")
	if err != nil {
		t.Fatal(err)
	}
	japaneseClass, englishClass := "家庭用エアコン", "Home air conditioner"
	japaneseProperty, englishProperty := "動作状態", "Operation status"
	raw := "0x30"
	instance := model.InstanceResult{
		EOJ:         echonet.EOJ{0x01, 0x30, 0x01},
		ClassName:   &japaneseClass,
		ClassNameEN: &englishClass,
		GetProperties: []model.PropertyResult{{
			EPC: "0x80", NameJA: &japaneseProperty, NameEN: &englishProperty, RawEDT: &raw, Value: "On", Status: model.PropertyOK,
		}},
	}
	printDumpInstance(output, "  -", instance, false, catalog.LocaleEnglish)
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, "Home air conditioner") || !strings.Contains(text, "Operation status") {
		t.Fatalf("English localized output = %s", text)
	}
	if strings.Contains(text, japaneseClass) || strings.Contains(text, japaneseProperty) {
		t.Fatalf("English localized output contains Japanese names: %s", text)
	}
}

func TestPrintDumpInstancePrintsPropertyMapsAsHeadings(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "dump-map-output")
	if err != nil {
		t.Fatal(err)
	}
	instance := model.InstanceResult{EOJ: echonet.EOJ{0x01, 0x30, 0x01}, GetProperties: []model.PropertyResult{{EPC: "0x9E", Status: model.PropertyNotReturned}, {EPC: "0x9F", Status: model.PropertyNotReturned}}}
	printDumpInstance(output, "  -", instance, false, catalog.LocaleEnglish)
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "0x9E):\n") || !strings.Contains(string(content), "0x9F):\n") || strings.Contains(string(content), "unavailable") {
		t.Fatalf("property-map output = %s", content)
	}
}

func TestMetricsConfigExcludesNodeProfile(t *testing.T) {
	property := model.PropertyResult{EPC: "0x80", Status: model.PropertyOK}
	result := metricsConfig([]model.NodeResult{{
		Address:     "192.0.2.10",
		NodeProfile: model.InstanceResult{EOJ: echonet.NodeProfileEOJ, GetProperties: []model.PropertyResult{property}},
		Instances:   []model.InstanceResult{{EOJ: echonet.EOJ{0x01, 0x30, 0x01}, GetProperties: []model.PropertyResult{property}}},
	}}, nil)
	if len(result.Instances) != 1 || result.Instances[0].EOJ != "0x013001" {
		t.Fatalf("instances = %#v", result.Instances)
	}
}

func TestMetricsConfigUsesTemplateShape(t *testing.T) {
	shortName := "temperature"
	classShortName := "home_air_conditioner"
	config := metricsConfig([]model.NodeResult{
		{
			Address: "192.0.2.10",
			Instances: []model.InstanceResult{
				{
					EOJ:            echonet.EOJ{0x01, 0x30, 0x01},
					ClassShortName: &classShortName,
					GetProperties: []model.PropertyResult{
						{EPC: "0x80", ShortName: &shortName},
						{EPC: "0x81", Status: model.PropertySkipped},
						{EPC: "0x9D", ShortName: &shortName},
						{EPC: "0x9E"},
						{EPC: "0x9F"},
					},
				},
			},
		},
	}, nil)
	var output strings.Builder
	if err := writeMetricsConfig(&output, config); err != nil {
		t.Fatal(err)
	}
	instance := config.Instances[0]
	if instance.MetricPrefix != "echonet_home_air_conditioner_" || instance.Labels["class"] != "home_air_conditioner" || len(instance.Properties) != 1 || instance.Properties[0].SuggestedMetricName != "temperature" {
		t.Fatalf("config = %#v", instance)
	}
	text := output.String()
	for _, expected := range []string{
		`instances_format: 2`,
		`defaults:`,
		`collection:`,
		`interval_seconds: 60`,
		`batch_size: -1`,
		`metric_prefix: "echonet_home_air_conditioner_"`,
		`class: "home_air_conditioner"`,
		`eoj: "0x013001"`,
		`node_ip: "192.0.2.10"`,
		`- epc: "0x80"`,
		`# metric_name: temperature`,
	} {
		if strings.Contains(text, expected) {
			continue
		}
		t.Fatalf("template does not contain %q:\n%s", expected, text)
	}
	if strings.Contains(text, "0x81") || strings.Contains(text, "0x9D") || strings.Contains(text, "0x9E") || strings.Contains(text, "0x9F") {
		t.Fatalf("template = %s", output.String())
	}
}

func TestMetricsConfigExcludesDisabledProfileProperty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	if err := os.WriteFile(path, []byte(`
catalog_format: 1
profiles:
  test:
    class: "0x0130"
    collection:
      properties:
        "0x80": { enabled: false }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	profiles, err := profile.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	result := metricsConfig([]model.NodeResult{{
		Address: "192.0.2.10",
		Instances: []model.InstanceResult{{
			EOJ:     echonet.EOJ{0x01, 0x30, 0x01},
			Profile: &model.ProfileResult{ID: "test"},
			GetProperties: []model.PropertyResult{
				{EPC: "0x80", Status: model.PropertyOK},
			},
		}},
	}}, profiles)
	if len(result.Instances) != 1 || len(result.Instances[0].Properties) != 0 {
		t.Fatalf("metrics config = %#v", result)
	}
}

func runForTest(t *testing.T, args []string) (int, string, string) {
	return runForDependencies(t, args, defaultCLIDependencies())
}

func runForDependencies(t *testing.T, args []string, dependencies cliDependencies) (int, string, string) {
	t.Helper()
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	code := runWithDependencies(args, stdout, stderr, dependencies)
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderr.Close(); err != nil {
		t.Fatal(err)
	}
	stdoutText, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatal(err)
	}
	stderrText, err := os.ReadFile(stderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	return code, string(stdoutText), string(stderrText)
}
