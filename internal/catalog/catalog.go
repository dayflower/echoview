// Package catalog loads and resolves the generated ECHONET Lite YAML catalog.
package catalog

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dayflower/echoview/internal/echonet"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/unicode"
	"gopkg.in/yaml.v3"
)

// Catalog is the resolved subset of the generated system catalog needed by
// the CLI.  Parent properties are included in every resolved class.
type Catalog struct {
	classes map[string]Class
}

// Class describes a device or node-profile class.
type Class struct {
	Code       string
	NameJA     string
	NameEN     string
	ShortName  string
	Properties map[byte]Property
}

// Property contains a property name and its ordered codec alternatives.
type Property struct {
	EPC        byte
	NameJA     string
	NameEN     string
	ShortName  string
	Codecs     []Codec
	Prometheus PrometheusPolicy
}

// PrometheusPolicy controls a property's Prometheus representation and type.
type PrometheusPolicy struct {
	Export     string   `yaml:"export"`
	MetricType string   `yaml:"metric_type"`
	EnumMap    *EnumMap `yaml:"enum_map"`
	RawUint    *RawUint `yaml:"raw_uint"`
}

// EnumMap maps complete EDT strings to semantic integer values.
type EnumMap struct {
	Values  map[string]int64 `yaml:"values"`
	Replace bool             `yaml:"replace"`
}

// RawUint optionally constrains the EDT width for raw unsigned export.
type RawUint struct {
	Bytes int `yaml:"bytes"`
}

const maxExactPrometheusInteger = int64(1 << 53)

// Codec is one catalog value encoding alternative.
type Codec struct {
	Kind         string               `yaml:"kind"`
	Bytes        int                  `yaml:"bytes"`
	Size         int                  `yaml:"size"`
	Unit         string               `yaml:"unit"`
	Values       map[string]EnumValue `yaml:"values"`
	ItemSize     int                  `yaml:"itemSize"`
	MinItems     *int                 `yaml:"minItems"`
	MaxItems     *int                 `yaml:"maxItems"`
	Minimum      string               `yaml:"minimum"`
	Maximum      string               `yaml:"maximum"`
	Scale        string               `yaml:"scale"`
	Offset       string               `yaml:"offset"`
	MaximumHour  *int                 `yaml:"maximum_hour"`
	Encoding     string               `yaml:"encoding"`
	MinBytes     *int                 `yaml:"min_bytes"`
	MaxBytes     *int                 `yaml:"max_bytes"`
	BOM          *bool                `yaml:"bom"`
	TrimRight    []string             `yaml:"trim_right"`
	LengthByte   *int                 `yaml:"length_byte"`
	EncodingByte *int                 `yaml:"encoding_byte"`
	ReservedByte *int                 `yaml:"reserved_byte"`
	TextOffset   *int                 `yaml:"text_offset"`
	MaxTextBytes *int                 `yaml:"max_text_bytes"`
	Encodings    map[string]string    `yaml:"encodings"`
	Items        []Codec              `yaml:"-"`
}

// UnmarshalYAML accepts the generated nested codec form ({kinds: [...]}) as
// well as a single item codec. Both forms are supported by the Python tool.
func (c *Codec) UnmarshalYAML(value *yaml.Node) error {
	type plain Codec
	var decoded plain
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	*c = Codec(decoded)
	var items *yaml.Node
	for index := 0; index+1 < len(value.Content); index += 2 {
		if (value.Content[index].Value == "scale" || value.Content[index].Value == "offset") && value.Content[index+1].Tag != "!!str" {
			return fmt.Errorf("%s must be a decimal string", value.Content[index].Value)
		}
		if value.Content[index].Value == "items" {
			items = value.Content[index+1]
			break
		}
	}
	if items == nil {
		return nil
	}
	var nested struct {
		Kinds []Codec `yaml:"kinds"`
	}
	if err := items.Decode(&nested); err != nil {
		return err
	}
	if len(nested.Kinds) > 0 {
		c.Items = nested.Kinds
		return nil
	}
	var item Codec
	if err := items.Decode(&item); err != nil {
		return err
	}
	if item.Kind == "" {
		return errors.New("array items must contain a codec")
	}
	c.Items = []Codec{item}
	return nil
}

