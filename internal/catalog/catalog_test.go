package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dayflower/echoview/internal/echonet"
	"gopkg.in/yaml.v3"
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

func TestLocaleFromEnvironment(t *testing.T) {
	tests := []struct {
		name       string
		lcAll      string
		lcMessages string
		lang       string
		want       Locale
	}{
		{"English language", "", "", "en_US.UTF-8", LocaleEnglish},
		{"Japanese language", "", "", "ja_JP.UTF-8", LocaleJapanese},
		{"LC_ALL takes precedence", "en_GB.UTF-8", "ja_JP.UTF-8", "ja_JP.UTF-8", LocaleEnglish},
		{"LC_MESSAGES takes precedence over LANG", "", "ja-JP", "en_US.UTF-8", LocaleJapanese},
		{"unsupported language uses English", "", "", "fr_FR.UTF-8", LocaleEnglish},
		{"unset uses English", "", "", "", LocaleEnglish},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("LC_ALL", test.lcAll)
			t.Setenv("LC_MESSAGES", test.lcMessages)
			t.Setenv("LANG", test.lang)
			if got := LocaleFromEnvironment(); got != test.want {
				t.Fatalf("LocaleFromEnvironment() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDecodePropertyDefinitionUsesLocaleForEnumLabels(t *testing.T) {
	property := Property{Codecs: []Codec{{
		Kind: "enum",
		Size: 1,
		Values: map[string]EnumValue{
			"0x41": {Label: Localized{EN: "Fault occurred.", JA: "異常あり"}},
		},
	}}}
	if got := DecodePropertyDefinition(property, []byte{0x41}, LocaleEnglish).Value; got != "Fault occurred." {
		t.Fatalf("English enum value = %#v", got)
	}
	if got := DecodePropertyDefinition(property, []byte{0x41}, LocaleJapanese).Value; got != "異常あり" {
		t.Fatalf("Japanese enum value = %#v", got)
	}

	englishOnly := Property{Codecs: []Codec{{Kind: "enum", Size: 1, Values: map[string]EnumValue{"0x42": {Label: Localized{EN: "English only"}}}}}}
	if got := DecodePropertyDefinition(englishOnly, []byte{0x42}, LocaleJapanese).Value; got != "English only" {
		t.Fatalf("Japanese fallback value = %#v", got)
	}
}

func TestValidateCodecsAcceptsKnownKinds(t *testing.T) {
	kinds := []string{
		"enum", "uint", "int", "array", "raw", "time", "date", "date_time",
		"text", "encoded_text", "bitmap", "property_map", "instance_list",
	}
	codecs := make([]Codec, 0, len(kinds))
	for _, kind := range kinds {
		codecs = append(codecs, Codec{Kind: kind})
	}
	if err := ValidateCodecs(codecs); err != nil {
		t.Fatalf("ValidateCodecs() = %v", err)
	}
}

func TestCatalogRejectsUnknownCodecKindWithLocation(t *testing.T) {
	_, err := LoadYAML([]byte(`
catalog_format: 1
classes:
  "0x0130":
    properties:
      "0x80":
        codec: {kinds: [{kind: unknown_kind}]}
`), "class-fixture.yaml")
	if err == nil {
		t.Fatal("LoadYAML() succeeded")
	}
	for _, expected := range []string{
		`catalog "class-fixture.yaml"`, "class 0x0130", "property 0x80",
		`codec.kinds[0]: unknown codec kind "unknown_kind"`,
	} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error %q does not contain %q", err, expected)
		}
	}
}

func TestCatalogRejectsUnknownNestedCodecKind(t *testing.T) {
	_, err := LoadYAML([]byte(`
catalog_format: 1
classes:
  "0x0130":
    properties:
      "0x80":
        codec:
          kinds:
            - kind: array
              itemSize: 1
              items: {kinds: [{kind: unknown_item_kind}]}
`), "nested-class-fixture.yaml")
	if err == nil {
		t.Fatal("LoadYAML() succeeded")
	}
	if !strings.Contains(err.Error(), `codec.kinds[0].items[0]: unknown codec kind "unknown_item_kind"`) {
		t.Fatalf("error = %q", err)
	}
}

func TestCatalogIgnoresUnknownCodecFields(t *testing.T) {
	loaded, err := LoadYAML([]byte(`
catalog_format: 1
classes:
  "0x0130":
    ignored_class_field: ignored
    properties:
      "0x80":
        ignored_property_field: ignored
        codec:
          kinds:
            - kind: raw
              ignored_codec_field: ignored
`), "unknown-fields.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.PropertyForEOJ(echonet.EOJ{0x01, 0x30, 0x01}, 0x80); !ok {
		t.Fatal("property was not loaded")
	}
}

func TestCatalogLoadsAndResolvesInheritedProperties(t *testing.T) {
	loaded := loadCatalogFixture(t, `
catalog_format: 1
classes:
  "0x0000":
    name: {ja: superclass}
    short_name: superclass
    properties:
      "0x80":
        name: {ja: operation_status}
        short_name: operation_status
        codec:
          kinds:
            - kind: enum
              size: 1
              values:
                "0x30": {label: {en: On, ja: ON}}
  "0x0130":
    name: {ja: test device}
    short_name: test_device
    extends: ["0x0000"]
`)
	class, ok := loaded.ClassForEOJ(echonet.EOJ{0x01, 0x30, 0x01})
	if !ok || class.NameJA != "test device" {
		t.Fatalf("class = %#v, found = %t", class, ok)
	}
	property, ok := loaded.PropertyForEOJ(echonet.EOJ{0x01, 0x30, 0x01}, 0x80)
	if !ok || property.NameJA != "operation_status" {
		t.Fatalf("property = %#v, found = %t", property, ok)
	}
	decoded := loaded.DecodeProperty(echonet.EOJ{0x01, 0x30, 0x01}, 0x80, []byte{0x30}, LocaleEnglish)
	if !decoded.Decoded || decoded.Value != "On" || decoded.Raw != "0x30" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestDecodeArrayCodec(t *testing.T) {
	decoded := DecodePropertyDefinition(Property{Codecs: []Codec{{
		Kind: "array", ItemSize: 1, Items: []Codec{
			{Kind: "uint", Bytes: 1, Maximum: "254", Unit: "%"},
			{Kind: "enum", Size: 1, Values: map[string]EnumValue{"0xFF": {Label: Localized{EN: "unknown"}}}},
		},
	}}}, []byte{0x64, 0xFF}, LocaleEnglish)
	if !decoded.Decoded {
		t.Fatalf("decoded = %#v", decoded)
	}
	if want := []any{uint64(100), "unknown"}; !reflect.DeepEqual(decoded.Value, want) {
		t.Fatalf("value = %#v, want %#v", decoded.Value, want)
	}
	if decoded.Unit != "%" {
		t.Fatalf("unit = %q, want %%", decoded.Unit)
	}
}

func TestDecodeSignedAndEnumAlternatives(t *testing.T) {
	property := Property{Codecs: []Codec{
		{Kind: "int", Bytes: 1, Maximum: "125", Unit: "Celsius"},
		{Kind: "enum", Size: 1, Values: map[string]EnumValue{"0x7E": {Value: "unmeasurable", Label: Localized{EN: "Unmeasurable"}}}},
	}}
	for _, test := range []struct {
		name  string
		edt   []byte
		value string
		unit  string
	}{
		{"signed temperature", []byte{0xFE}, "-2", "Celsius"},
		{"unmeasurable enum", []byte{0x7E}, "Unmeasurable", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoded := DecodePropertyDefinition(property, test.edt, LocaleEnglish)
			if !decoded.Decoded || decoded.Value != test.value || decoded.Unit != test.unit {
				t.Fatalf("decoded = %#v", decoded)
			}
		})
	}

	decoded := DecodePropertyDefinition(Property{Codecs: []Codec{{Kind: "unrecognized"}}}, []byte{0x42}, LocaleEnglish)
	if decoded.Decoded || decoded.Kind != "raw" || decoded.Value != "0x42" {
		t.Fatalf("unknown codec decoded = %#v", decoded)
	}
}

func TestDecodeIntegerAppliesScaleAndOffset(t *testing.T) {
	for _, test := range []struct {
		name     string
		codec    Codec
		edt      []byte
		want     string
		wantUnit string
	}{
		{"unsigned scale", Codec{Kind: "uint", Bytes: 2, Scale: "0.1", Unit: "V"}, []byte{0, 123}, "12.3", "V"},
		{"signed scale and offset", Codec{Kind: "int", Bytes: 1, Scale: "0.1", Offset: "1.5", Unit: "Celsius"}, []byte{0xFE}, "1.3", "Celsius"},
		{"integer result retains trailing-zero-free form", Codec{Kind: "uint", Bytes: 2, Scale: "0.1"}, []byte{0x03, 0xE8}, "100", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoded := DecodePropertyDefinition(Property{Codecs: []Codec{test.codec}}, test.edt, LocaleEnglish)
			if !decoded.Decoded || decoded.Value != test.want || decoded.Unit != test.wantUnit {
				t.Fatalf("decoded = %#v, want value %q and unit %q", decoded, test.want, test.wantUnit)
			}
		})
	}

	decoded := DecodePropertyDefinition(Property{Codecs: []Codec{{
		Kind: "array", ItemSize: 2, Items: []Codec{{Kind: "uint", Bytes: 2, Scale: "0.1", Unit: "V"}},
	}}}, []byte{0, 100, 0, 123}, LocaleEnglish)
	if !decoded.Decoded || !reflect.DeepEqual(decoded.Value, []any{"10", "12.3"}) || decoded.Unit != "V" {
		t.Fatalf("scaled array = %#v", decoded)
	}
}

