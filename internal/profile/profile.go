// Package profile loads explicit device-profile assignments and match rules.
package profile

import (
	"fmt"
	"os"
	"regexp"
	"sort"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/collectionpolicy"
	"github.com/dayflower/echoview/internal/echonet"
	"gopkg.in/yaml.v3"
)

// Profile is a resolved explicit device profile.
type Profile struct {
	ID          string
	Class       echonet.ClassCode
	NameJA      string
	NameEN      string
	Required    map[byte][][]byte
	Optional    map[byte][][]byte
	Properties  map[byte]catalog.Property
	MetricNames map[byte]string
	Prometheus  map[byte]catalog.PrometheusPolicy
	Disabled    map[byte]bool
	Single      map[byte]bool
	Collection  collectionpolicy.Policy
}

// Catalog stores profiles by their stable identifier.
type Catalog struct{ profiles map[string]Profile }

// Get finds one profile by ID.
func (c *Catalog) Get(id string) (Profile, bool) {
	if c == nil {
		return Profile{}, false
	}
	p, ok := c.profiles[id]
	return p, ok
}

type document struct {
	CatalogFormat int                   `yaml:"catalog_format"`
	Profiles      map[string]rawProfile `yaml:"profiles"`
}
type rawProfile struct {
	Class      string                 `yaml:"class"`
	Extends    []string               `yaml:"extends"`
	Name       catalog.Localized      `yaml:"name"`
	Match      rawMatch               `yaml:"match"`
	Properties map[string]rawProperty `yaml:"properties"`
	Collection rawCollection          `yaml:"collection"`
}
type rawMatch struct {
	Required map[string][]string `yaml:"required"`
	Optional map[string][]string `yaml:"optional"`
}
type rawProperty struct {
	Name       catalog.Localized        `yaml:"name"`
	ShortName  string                   `yaml:"short_name"`
	MetricName string                   `yaml:"metric_name"`
	Prometheus catalog.PrometheusPolicy `yaml:"prometheus"`
	Codec      struct {
		Kinds []catalog.Codec `yaml:"kinds"`
	} `yaml:"codec"`
}
type rawCollection struct {
	Properties      map[string]rawPolicy `yaml:"properties"`
	IntervalSeconds *int                 `yaml:"interval_seconds"`
	BatchSize       *int                 `yaml:"batch_size"`
}
type rawPolicy struct {
	Enabled *bool  `yaml:"enabled"`
	Request string `yaml:"request"`
}

// Load reads a format-1 profile catalog and resolves profile inheritance.
// New callers which already parsed a catalog document should use LoadYAML.
func Load(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profiles %q: %w", path, err)
	}
	return LoadYAML(data, path)
}