// EnumValue is one enum value's catalog representation.
type EnumValue struct {
	Value any       `yaml:"value"`
	Label Localized `yaml:"label"`
}

// Localized stores the Japanese and English labels supplied by the catalog.
type Localized struct {
	JA string `yaml:"ja"`
	EN string `yaml:"en"`
}

// Locale selects the language used for decoded catalog labels.
type Locale string

const (
	LocaleEnglish  Locale = "en"
	LocaleJapanese Locale = "ja"
)

// LocaleFromEnvironment resolves the process locale using the conventional
// POSIX precedence. Unsupported and unset locales use English.
func LocaleFromEnvironment() Locale {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := os.Getenv(name); value != "" {
			return parseLocale(value)
		}
	}
	return LocaleEnglish
}

func parseLocale(value string) Locale {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.SplitN(value, ".", 2)[0]
	value = strings.SplitN(value, "@", 2)[0]
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '_' || r == '-' })
	if len(parts) == 0 {
		return LocaleEnglish
	}
	primary := parts[0]
	if primary == "ja" {
		return LocaleJapanese
	}
	return LocaleEnglish
}

// Value returns the localized text, falling back to the other translation.
func (value Localized) Value(locale Locale) string {
	if locale == LocaleJapanese {
		if value.JA != "" {
			return value.JA
		}
		return value.EN
	}
	if value.EN != "" {
		return value.EN
	}
	return value.JA
}

type document struct {
	CatalogFormat int                 `yaml:"catalog_format"`
	Classes       map[string]rawClass `yaml:"classes"`
}

type rawClass struct {
	Name       Localized              `yaml:"name"`
	ShortName  string                 `yaml:"short_name"`
	Extends    []string               `yaml:"extends"`
	Properties map[string]rawProperty `yaml:"properties"`
}

type rawProperty struct {
	Name       Localized        `yaml:"name"`
	ShortName  string           `yaml:"short_name"`
	Prometheus PrometheusPolicy `yaml:"prometheus"`
	Codec      struct {
		Kinds []Codec `yaml:"kinds"`
	} `yaml:"codec"`
}

// Load reads a format-1 generated catalog from path and resolves inheritance.
// New callers which already parsed a catalog document should use LoadYAML.
func Load(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read catalog %q: %w", path, err)
	}
	return LoadYAML(data, path)
}

