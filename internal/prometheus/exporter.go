// Package prometheus exposes scheduler snapshots in Prometheus text format.
package prometheus

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/metricsconfig"
	"github.com/dayflower/echoview/internal/model"
	"github.com/dayflower/echoview/internal/profile"
	"github.com/dayflower/echoview/internal/scheduler"
)

const (
	metricsPath = "/metrics"
	healthPath  = "/healthz"
)

// Config supplies the immutable metadata needed to translate a collection
// snapshot into metric names.
type Config struct {
	Snapshots scheduler.SnapshotReader
	Instances []metricsconfig.Instance
	Profiles  *profile.Catalog
	Catalog   *catalog.Catalog
	Now       func() time.Time
}

// Exporter is an HTTP handler that reads snapshots without performing device
// communication.
type Exporter struct {
	snapshots scheduler.SnapshotReader
	instances map[string]metricsconfig.Instance
	profiles  *profile.Catalog
	catalog   *catalog.Catalog
	now       func() time.Time
}

// New validates the snapshot source and constructs an exporter.
func New(config Config) (*Exporter, error) {
	if config.Snapshots == nil {
		return nil, fmt.Errorf("prometheus exporter has no snapshot reader")
	}
	instances := make(map[string]metricsconfig.Instance, len(config.Instances))
	for _, instance := range config.Instances {
		key := instanceKey(instance.Address.String(), instance.EOJ.String())
		if _, exists := instances[key]; exists {
			return nil, fmt.Errorf("duplicate exporter instance %s", key)
		}
		instances[key] = instance
		if _, exists := instance.Labels["item_index"]; exists && hasArrayProperty(instance, config.Profiles, config.Catalog) {
			return nil, fmt.Errorf("exporter instance %s uses reserved label item_index with an array property", key)
		}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	exporter := &Exporter{snapshots: config.Snapshots, instances: instances, profiles: config.Profiles, catalog: config.Catalog, now: now}
	if err := exporter.validateConfiguredMetricTypes(config.Instances); err != nil {
		return nil, err
	}
	return exporter, nil
}

// ServeHTTP implements /metrics and /healthz. It does not interact with
// ECHONET Lite sockets, even while a scheduler updates its snapshot.
func (e *Exporter) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case metricsPath:
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if request.Method == http.MethodHead {
			return
		}
		if err := e.WriteMetrics(writer); err != nil {
			// A client disconnect is not an exporter failure worth logging here.
			return
		}
	case healthPath:
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		if request.Method != http.MethodHead {
			_, _ = io.WriteString(writer, "ok\n")
		}
	default:
		http.NotFound(writer, request)
	}
}