// LoadYAML resolves the profiles in a format-1 unified catalog document. The
// document may omit profiles, which produces an empty profile catalog.
func LoadYAML(data []byte, sourceName string) (*Catalog, error) {
	var source document
	if err := yaml.Unmarshal(data, &source); err != nil {
		return nil, fmt.Errorf("parse profiles %q: %w", sourceName, err)
	}
	if source.CatalogFormat != 1 {
		return nil, fmt.Errorf("unsupported profile catalog format in %q", sourceName)
	}
	if source.Profiles == nil {
		source.Profiles = map[string]rawProfile{}
	}
	resolved := map[string]Profile{}
	resolving := map[string]bool{}
	var resolve func(string) (Profile, error)
	resolve = func(id string) (Profile, error) {
		if p, ok := resolved[id]; ok {
			return p, nil
		}
		if resolving[id] {
			return Profile{}, fmt.Errorf("cyclic profile inheritance at %q", id)
		}
		raw, ok := source.Profiles[id]
		if !ok {
			return Profile{}, fmt.Errorf("unknown profile %q", id)
		}
		resolving[id] = true
		p := Profile{ID: id, Required: map[byte][][]byte{}, Optional: map[byte][][]byte{}, Properties: map[byte]catalog.Property{}, MetricNames: map[byte]string{}, Prometheus: map[byte]catalog.PrometheusPolicy{}, Disabled: map[byte]bool{}, Single: map[byte]bool{}}
		classSet := false
		for _, parentID := range raw.Extends {
			parent, err := resolve(parentID)
			if err != nil {
				return Profile{}, err
			}
			if !classSet {
				p.Class, classSet = parent.Class, true
			} else if p.Class != parent.Class {
				return Profile{}, fmt.Errorf("profile %q inherits conflicting classes", id)
			}
			mergeProfile(&p, parent)
		}
		if raw.Class != "" {
			class, err := echonet.ParseClassCode(raw.Class)
			if err != nil {
				return Profile{}, fmt.Errorf("profile %q: %w", id, err)
			}
			if classSet && class != p.Class {
				return Profile{}, fmt.Errorf("profile %q has class conflicting with its parent", id)
			}
			p.Class, classSet = class, true
		}
		if !classSet {
			return Profile{}, fmt.Errorf("profile %q has no class", id)
		}
		policy := collectionpolicy.Policy{IntervalSeconds: raw.Collection.IntervalSeconds, BatchSize: raw.Collection.BatchSize}
		if err := policy.Validate(); err != nil {
			return Profile{}, fmt.Errorf("profile %q collection: %w", id, err)
		}
		if policy.IntervalSeconds != nil {
			value := *policy.IntervalSeconds
			p.Collection.IntervalSeconds = &value
		}
		if policy.BatchSize != nil {
			value := *policy.BatchSize
			p.Collection.BatchSize = &value
		}
		if raw.Name.JA != "" {
			p.NameJA = raw.Name.JA
		}
		if raw.Name.EN != "" {
			p.NameEN = raw.Name.EN
		}
		for _, item := range []struct {
			source      map[string][]string
			destination map[byte][][]byte
			label       string
		}{{raw.Match.Required, p.Required, "required"}, {raw.Match.Optional, p.Optional, "optional"}} {
			for key, values := range item.source {
				epc, err := echonet.ParseEPC(key)
				if err != nil {
					return Profile{}, fmt.Errorf("profile %q %s: %w", id, item.label, err)
				}
				decoded, err := parseValues(values)
				if err != nil {
					return Profile{}, fmt.Errorf("profile %q %s %s: %w", id, item.label, key, err)
				}
				item.destination[epc] = decoded
			}
		}
		for epc := range p.Required {
			if _, ok := p.Optional[epc]; ok {
				return Profile{}, fmt.Errorf("profile %q has conflicting match rule for 0x%02X", id, epc)
			}
		}
		for key, definition := range raw.Properties {
			epc, err := echonet.ParseEPC(key)
			if err != nil {
				return Profile{}, fmt.Errorf("profile catalog %q profile %q properties: %w", sourceName, id, err)
			}
			if err := catalog.ValidatePrometheusPolicy(definition.Prometheus); err != nil {
				return Profile{}, fmt.Errorf("profile catalog %q profile %q properties 0x%02X: %w", sourceName, id, epc, err)
			}
			if err := catalog.ValidateCodecs(definition.Codec.Kinds); err != nil {
				return Profile{}, fmt.Errorf("profile catalog %q profile %q properties 0x%02X: %w", sourceName, id, epc, err)
			}
			if definition.Prometheus.Export != "" || definition.Prometheus.MetricType != "" || definition.Prometheus.EnumMap != nil || definition.Prometheus.RawUint != nil {
				if existing, ok := p.Prometheus[epc]; ok {
					p.Prometheus[epc] = catalog.MergePrometheusPolicy(existing, definition.Prometheus)
				} else {
					p.Prometheus[epc] = definition.Prometheus
				}
			}
			if len(definition.Codec.Kinds) > 0 {
				p.Properties[epc] = catalog.Property{EPC: epc, NameJA: definition.Name.JA, NameEN: definition.Name.EN, ShortName: definition.ShortName, Codecs: definition.Codec.Kinds}
			}
			if definition.MetricName != "" {
				if !metricNamePattern.MatchString(definition.MetricName) {
					return Profile{}, fmt.Errorf("profile %q properties 0x%02X: invalid metric_name", id, epc)
				}
				p.MetricNames[epc] = definition.MetricName
			}
		}
		for key, policy := range raw.Collection.Properties {
			epc, err := echonet.ParseEPC(key)
			if err != nil {
				return Profile{}, fmt.Errorf("profile %q collection: %w", id, err)
			}
			if policy.Enabled != nil {
				p.Disabled[epc] = !*policy.Enabled
			}
			if policy.Request != "" && policy.Request != "single" && policy.Request != "batch" {
				return Profile{}, fmt.Errorf("profile %q collection 0x%02X: invalid request", id, epc)
			}
			if policy.Request != "" {
				p.Single[epc] = policy.Request == "single"
			}
		}
		resolving[id] = false
		resolved[id] = p
		return p, nil
	}
	for id := range source.Profiles {
		if _, err := resolve(id); err != nil {
			return nil, err
		}
	}
	return &Catalog{profiles: resolved}, nil
}