// LoadYAML resolves the classes in a format-1 catalog document. The document
// may omit classes, which produces an empty catalog. This allows a unified
// catalog document to contain profiles only.
func LoadYAML(data []byte, sourceName string) (*Catalog, error) {
	var source document
	if err := yaml.Unmarshal(data, &source); err != nil {
		return nil, fmt.Errorf("parse catalog %q: %w", sourceName, err)
	}
	if source.CatalogFormat != 1 {
		return nil, fmt.Errorf("unsupported catalog format in %q", sourceName)
	}
	if source.Classes == nil {
		source.Classes = map[string]rawClass{}
	}
	normalizedClasses := make(map[string]rawClass, len(source.Classes))
	for sourceCode, raw := range source.Classes {
		code, err := echonet.ParseClassCode(sourceCode)
		if err != nil {
			return nil, fmt.Errorf("invalid catalog class %q", sourceCode)
		}
		canonicalCode := code.String()
		if _, exists := normalizedClasses[canonicalCode]; exists {
			return nil, fmt.Errorf("duplicate catalog class %s", canonicalCode)
		}
		for index, parent := range raw.Extends {
			parentCode, err := echonet.ParseClassCode(parent)
			if err != nil {
				return nil, fmt.Errorf("class %s: invalid parent class %q", canonicalCode, parent)
			}
			raw.Extends[index] = parentCode.String()
		}
		normalizedClasses[canonicalCode] = raw
	}
	source.Classes = normalizedClasses
	resolved := make(map[string]Class, len(source.Classes))
	resolving := make(map[string]bool)
	var resolve func(string) (Class, error)
	resolve = func(code string) (Class, error) {
		if class, ok := resolved[code]; ok {
			return class, nil
		}
		if resolving[code] {
			return Class{}, fmt.Errorf("cyclic class inheritance at %s", code)
		}
		raw, ok := source.Classes[code]
		if !ok {
			return Class{}, fmt.Errorf("unknown catalog class %s", code)
		}
		resolving[code] = true
		class := Class{Code: code, NameJA: raw.Name.JA, NameEN: raw.Name.EN, ShortName: raw.ShortName, Properties: map[byte]Property{}}
		for _, parent := range raw.Extends {
			parentClass, err := resolve(parent)
			if err != nil {
				return Class{}, err
			}
			for epc, property := range parentClass.Properties {
				class.Properties[epc] = property
			}
		}
		for epcText, rawProperty := range raw.Properties {
			epc, err := echonet.ParseEPC(epcText)
			if err != nil {
				return Class{}, fmt.Errorf("catalog %q class %s: %w", sourceName, code, err)
			}
			if err := ValidatePrometheusPolicy(rawProperty.Prometheus); err != nil {
				return Class{}, fmt.Errorf("catalog %q class %s property %s: %w", sourceName, code, epcText, err)
			}
			if err := ValidateCodecs(rawProperty.Codec.Kinds); err != nil {
				return Class{}, fmt.Errorf("catalog %q class %s property %s: %w", sourceName, code, epcText, err)
			}
			if rawProperty.Prometheus.Export == "enum_map" && rawProperty.Prometheus.EnumMap == nil {
				return Class{}, fmt.Errorf("catalog %q class %s property %s: enum_map export requires enum_map", sourceName, code, epcText)
			}
			class.Properties[epc] = Property{EPC: epc, NameJA: rawProperty.Name.JA, NameEN: rawProperty.Name.EN, ShortName: rawProperty.ShortName, Codecs: rawProperty.Codec.Kinds, Prometheus: clonePrometheusPolicy(rawProperty.Prometheus)}
		}
		resolving[code] = false
		resolved[code] = class
		return class, nil
	}
	for code := range source.Classes {
		if _, err := resolve(code); err != nil {
			return nil, err
		}
	}
	return &Catalog{classes: resolved}, nil
}

// ClassForEOJ resolves a class from an EOJ. Unknown device classes still
// inherit the device-object superclass; an unknown node profile has no such
// fallback.
func (c *Catalog) ClassForEOJ(eoj echonet.EOJ) (Class, bool) {
	if c == nil {
		return Class{}, false
	}
	code := eoj.ClassCode().String()
	if class, ok := c.classes[code]; ok {
		return class, true
	}
	if code == "0x0EF0" {
		return Class{}, false
	}
	class, ok := c.classes["0x0000"]
	return class, ok
}

// PropertyForEOJ returns the property definition after resolving class
// inheritance and the superclass fallback.
func (c *Catalog) PropertyForEOJ(eoj echonet.EOJ, epc byte) (Property, bool) {
	class, ok := c.ClassForEOJ(eoj)
	if !ok {
		return Property{}, false
	}
	property, ok := class.Properties[epc]
	return property, ok
}

// DecodedValue keeps the raw EDT and optional catalog interpretation.
type DecodedValue struct {
	Raw     string
	Value   any
	Unit    string
	Kind    string
	Decoded bool
}

