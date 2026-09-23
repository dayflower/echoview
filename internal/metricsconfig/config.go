// Package metricsconfig loads the format-1 instance collection configuration.
package metricsconfig

import (
	"bytes"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"sort"

	"github.com/dayflower/echoview/internal/collectionpolicy"
	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/profile"
	"gopkg.in/yaml.v3"
)

// Config identifies the object properties collected by the get command.
type Config struct {
	DefaultCollection collectionpolicy.Policy
	Instances         []Instance
}

// Instance is one configured ECHONET Lite object.
type Instance struct {
	Address         netip.Addr
	EOJ             echonet.EOJ
	ProfileID       string
	MetricPrefix    string
	Labels          map[string]string
	IntervalSeconds int
	BatchSize       int
	Properties      []Property
}

// Property is one configured EPC and its optional metric suffix.
type Property struct {
	EPC        byte
	MetricName string
}

type document struct {
	InstancesFormat int            `yaml:"instances_format"`
	Defaults        rawDefaults    `yaml:"defaults"`
	Instances       *[]rawInstance `yaml:"instances"`
}

type rawDefaults struct {
	Collection rawCollectionPolicy `yaml:"collection"`
}

type rawCollectionPolicy struct {
	IntervalSeconds *int `yaml:"interval_seconds"`
	BatchSize       *int `yaml:"batch_size"`
}

type rawInstance struct {
	Address         string            `yaml:"address"`
	EOJ             string            `yaml:"eoj"`
	Profile         string            `yaml:"profile"`
	MetricPrefix    string            `yaml:"metric_prefix"`
	Labels          map[string]string `yaml:"labels"`
	IntervalSeconds *int              `yaml:"interval_seconds"`
	BatchSize       *int              `yaml:"batch_size"`
	Properties      *[]rawProperty    `yaml:"properties"`
}

type rawProperty struct {
	EPC        string `yaml:"epc"`
	MetricName string `yaml:"metric_name"`
}

var labelPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var metricPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Load parses and validates a strict format-2 instance configuration. Profiles
// are checked here because a class mismatch is a startup error.
func Load(path string, profiles *profile.Catalog) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read metrics configuration %q: %w", path, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var source document
	if err := decoder.Decode(&source); err != nil {
		return nil, fmt.Errorf("parse metrics configuration %q: %w", path, err)
	}
	if source.InstancesFormat != 2 || source.Instances == nil {
		return nil, fmt.Errorf("unsupported metrics configuration format in %q", path)
	}
	defaults := collectionpolicy.Policy{IntervalSeconds: source.Defaults.Collection.IntervalSeconds, BatchSize: source.Defaults.Collection.BatchSize}
	if err := defaults.Validate(); err != nil {
		return nil, fmt.Errorf("defaults.collection: %w", err)
	}
	result := &Config{DefaultCollection: defaults, Instances: make([]Instance, 0, len(*source.Instances))}
	seen := map[objectKey]struct{}{}
	for index, raw := range *source.Instances {
		item, err := resolveInstance(raw, defaults, profiles)
		if err != nil {
			return nil, fmt.Errorf("instance %d: %w", index+1, err)
		}
		key := objectKey{address: item.Address, eoj: item.EOJ}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("instance %d: duplicate address/EOJ %s/%s", index+1, item.Address, item.EOJ)
		}
		seen[key] = struct{}{}
		result.Instances = append(result.Instances, item)
	}
	sort.Slice(result.Instances, func(i, j int) bool {
		if result.Instances[i].Address != result.Instances[j].Address {
			return result.Instances[i].Address.Less(result.Instances[j].Address)
		}
		return result.Instances[i].EOJ.String() < result.Instances[j].EOJ.String()
	})
	return result, nil
}

type objectKey struct {
	address netip.Addr
	eoj     echonet.EOJ
}

func resolveInstance(raw rawInstance, defaults collectionpolicy.Policy, profiles *profile.Catalog) (Instance, error) {
	if raw.Address == "" || raw.EOJ == "" || raw.MetricPrefix == "" || raw.Properties == nil {
		return Instance{}, fmt.Errorf("address, eoj, metric_prefix, and properties are required")
	}
	address, err := netip.ParseAddr(raw.Address)
	if err != nil || !address.Is4() {
		return Instance{}, fmt.Errorf("invalid IPv4 address %q", raw.Address)
	}
	eoj, err := echonet.ParseCanonicalEOJ(raw.EOJ)
	if err != nil {
		return Instance{}, err
	}
	if raw.MetricPrefix[len(raw.MetricPrefix)-1] != '_' {
		return Instance{}, fmt.Errorf("metric_prefix must end with _: %q", raw.MetricPrefix)
	}
	for name := range raw.Labels {
		if !labelPattern.MatchString(name) {
			return Instance{}, fmt.Errorf("invalid label name %q", name)
		}
	}
	instancePolicy := collectionpolicy.Policy{IntervalSeconds: raw.IntervalSeconds, BatchSize: raw.BatchSize}
	if err := instancePolicy.Validate(); err != nil {
		return Instance{}, err
	}
	profilePolicy := collectionpolicy.Policy{}
	item := Instance{Address: address.Unmap(), EOJ: eoj, ProfileID: raw.Profile, MetricPrefix: raw.MetricPrefix, Labels: raw.Labels, Properties: make([]Property, 0, len(*raw.Properties))}
	if item.ProfileID != "" {
		if profiles == nil {
			return Instance{}, fmt.Errorf("profile %q requires a profile catalog", item.ProfileID)
		}
		selected, ok := profiles.Get(item.ProfileID)
		if !ok {
			return Instance{}, fmt.Errorf("unknown profile %q", item.ProfileID)
		}
		if !selected.ClassMatches(item.EOJ) {
			return Instance{}, fmt.Errorf("profile %q class does not match %s", item.ProfileID, item.EOJ)
		}
		profilePolicy = selected.Collection
	}
	resolved, err := collectionpolicy.Resolve(defaults, profilePolicy, instancePolicy)
	if err != nil {
		return Instance{}, err
	}
	item.IntervalSeconds, item.BatchSize = resolved.IntervalSeconds, resolved.BatchSize
	seen := map[byte]struct{}{}
	for index, rawProperty := range *raw.Properties {
		epc, err := echonet.ParseCanonicalEPC(rawProperty.EPC)
		if err != nil {
			return Instance{}, fmt.Errorf("property %d: %w", index+1, err)
		}
		if _, exists := seen[epc]; exists {
			return Instance{}, fmt.Errorf("property %d: duplicate EPC %s", index+1, rawProperty.EPC)
		}
		if rawProperty.MetricName != "" && !metricPattern.MatchString(rawProperty.MetricName) {
			return Instance{}, fmt.Errorf("property %d: invalid metric_name", index+1)
		}
		seen[epc] = struct{}{}
		item.Properties = append(item.Properties, Property{EPC: epc, MetricName: rawProperty.MetricName})
	}
	sort.Slice(item.Properties, func(i, j int) bool { return item.Properties[i].EPC < item.Properties[j].EPC })
	return item, nil
}