func TestValidateCodecsRejectsInvalidDecimalTransform(t *testing.T) {
	for _, codecs := range [][]Codec{
		{{Kind: "uint", Scale: "one tenth"}},
		{{Kind: "array", Scale: "0.1"}},
		{{Kind: "array", Items: []Codec{{Kind: "int", Offset: "1/2"}}}},
	} {
		if err := ValidateCodecs(codecs); err == nil {
			t.Fatalf("ValidateCodecs(%#v) succeeded", codecs)
		}
	}
}

func TestDecodeTemporalCodecs(t *testing.T) {
	for _, test := range []struct {
		name  string
		codec Codec
		edt   []byte
		value string
	}{
		{"current time", Codec{Kind: "time", Bytes: 2}, []byte{0x0D, 0x2D}, "13:45"},
		{"current time with seconds", Codec{Kind: "time", Bytes: 3}, []byte{0x0D, 0x2D, 0x3B}, "13:45:59"},
		{"relative timer time", Codec{Kind: "time", Bytes: 2, MaximumHour: pointerTo(255)}, []byte{0xFF, 0x3B}, "255:59"},
		{"current date", Codec{Kind: "date", Bytes: 4}, []byte{0x07, 0xEA, 0x09, 0x0F}, "2026-09-15"},
		{"date and time with minutes", Codec{Kind: "date_time", Bytes: 6}, []byte{0x07, 0xEA, 0x09, 0x0F, 0x0D, 0x2D}, "2026-09-15T13:45"},
		{"date and time with seconds", Codec{Kind: "date_time", Bytes: 7}, []byte{0x07, 0xEA, 0x09, 0x0F, 0x0D, 0x2D, 0x3B}, "2026-09-15T13:45:59"},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoded := DecodePropertyDefinition(Property{Codecs: []Codec{test.codec}}, test.edt, LocaleEnglish)
			if !decoded.Decoded || decoded.Value != test.value {
				t.Fatalf("decoded = %#v, want value %q", decoded, test.value)
			}
		})
	}
}