// DecodeProperty decodes the first matching catalog codec.  The raw EDT is
// always retained when a catalog codec does not match.
func (c *Catalog) DecodeProperty(eoj echonet.EOJ, epc byte, edt []byte, locale Locale) DecodedValue {
	property, ok := c.PropertyForEOJ(eoj, epc)
	if !ok {
		raw := echonet.FormatEDT(edt)
		return DecodedValue{Raw: raw, Value: raw, Kind: "raw"}
	}
	return DecodePropertyDefinition(property, edt, locale)
}

// DecodePropertyDefinition decodes EDT with a resolved property definition.
// It is exported so explicit device profiles can override catalog codecs.
func DecodePropertyDefinition(property Property, edt []byte, locale Locale) DecodedValue {
	raw := echonet.FormatEDT(edt)
	for _, codec := range property.Codecs {
		switch codec.Kind {
		case "raw":
			return DecodedValue{Raw: raw, Value: raw, Kind: "raw"}
		case "enum":
			if codec.Size > 0 && len(edt) != codec.Size {
				continue
			}
			entry, found := codec.Values[raw]
			if !found {
				continue
			}
			value := entry.Label.Value(locale)
			if value == "" && entry.Value != nil {
				value = fmt.Sprint(entry.Value)
			}
			if value != "" {
				return DecodedValue{Raw: raw, Value: value, Kind: "enum", Decoded: true}
			}
		case "uint", "int":
			if codec.Bytes <= 0 || len(edt) != codec.Bytes || !arrayIntegerInRange(codec, edt) {
				continue
			}
			text, ok := decodeIntegerText(codec, edt)
			if !ok {
				continue
			}
			return DecodedValue{Raw: raw, Value: text, Unit: codec.Unit, Kind: codec.Kind, Decoded: true}
		case "text":
			if value, ok := decodeText(codec, edt); ok {
				return DecodedValue{Raw: raw, Value: value, Kind: "text", Decoded: true}
			}
		case "encoded_text":
			if value, ok := decodeEncodedText(codec, edt); ok {
				return DecodedValue{Raw: raw, Value: value, Kind: "encoded_text", Decoded: true}
			}
		case "time":
			if value, ok := decodeTime(codec, edt); ok {
				return DecodedValue{Raw: raw, Value: value, Kind: "time", Decoded: true}
			}
		case "date":
			if value, ok := decodeDate(codec, edt); ok {
				return DecodedValue{Raw: raw, Value: value, Kind: "date", Decoded: true}
			}
		case "date_time":
			if value, ok := decodeDateTime(codec, edt); ok {
				return DecodedValue{Raw: raw, Value: value, Kind: "date_time", Decoded: true}
			}
		case "array":
			if value, unit, ok := decodeArray(codec, edt, locale); ok {
				return DecodedValue{Raw: raw, Value: value, Unit: unit, Kind: "array", Decoded: true}
			}
		}
	}
	return DecodedValue{Raw: raw, Value: raw, Kind: "raw"}
}

func decodeText(codec Codec, edt []byte) (string, bool) {
	if codec.Bytes > 0 && len(edt) != codec.Bytes {
		return "", false
	}
	if codec.MinBytes != nil && len(edt) < *codec.MinBytes {
		return "", false
	}
	if codec.MaxBytes != nil && len(edt) > *codec.MaxBytes {
		return "", false
	}
	value, ok := decodeTextBytes(codec.Encoding, edt)
	if !ok {
		return "", false
	}
	if codec.BOM != nil && !*codec.BOM && strings.HasPrefix(value, "\ufeff") {
		return "", false
	}
	for _, suffix := range codec.TrimRight {
		switch suffix {
		case "nul":
			value = strings.TrimRight(value, "\x00")
		case "space":
			value = strings.TrimRight(value, " ")
		default:
			return "", false
		}
	}
	return value, true
}