// WriteMetrics writes one consistent snapshot in Prometheus' text exposition
// format. Callers may use it directly in tests or custom HTTP servers.
func (e *Exporter) WriteMetrics(writer io.Writer) error {
	snapshot := e.snapshots.Snapshot()
	now := e.now()
	samples := make(map[string][]sample)
	for _, item := range snapshot.Instances {
		instance, exists := e.instances[instanceKey(item.Address, item.EOJ.String())]
		if !exists {
			continue
		}
		labels := cloneLabels(instance.Labels)
		samples["echonet_lite_collection_success"] = append(samples["echonet_lite_collection_success"], sample{labels: labels, value: boolValue(item.Completed && item.Succeeded), metricType: "gauge"})
		if !item.LastSucceededAt.IsZero() {
			lastSuccess := item.LastSucceededAt.UnixNano()
			samples["echonet_lite_collection_last_success_timestamp_seconds"] = append(samples["echonet_lite_collection_last_success_timestamp_seconds"], sample{labels: labels, value: float64(lastSuccess) / float64(time.Second), metricType: "gauge"})
			age := now.Sub(item.LastSucceededAt).Seconds()
			if age < 0 {
				age = 0
			}
			samples["echonet_lite_collection_age_seconds"] = append(samples["echonet_lite_collection_age_seconds"], sample{labels: labels, value: age, metricType: "gauge"})
		}
		if !item.Completed || !item.Succeeded {
			continue
		}
		for _, property := range item.Result.GetProperties {
			if property.Status != model.PropertyOK {
				continue
			}
			name := e.metricName(instance, property)
			if name == "" {
				continue
			}
			policy := e.policy(instance, property)
			metricType := prometheusMetricType(policy)
			switch property.ValueKind {
			case "uint", "int":
				if value, ok := numericValue(property.Value); ok {
					samples[name] = append(samples[name], sample{labels: labels, value: value, metricType: metricType})
				}
			case "enum":
				if policy.Export == "enum_map" {
					samples[name] = append(samples[name], sample{labels: labels, value: enumValue(policy, property.RawEDT), metricType: metricType})
				} else if policy.Export == "raw_uint" {
					if value, ok := rawUintValue(policy, property.RawEDT); ok {
						samples[name] = append(samples[name], sample{labels: labels, value: value, metricType: metricType})
					}
				}
			case "raw":
				if policy.Export == "enum_map" {
					samples[name] = append(samples[name], sample{labels: labels, value: enumValue(policy, property.RawEDT), metricType: metricType})
				} else if policy.Export == "raw_uint" {
					if value, ok := rawUintValue(policy, property.RawEDT); ok {
						samples[name] = append(samples[name], sample{labels: labels, value: value, metricType: metricType})
					}
				}
			case "array":
				if values, ok := numericArray(property.Value); ok {
					for index, value := range values {
						itemLabels := cloneLabels(labels)
						itemLabels["item_index"] = strconv.Itoa(index)
						samples[name] = append(samples[name], sample{labels: itemLabels, value: value, metricType: metricType})
					}
				}
			}
		}
	}
	return writeSamples(writer, samples)
}

type sample struct {
	labels     map[string]string
	value      float64
	metricType string
}

func writeSamples(writer io.Writer, samples map[string][]sample) error {
	names := make([]string, 0, len(samples))
	for name := range samples {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		items := samples[name]
		metricType := items[0].metricType
		for _, item := range items[1:] {
			if item.metricType != metricType {
				return fmt.Errorf("metric %s has conflicting types %q and %q", name, metricType, item.metricType)
			}
		}
		if _, err := fmt.Fprintf(writer, "# TYPE %s %s\n", name, metricType); err != nil {
			return err
		}
		sort.SliceStable(items, func(i, j int) bool { return labelText(items[i].labels) < labelText(items[j].labels) })
		for _, item := range items {
			if _, err := fmt.Fprintf(writer, "%s%s %s\n", name, labelText(item.labels), formatFloat(item.value)); err != nil {
				return err
			}
		}
	}
	return nil
}

func prometheusMetricType(policy catalog.PrometheusPolicy) string {
	if policy.MetricType == "counter" {
		return "counter"
	}
	return "gauge"
}

func (e *Exporter) validateConfiguredMetricTypes(instances []metricsconfig.Instance) error {
	types := map[string]string{}
	for _, instance := range instances {
		for _, configured := range instance.Properties {
			property := model.PropertyResult{EPC: echonet.FormatEPC(configured.EPC)}
			name := e.metricName(instance, property)
			metricType := prometheusMetricType(e.policy(instance, property))
			if existing, found := types[name]; found && existing != metricType {
				return fmt.Errorf("metric %s has conflicting configured types %q and %q", name, existing, metricType)
			}
			types[name] = metricType
		}
	}
	return nil
}