func TestDecodeTextCodecs(t *testing.T) {
	encodedText := Codec{
		Kind: "encoded_text", MinBytes: pointerTo(3), LengthByte: pointerTo(0), EncodingByte: pointerTo(1), ReservedByte: pointerTo(2), TextOffset: pointerTo(3), MaxTextBytes: pointerTo(64),
		Encodings: map[string]string{"0x01": "us-ascii", "0x02": "shift_jis"},
	}
	for _, test := range []struct {
		name  string
		codec Codec
		edt   []byte
		value string
	}{
		{"fixed ASCII with NUL padding", Codec{Kind: "text", Bytes: 12, Encoding: "us-ascii", TrimRight: []string{"nul"}}, []byte("MODEL-1\x00\x00\x00\x00\x00"), "MODEL-1"},
		{"UTF-8", Codec{Kind: "text", Encoding: "utf-8"}, []byte("給湯器"), "給湯器"},
		{"encoded ASCII", encodedText, []byte{2, 1, 0, 'O', 'K'}, "OK"},
		{"encoded Shift JIS", encodedText, []byte{2, 2, 0, 0x82, 0xA0}, "あ"},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoded := DecodePropertyDefinition(Property{Codecs: []Codec{test.codec}}, test.edt, LocaleEnglish)
			if !decoded.Decoded || decoded.Value != test.value {
				t.Fatalf("decoded = %#v, want value %q", decoded, test.value)
			}
		})
	}

	for _, test := range []struct {
		name string
		edt  []byte
	}{
		{"invalid length", []byte{3, 1, 0, 'O', 'K'}},
		{"non-zero reserved byte", []byte{2, 1, 1, 'O', 'K'}},
		{"unknown encoding", []byte{2, 0xFF, 0, 'O', 'K'}},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoded := DecodePropertyDefinition(Property{Codecs: []Codec{encodedText}}, test.edt, LocaleEnglish)
			if decoded.Decoded || decoded.Kind != "raw" || decoded.Value != echonet.FormatEDT(test.edt) {
				t.Fatalf("decoded = %#v", decoded)
			}
		})
	}
}