func decodeEncodedText(codec Codec, edt []byte) (string, bool) {
	if codec.LengthByte == nil || codec.EncodingByte == nil || codec.ReservedByte == nil || codec.TextOffset == nil {
		return "", false
	}
	lengthByte, encodingByte, reservedByte, textOffset := *codec.LengthByte, *codec.EncodingByte, *codec.ReservedByte, *codec.TextOffset
	if lengthByte < 0 || encodingByte < 0 || reservedByte < 0 || textOffset < 0 || textOffset <= lengthByte || textOffset <= encodingByte || textOffset <= reservedByte {
		return "", false
	}
	if lengthByte >= len(edt) || encodingByte >= len(edt) || reservedByte >= len(edt) || edt[reservedByte] != 0 {
		return "", false
	}
	textLength := int(edt[lengthByte])
	if codec.MaxTextBytes != nil && textLength > *codec.MaxTextBytes {
		return "", false
	}
	if len(edt) != textOffset+textLength {
		return "", false
	}
	encoding, ok := codec.Encodings[echonet.FormatEPC(edt[encodingByte])]
	if !ok {
		return "", false
	}
	return decodeTextBytes(encoding, edt[textOffset:])
}

func decodeTextBytes(encoding string, value []byte) (string, bool) {
	switch strings.ToLower(strings.ReplaceAll(encoding, "_", "-")) {
	case "", "us-ascii", "ascii":
		for _, octet := range value {
			if octet > 0x7F {
				return "", false
			}
		}
		return string(value), true
	case "utf-8", "utf8":
		if !utf8.Valid(value) {
			return "", false
		}
		return string(value), true
	case "shift-jis", "shiftjis":
		decoded, err := japanese.ShiftJIS.NewDecoder().Bytes(value)
		return string(decoded), err == nil
	case "iso-2022-jp", "iso2022-jp":
		decoded, err := japanese.ISO2022JP.NewDecoder().Bytes(value)
		return string(decoded), err == nil
	case "euc-jp", "eucjp":
		decoded, err := japanese.EUCJP.NewDecoder().Bytes(value)
		return string(decoded), err == nil
	case "ucs-2", "utf-16be":
		decoded, err := unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder().Bytes(value)
		return string(decoded), err == nil
	case "ucs-4", "utf-32be":
		return decodeUCS4(value)
	case "latin-1", "iso-8859-1":
		var result strings.Builder
		result.Grow(len(value) * 2)
		for _, octet := range value {
			result.WriteRune(rune(octet))
		}
		return result.String(), true
	default:
		return "", false
	}
}

func decodeUCS4(value []byte) (string, bool) {
	if len(value)%4 != 0 {
		return "", false
	}
	var result strings.Builder
	for offset := 0; offset < len(value); offset += 4 {
		runeValue := rune(value[offset])<<24 | rune(value[offset+1])<<16 | rune(value[offset+2])<<8 | rune(value[offset+3])
		if !utf8.ValidRune(runeValue) {
			return "", false
		}
		result.WriteRune(runeValue)
	}
	return result.String(), true
}

// ValidatePrometheusPolicy validates the supported property export policy.
func ValidatePrometheusPolicy(policy PrometheusPolicy) error {
	if policy.Export != "" && policy.Export != "enum_map" && policy.Export != "raw_uint" {
		return fmt.Errorf("invalid Prometheus export %q", policy.Export)
	}
	if policy.MetricType != "" && policy.MetricType != "gauge" && policy.MetricType != "counter" {
		return fmt.Errorf("invalid Prometheus metric_type %q", policy.MetricType)
	}
	if policy.EnumMap != nil {
		for raw, value := range policy.EnumMap.Values {
			if _, err := rawEDTBytes(raw); err != nil {
				return fmt.Errorf("invalid enum map EDT %q", raw)
			}
			if value == -1 {
				return fmt.Errorf("enum map value -1 is reserved for unknown EDT")
			}
			if value < -maxExactPrometheusInteger || value > maxExactPrometheusInteger {
				return fmt.Errorf("enum map value %d is not exactly representable by Prometheus", value)
			}
		}
	}
	if policy.RawUint != nil && (policy.RawUint.Bytes < 1 || policy.RawUint.Bytes > 6) {
		return fmt.Errorf("raw_uint bytes must be between 1 and 6 when specified")
	}
	if policy.Export == "raw_uint" && policy.RawUint == nil {
		return fmt.Errorf("raw_uint export requires raw_uint")
	}
	return nil
}