func mergeProfile(destination *Profile, source Profile) {
	if destination.NameJA == "" {
		destination.NameJA = source.NameJA
	}
	if destination.NameEN == "" {
		destination.NameEN = source.NameEN
	}
	for epc, values := range source.Required {
		destination.Required[epc] = values
	}
	for epc, values := range source.Optional {
		destination.Optional[epc] = values
	}
	for epc, property := range source.Properties {
		destination.Properties[epc] = property
	}
	for epc, name := range source.MetricNames {
		destination.MetricNames[epc] = name
	}
	for epc, policy := range source.Prometheus {
		if current, ok := destination.Prometheus[epc]; ok {
			destination.Prometheus[epc] = catalog.MergePrometheusPolicy(current, policy)
		} else {
			destination.Prometheus[epc] = policy
		}
	}
	for epc, value := range source.Disabled {
		destination.Disabled[epc] = value
	}
	for epc, value := range source.Single {
		destination.Single[epc] = value
	}
	if source.Collection.IntervalSeconds != nil {
		value := *source.Collection.IntervalSeconds
		destination.Collection.IntervalSeconds = &value
	}
	if source.Collection.BatchSize != nil {
		value := *source.Collection.BatchSize
		destination.Collection.BatchSize = &value
	}
}

var metricNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func parseValues(values []string) ([][]byte, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("missing match values")
	}
	out := make([][]byte, len(values))
	for i, value := range values {
		bytes, err := echonet.ParseEDT(value)
		if err != nil {
			return nil, fmt.Errorf("invalid EDT %q", value)
		}
		out[i] = bytes
	}
	return out, nil
}

// MatchState reports matched, unknown, or mismatched. A required unavailable
// property mismatches; an unavailable optional one makes the match unknown.
func MatchState(p Profile, values map[byte][]byte) (string, string) {
	for _, epc := range sortedEPCs(p.Required) {
		value, ok := values[epc]
		if !ok {
			return "mismatched", fmt.Sprintf("missing required 0x%02X", epc)
		}
		if !matches(value, p.Required[epc]) {
			return "mismatched", fmt.Sprintf("%s was %s", echonet.FormatEPC(epc), echonet.FormatEDT(value))
		}
	}
	unknown := false
	for _, epc := range sortedEPCs(p.Optional) {
		value, ok := values[epc]
		if !ok {
			unknown = true
			continue
		}
		if !matches(value, p.Optional[epc]) {
			return "mismatched", fmt.Sprintf("%s was %s", echonet.FormatEPC(epc), echonet.FormatEDT(value))
		}
	}
	if unknown {
		return "unknown", ""
	}
	return "matched", ""
}
func matches(value []byte, alternatives [][]byte) bool {
	for _, candidate := range alternatives {
		if string(value) == string(candidate) {
			return true
		}
	}
	return false
}
func sortedEPCs(values map[byte][][]byte) []byte {
	result := make([]byte, 0, len(values))
	for epc := range values {
		result = append(result, epc)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// Definition returns a profile override for an EPC, if one exists.
func (p Profile) Definition(epc byte) (catalog.Property, bool) {
	value, ok := p.Properties[epc]
	return value, ok
}

// MetricName returns a profile's metric-name suffix for an EPC.
func (p Profile) MetricName(epc byte) (string, bool) {
	value, ok := p.MetricNames[epc]
	return value, ok
}

// PrometheusPolicy returns a profile policy for an EPC.
func (p Profile) PrometheusPolicy(epc byte) (catalog.PrometheusPolicy, bool) {
	value, ok := p.Prometheus[epc]
	return value, ok
}

// MatchEPCs returns every property needed to validate a profile.
func (p Profile) MatchEPCs() []byte {
	values := map[byte]struct{}{}
	for epc := range p.Required {
		values[epc] = struct{}{}
	}
	for epc := range p.Optional {
		values[epc] = struct{}{}
	}
	result := make([]byte, 0, len(values))
	for epc := range values {
		result = append(result, epc)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// ClassMatches reports whether a profile can apply to the given EOJ class.
func (p Profile) ClassMatches(eoj echonet.EOJ) bool { return p.Class == eoj.ClassCode() }