func TestDecodeTextBytesSupportsEveryCatalogEncoding(t *testing.T) {
	for _, test := range []struct {
		encoding string
		input    []byte
		want     string
	}{
		{"us-ascii", []byte("OK"), "OK"},
		{"shift_jis", []byte{0x82, 0xA0}, "あ"},
		{"iso-2022-jp", []byte{0x1B, 0x24, 0x42, 0x24, 0x22, 0x1B, 0x28, 0x42}, "あ"},
		{"euc_jp", []byte{0xA4, 0xA2}, "あ"},
		{"ucs-4", []byte{0, 0, 0x30, 0x42}, "あ"},
		{"ucs-2", []byte{0x30, 0x42}, "あ"},
		{"latin-1", []byte{0xE9}, "é"},
		{"utf-8", []byte("給湯器"), "給湯器"},
	} {
		t.Run(test.encoding, func(t *testing.T) {
			got, ok := decodeTextBytes(test.encoding, test.input)
			if !ok || got != test.want {
				t.Fatalf("decodeTextBytes(%q, % X) = %q, %t; want %q, true", test.encoding, test.input, got, ok, test.want)
			}
		})
	}
}

func TestTemporalCodecsRejectInvalidValues(t *testing.T) {
	maximumHour := 255
	for _, test := range []struct {
		name  string
		codec Codec
		edt   []byte
	}{
		{"time beyond default maximum hour", Codec{Kind: "time", Bytes: 2}, []byte{24, 0}},
		{"time invalid minute", Codec{Kind: "time", Bytes: 2, MaximumHour: &maximumHour}, []byte{255, 60}},
		{"time invalid maximum hour", Codec{Kind: "time", Bytes: 2, MaximumHour: pointerTo(256)}, []byte{23, 0}},
		{"date invalid leap day", Codec{Kind: "date", Bytes: 4}, []byte{0x07, 0xE9, 2, 29}},
		{"date invalid year", Codec{Kind: "date", Bytes: 4}, []byte{0, 0, 1, 1}},
		{"date and time invalid hour", Codec{Kind: "date_time", Bytes: 6}, []byte{0x07, 0xEA, 9, 15, 24, 0}},
		{"date and time invalid second", Codec{Kind: "date_time", Bytes: 7}, []byte{0x07, 0xEA, 9, 15, 13, 45, 60}},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoded := DecodePropertyDefinition(Property{Codecs: []Codec{test.codec}}, test.edt, LocaleEnglish)
			if decoded.Decoded {
				t.Fatalf("decoded = %#v, want undecoded", decoded)
			}
		})
	}
}