func clonePrometheusPolicy(source PrometheusPolicy) PrometheusPolicy {
	result := source
	if source.EnumMap != nil {
		item := *source.EnumMap
		item.Values = make(map[string]int64, len(source.EnumMap.Values))
		for key, value := range source.EnumMap.Values {
			item.Values[key] = value
		}
		result.EnumMap = &item
	}
	if source.RawUint != nil {
		item := *source.RawUint
		result.RawUint = &item
	}
	return result
}

// MergePrometheusPolicy overlays a profile policy on a catalog policy.
func MergePrometheusPolicy(base, overlay PrometheusPolicy) PrometheusPolicy {
	result := clonePrometheusPolicy(base)
	if overlay.Export != "" {
		result.Export = overlay.Export
	}
	if overlay.MetricType != "" {
		result.MetricType = overlay.MetricType
	}
	if overlay.RawUint != nil {
		item := *overlay.RawUint
		result.RawUint = &item
	}
	if overlay.EnumMap != nil {
		if overlay.EnumMap.Replace || result.EnumMap == nil {
			result.EnumMap = &EnumMap{Values: map[string]int64{}}
		}
		if result.EnumMap.Values == nil {
			result.EnumMap.Values = map[string]int64{}
		}
		for key, value := range overlay.EnumMap.Values {
			result.EnumMap.Values[key] = value
		}
	}
	return result
}

func rawEDTBytes(value string) ([]byte, error) {
	result, err := echonet.ParseEDT(value)
	if err != nil {
		return nil, errors.New("invalid raw EDT")
	}
	return result, nil
}

func decodeTime(codec Codec, edt []byte) (string, bool) {
	if (codec.Bytes != 2 && codec.Bytes != 3) || len(edt) != codec.Bytes {
		return "", false
	}
	maximumHour := 23
	if codec.MaximumHour != nil {
		maximumHour = *codec.MaximumHour
	}
	if maximumHour < 0 || maximumHour > 255 {
		return "", false
	}
	hour, minute := int(edt[0]), int(edt[1])
	if hour > maximumHour || minute > 59 {
		return "", false
	}
	if codec.Bytes == 2 {
		return fmt.Sprintf("%02d:%02d", hour, minute), true
	}
	second := int(edt[2])
	if second > 59 {
		return "", false
	}
	return fmt.Sprintf("%02d:%02d:%02d", hour, minute, second), true
}

func decodeDate(codec Codec, edt []byte) (string, bool) {
	if codec.Bytes != 4 || len(edt) != 4 {
		return "", false
	}
	year := int(edt[0])<<8 | int(edt[1])
	if !validDateTime(year, int(edt[2]), int(edt[3]), 0, 0, 0) {
		return "", false
	}
	return fmt.Sprintf("%04d-%02d-%02d", year, edt[2], edt[3]), true
}

func decodeDateTime(codec Codec, edt []byte) (string, bool) {
	if (codec.Bytes != 6 && codec.Bytes != 7) || len(edt) != codec.Bytes {
		return "", false
	}
	year := int(edt[0])<<8 | int(edt[1])
	second := 0
	if codec.Bytes == 7 {
		second = int(edt[6])
	}
	if !validDateTime(year, int(edt[2]), int(edt[3]), int(edt[4]), int(edt[5]), second) {
		return "", false
	}
	if codec.Bytes == 6 {
		return fmt.Sprintf("%04d-%02d-%02dT%02d:%02d", year, edt[2], edt[3], edt[4], edt[5]), true
	}
	return fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d", year, edt[2], edt[3], edt[4], edt[5], edt[6]), true
}