func (e *Exporter) metricName(instance metricsconfig.Instance, property model.PropertyResult) string {
	epc, err := echonet.ParseEPC(property.EPC)
	if err != nil {
		return ""
	}
	metricName := ""
	for _, configured := range instance.Properties {
		if configured.EPC == epc {
			metricName = configured.MetricName
			break
		}
	}
	if metricName == "" && instance.ProfileID != "" && e.profiles != nil {
		if selected, ok := e.profiles.Get(instance.ProfileID); ok {
			metricName, _ = selected.MetricName(epc)
			if metricName == "" {
				if definition, found := selected.Definition(epc); found {
					metricName = definition.ShortName
				}
			}
		}
	}
	if metricName == "" && e.catalog != nil {
		if definition, ok := e.catalog.PropertyForEOJ(instance.EOJ, epc); ok {
			metricName = definition.ShortName
		}
	}
	if metricName == "" {
		metricName = fmt.Sprintf("epc_0x%02x", epc)
	}
	return instance.MetricPrefix + metricName
}

func (e *Exporter) policy(instance metricsconfig.Instance, property model.PropertyResult) catalog.PrometheusPolicy {
	epc, err := echonet.ParseEPC(property.EPC)
	if err != nil {
		return catalog.PrometheusPolicy{}
	}
	base := catalog.PrometheusPolicy{}
	if definition, ok := e.catalog.PropertyForEOJ(instance.EOJ, epc); ok {
		base = definition.Prometheus
	}
	if instance.ProfileID != "" && e.profiles != nil {
		if selected, ok := e.profiles.Get(instance.ProfileID); ok {
			if overlay, ok := selected.PrometheusPolicy(epc); ok {
				base = catalog.MergePrometheusPolicy(base, overlay)
			}
		}
	}
	return base
}

func hasArrayProperty(instance metricsconfig.Instance, profiles *profile.Catalog, loaded *catalog.Catalog) bool {
	for _, configured := range instance.Properties {
		var definition catalog.Property
		found := false
		if instance.ProfileID != "" && profiles != nil {
			if selected, ok := profiles.Get(instance.ProfileID); ok {
				definition, found = selected.Definition(configured.EPC)
			}
		}
		if !found && loaded != nil {
			definition, found = loaded.PropertyForEOJ(instance.EOJ, configured.EPC)
		}
		if found {
			for _, codec := range definition.Codecs {
				if codec.Kind == "array" {
					return true
				}
			}
		}
	}
	return false
}

func enumValue(policy catalog.PrometheusPolicy, raw *string) float64 {
	if raw == nil || policy.EnumMap == nil {
		return -1
	}
	if value, ok := policy.EnumMap.Values[*raw]; ok {
		return float64(value)
	}
	return -1
}

func rawUintValue(policy catalog.PrometheusPolicy, raw *string) (float64, bool) {
	if raw == nil || policy.RawUint == nil {
		return 0, false
	}
	bytes, err := echonet.ParseEDT(*raw)
	if err != nil || len(bytes) > 6 || (policy.RawUint.Bytes > 0 && len(bytes) != policy.RawUint.Bytes) {
		return 0, false
	}
	value := uint64(0)
	for _, octet := range bytes {
		value = value<<8 | uint64(octet)
	}
	return float64(value), true
}

func numericArray(value any) ([]float64, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]float64, len(items))
	for i, item := range items {
		number, ok := numericValue(item)
		if !ok {
			return nil, false
		}
		result[i] = number
	}
	return result, true
}

func numericValue(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case int:
		number = float64(typed)
	case int8:
		number = float64(typed)
	case int16:
		number = float64(typed)
	case int32:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case uint:
		number = float64(typed)
	case uint8:
		number = float64(typed)
	case uint16:
		number = float64(typed)
	case uint32:
		number = float64(typed)
	case uint64:
		number = float64(typed)
	case float32:
		number = float64(typed)
	case float64:
		number = typed
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

func cloneLabels(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for name, value := range source {
		result[name] = value
	}
	return result
}

func labelText(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	names := make([]string, 0, len(labels))
	for name := range labels {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = name + `="` + escapeLabel(labels[name]) + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escapeLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func formatFloat(value float64) string { return strconv.FormatFloat(value, 'g', -1, 64) }
func boolValue(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
func instanceKey(address, eoj string) string { return address + "/" + eoj }
