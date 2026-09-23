package main

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/model"
	"github.com/dayflower/echoview/internal/profile"
)

type metricsConfigDocument struct {
	InstancesFormat int                     `yaml:"instances_format"`
	Defaults        metricsConfigDefaults   `yaml:"defaults"`
	Instances       []metricsConfigInstance `yaml:"instances"`
}

type metricsConfigDefaults struct {
	Collection metricsConfigCollectionDefaults
}

type metricsConfigCollectionDefaults struct {
	IntervalSeconds int
	BatchSize       int
}
type metricsConfigInstance struct {
	Address      string `yaml:"address"`
	EOJ          string `yaml:"eoj"`
	Profile      string `yaml:"profile,omitempty"`
	MetricPrefix string `yaml:"metric_prefix"`
	Labels       map[string]string
	Properties   []metricsConfigProperty
}
type metricsConfigProperty struct{ EPC, SuggestedMetricName string }

func metricsConfig(nodes []model.NodeResult, profiles *profile.Catalog) metricsConfigDocument {
	result := metricsConfigDocument{InstancesFormat: 2, Defaults: metricsConfigDefaults{Collection: metricsConfigCollectionDefaults{IntervalSeconds: 60, BatchSize: -1}}}
	for _, node := range nodes {
		for _, instance := range node.Instances {
			if instance.EOJ == (echonet.EOJ{}) || len(instance.GetProperties) == 0 {
				continue
			}
			class := instance.EOJ.ClassCode().String()
			prefix := "echonet_" + strings.ToLower(strings.TrimPrefix(class, "0x")) + "_"
			if instance.ClassShortName != nil {
				class = *instance.ClassShortName
				prefix = "echonet_" + class + "_"
			}
			item := metricsConfigInstance{Address: node.Address, EOJ: instance.EOJ.String(), MetricPrefix: prefix, Labels: map[string]string{"node_ip": node.Address, "eoj": instance.EOJ.String(), "class": class}}
			if instance.Profile != nil {
				item.Profile = instance.Profile.ID
			}
			for _, property := range instance.GetProperties {
				if property.EPC == "0x9D" || property.EPC == "0x9E" || property.EPC == "0x9F" || property.Status == model.PropertySkipped || profilePropertyDisabled(property, instance.Profile, profiles) {
					continue
				}
				item.Properties = append(item.Properties, metricsConfigProperty{EPC: property.EPC, SuggestedMetricName: suggestedMetricName(property, instance.Profile, profiles)})
			}
			result.Instances = append(result.Instances, item)
		}
	}
	return result
}

func profilePropertyDisabled(property model.PropertyResult, applied *model.ProfileResult, profiles *profile.Catalog) bool {
	if applied == nil || profiles == nil {
		return false
	}
	epc, err := echonet.ParseEPC(property.EPC)
	if err != nil {
		return false
	}
	selected, ok := profiles.Get(applied.ID)
	return ok && selected.Disabled[epc]
}
func suggestedMetricName(property model.PropertyResult, applied *model.ProfileResult, profiles *profile.Catalog) string {
	if applied != nil && profiles != nil {
		if epc, err := echonet.ParseEPC(property.EPC); err == nil {
			if selected, ok := profiles.Get(applied.ID); ok {
				if name, ok := selected.MetricName(epc); ok {
					return name
				}
			}
		}
	}
	if property.ShortName != nil {
		return *property.ShortName
	}
	return ""
}

func writeMetricsConfig(output io.Writer, document metricsConfigDocument) error {
	var text strings.Builder
	fmt.Fprintf(&text, "instances_format: %d\ndefaults:\n  collection:\n    interval_seconds: %d\n    batch_size: %d\ninstances:\n", document.InstancesFormat, document.Defaults.Collection.IntervalSeconds, document.Defaults.Collection.BatchSize)
	for _, instance := range document.Instances {
		fmt.Fprintf(&text, "  - address: %s\n    eoj: %s\n", yamlString(instance.Address), yamlString(instance.EOJ))
		if instance.Profile != "" {
			fmt.Fprintf(&text, "    profile: %s\n", yamlString(instance.Profile))
		}
		fmt.Fprintf(&text, "    metric_prefix: %s\n    labels:\n", yamlString(instance.MetricPrefix))
		labelNames := make([]string, 0, len(instance.Labels))
		for name := range instance.Labels {
			labelNames = append(labelNames, name)
		}
		sort.Strings(labelNames)
		for _, name := range labelNames {
			fmt.Fprintf(&text, "      %s: %s\n", name, yamlString(instance.Labels[name]))
		}
		if len(instance.Properties) == 0 {
			fmt.Fprintln(&text, "    properties: []")
			continue
		}
		fmt.Fprintln(&text, "    properties:")
		for _, property := range instance.Properties {
			fmt.Fprintf(&text, "      - epc: %s\n", yamlString(property.EPC))
			if property.SuggestedMetricName != "" {
				fmt.Fprintf(&text, "        # metric_name: %s\n", property.SuggestedMetricName)
			}
		}
	}
	_, err := fmt.Fprint(output, text.String())
	return err
}

func yamlString(value string) string { return strconv.Quote(value) }