func validDateTime(year, month, day, hour, minute, second int) bool {
	if year < 1 || year > 9999 || month < 1 || month > 12 || day < 1 || hour > 23 || minute > 59 || second > 59 {
		return false
	}
	value := time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC)
	return value.Year() == year && int(value.Month()) == month && value.Day() == day
}

func decodeArray(codec Codec, edt []byte, locale Locale) ([]any, string, bool) {
	if codec.ItemSize <= 0 || len(codec.Items) == 0 || len(edt)%codec.ItemSize != 0 {
		return nil, "", false
	}
	count := len(edt) / codec.ItemSize
	if codec.MinItems != nil && count < *codec.MinItems || codec.MaxItems != nil && count > *codec.MaxItems {
		return nil, "", false
	}
	values := make([]any, 0, count)
	units := map[string]struct{}{}
	for offset := 0; offset < len(edt); offset += codec.ItemSize {
		value, itemUnit, ok := decodeArrayItem(codec.Items, edt[offset:offset+codec.ItemSize], locale)
		if !ok {
			return nil, "", false
		}
		if itemUnit != "" {
			units[itemUnit] = struct{}{}
		}
		values = append(values, value)
	}
	if len(units) == 1 {
		for unit := range units {
			return values, unit, true
		}
	}
	return values, "", true
}

func decodeArrayItem(codecs []Codec, edt []byte, locale Locale) (any, string, bool) {
	for _, codec := range codecs {
		switch codec.Kind {
		case "enum":
			if codec.Size > 0 && len(edt) != codec.Size {
				continue
			}
			raw := echonet.FormatEDT(edt)
			entry, ok := codec.Values[raw]
			if !ok {
				continue
			}
			if entry.Value != nil {
				return entry.Value, "", true
			}
			if label := entry.Label.Value(locale); label != "" {
				return label, "", true
			}
		case "uint", "int":
			if codec.Bytes <= 0 || len(edt) != codec.Bytes || !arrayIntegerInRange(codec, edt) {
				continue
			}
			if codec.Scale != "" || codec.Offset != "" {
				value, ok := decodeIntegerText(codec, edt)
				if !ok {
					continue
				}
				return value, codec.Unit, true
			}
			value := uint64(0)
			for _, octet := range edt {
				value = value<<8 | uint64(octet)
			}
			if codec.Kind == "int" && edt[0]&0x80 != 0 {
				if codec.Bytes == 8 {
					return int64(value), codec.Unit, true
				}
				return int64(value - (uint64(1) << uint(codec.Bytes*8))), codec.Unit, true
			}
			return value, codec.Unit, true
		case "text":
			if codec.Bytes > 0 && len(edt) != codec.Bytes {
				continue
			}
			return string(edt), "", true
		case "raw":
			return echonet.FormatEDT(edt), "", true
		case "array":
			if value, unit, ok := decodeArray(codec, edt, locale); ok {
				return value, unit, true
			}
		}
	}
	return nil, "", false
}