func pointerTo(value int) *int {
	return &value
}

func TestDecodeArrayCodecSupportsDirectItemCodec(t *testing.T) {
	var codecs []Codec
	if err := yaml.Unmarshal([]byte(`
- kind: array
  itemSize: 2
  minItems: 2
  maxItems: 2
  items:
    kind: uint
    bytes: 2
    unit: W
`), &codecs); err != nil {
		t.Fatal(err)
	}
	decoded := DecodePropertyDefinition(Property{Codecs: codecs}, []byte{0, 1, 0, 2}, LocaleEnglish)
	if !decoded.Decoded {
		t.Fatalf("decoded = %#v", decoded)
	}
	if want := []any{uint64(1), uint64(2)}; !reflect.DeepEqual(decoded.Value, want) {
		t.Fatalf("value = %#v, want %#v", decoded.Value, want)
	}
	if decoded.Unit != "W" {
		t.Fatalf("unit = %q, want W", decoded.Unit)
	}
}

func TestDecodeOneOfPreservesSelectedKindAndNumericBounds(t *testing.T) {
	property := Property{Codecs: []Codec{
		{Kind: "uint", Bytes: 1, Minimum: "0", Maximum: "100"},
		{Kind: "enum", Size: 1, Values: map[string]EnumValue{"0xFF": {Value: "unavailable", Label: Localized{EN: "unavailable"}}}},
	}}
	for _, test := range []struct {
		edt  []byte
		kind string
	}{
		{edt: []byte{100}, kind: "uint"},
		{edt: []byte{0xFF}, kind: "enum"},
	} {
		decoded := DecodePropertyDefinition(property, test.edt, LocaleEnglish)
		if !decoded.Decoded || decoded.Kind != test.kind {
			t.Fatalf("DecodePropertyDefinition(%X) = %#v, want kind %q", test.edt, decoded, test.kind)
		}
	}
}

func TestPrometheusPolicyRejectsReservedAndInexactValues(t *testing.T) {
	for _, policy := range []PrometheusPolicy{
		{Export: "enum_map", EnumMap: &EnumMap{Values: map[string]int64{"0x30": -1}}},
		{Export: "enum_map", EnumMap: &EnumMap{Values: map[string]int64{"0x30": maxExactPrometheusInteger + 1}}},
		{Export: "raw_uint", RawUint: &RawUint{Bytes: 7}},
		{MetricType: "summary"},
	} {
		if err := ValidatePrometheusPolicy(policy); err == nil {
			t.Fatalf("ValidatePrometheusPolicy(%#v) succeeded", policy)
		}
	}
}

func TestUnknownDeviceClassFallsBackToSuperclass(t *testing.T) {
	loaded := &Catalog{classes: map[string]Class{
		"0x0000": {Code: "0x0000", Properties: map[byte]Property{0x81: {EPC: 0x81, NameJA: "installation_location"}}},
	}}
	property, ok := loaded.PropertyForEOJ(echonet.EOJ{0x7F, 0xFF, 0x01}, 0x81)
	if !ok || property.NameJA != "installation_location" {
		t.Fatalf("property = %#v, found = %t", property, ok)
	}
}

func loadCatalogFixture(t *testing.T, source string) *Catalog {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}