var decimalPattern = regexp.MustCompile(`^[+-]?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)

var knownCodecKinds = map[string]struct{}{
	"array":         {},
	"bitmap":        {},
	"date":          {},
	"date_time":     {},
	"encoded_text":  {},
	"enum":          {},
	"instance_list": {},
	"int":           {},
	"property_map":  {},
	"raw":           {},
	"text":          {},
	"time":          {},
	"uint":          {},
}

// ValidateCodecs checks codec kinds and decimal transformation settings,
// including nested array codecs. Values are represented as strings so they
// remain exact in YAML.
func ValidateCodecs(codecs []Codec) error {
	return validateCodecs(codecs, "codec.kinds")
}

func validateCodecs(codecs []Codec, path string) error {
	for index, codec := range codecs {
		codecPath := fmt.Sprintf("%s[%d]", path, index)
		if _, ok := knownCodecKinds[codec.Kind]; !ok {
			return fmt.Errorf("%s: unknown codec kind %q", codecPath, codec.Kind)
		}
		if codec.Scale != "" || codec.Offset != "" {
			if codec.Kind != "uint" && codec.Kind != "int" {
				return fmt.Errorf("%s: scale and offset require an int or uint codec", codecPath)
			}
			for _, setting := range []struct {
				name  string
				value string
			}{{"scale", codec.Scale}, {"offset", codec.Offset}} {
				if setting.value != "" && !decimalPattern.MatchString(setting.value) {
					return fmt.Errorf("%s.%s must be a decimal string", codecPath, setting.name)
				}
			}
		}
		if codec.Kind == "array" {
			if err := validateCodecs(codec.Items, codecPath+".items"); err != nil {
				return err
			}
		}
	}
	return nil
}

// decodeIntegerText returns the integer after applying its optional exact
// decimal transformation: raw * scale + offset.
func decodeIntegerText(codec Codec, edt []byte) (string, bool) {
	value := uint64(0)
	for _, octet := range edt {
		value = value<<8 | uint64(octet)
	}
	text := strconv.FormatUint(value, 10)
	if codec.Kind == "int" && edt[0]&0x80 != 0 {
		if codec.Bytes == 8 {
			text = strconv.FormatInt(int64(value), 10)
		} else {
			bits := uint(codec.Bytes * 8)
			text = strconv.FormatInt(int64(value-(uint64(1)<<bits)), 10)
		}
	}
	if codec.Scale == "" && codec.Offset == "" {
		return text, true
	}
	raw, ok := new(big.Rat).SetString(text)
	if !ok {
		return "", false
	}
	scale := big.NewRat(1, 1)
	if codec.Scale != "" {
		if scale, ok = new(big.Rat).SetString(codec.Scale); !ok {
			return "", false
		}
	}
	result := new(big.Rat).Mul(raw, scale)
	if codec.Offset != "" {
		offset, ok := new(big.Rat).SetString(codec.Offset)
		if !ok {
			return "", false
		}
		result.Add(result, offset)
	}
	return formatDecimal(result), true
}

func formatDecimal(value *big.Rat) string {
	denominator := new(big.Int).Set(value.Denom())
	twos, fives := 0, 0
	for new(big.Int).Mod(denominator, big.NewInt(2)).Sign() == 0 {
		denominator.Div(denominator, big.NewInt(2))
		twos++
	}
	for new(big.Int).Mod(denominator, big.NewInt(5)).Sign() == 0 {
		denominator.Div(denominator, big.NewInt(5))
		fives++
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return value.RatString()
	}
	places := max(twos, fives)
	text := value.FloatString(places)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	if text == "" || text == "-0" {
		return "0"
	}
	return text
}

func arrayIntegerInRange(codec Codec, edt []byte) bool {
	value := uint64(0)
	for _, octet := range edt {
		value = value<<8 | uint64(octet)
	}
	if codec.Kind == "int" && edt[0]&0x80 != 0 {
		if codec.Bytes == 8 {
			return integerInRange(strconv.FormatInt(int64(value), 10), codec)
		}
		value -= uint64(1) << uint(codec.Bytes*8)
		return integerInRange(strconv.FormatInt(int64(value), 10), codec)
	}
	return integerInRange(strconv.FormatUint(value, 10), codec)
}

func integerInRange(value string, codec Codec) bool {
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return false
	}
	if codec.Minimum != "" {
		minimum, ok := new(big.Int).SetString(codec.Minimum, 10)
		if !ok || parsed.Cmp(minimum) < 0 {
			return false
		}
	}
	if codec.Maximum != "" {
		maximum, ok := new(big.Int).SetString(codec.Maximum, 10)
		if !ok || parsed.Cmp(maximum) > 0 {
			return false
		}
	}
	return true
}
