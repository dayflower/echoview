// Package get implements one configured ECHONET Lite collection pass.
package get

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"time"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/metricsconfig"
	"github.com/dayflower/echoview/internal/model"
	"github.com/dayflower/echoview/internal/profile"
	"github.com/dayflower/echoview/internal/propertyread"
)

// Config controls one collection pass.
type Config struct {
	DataTimeout  time.Duration
	DataAttempts int
	InitialTID   uint16
	Instances    []metricsconfig.Instance
	Profiles     *profile.Catalog
	Catalog      *catalog.Catalog
	Locale       catalog.Locale
	Debugf       func(string, ...any)
	Waitf        func(string, ...any)
}

// ProfileMismatchError reports that an explicitly configured profile does not
// identify the target instance. It permanently disables a periodic target.
type ProfileMismatchError struct {
	Address   netip.Addr
	EOJ       echonet.EOJ
	ProfileID string
	Detail    string
}

func (e *ProfileMismatchError) Error() string {
	return fmt.Sprintf("profile %q did not match %s/%s: %s", e.ProfileID, e.Address, e.EOJ, e.Detail)
}

// DisableTarget marks this error as a permanent per-instance collection
// failure for schedulers that support disabling targets.
func (e *ProfileMismatchError) DisableTarget() bool { return true }

// Run reads exactly the configured properties and returns grouped node results.
func Run(ctx context.Context, conn echonet.PacketConn, config Config) ([]model.NodeResult, error) {
	if conn == nil {
		return nil, errors.New("ECHONET Lite get has no packet connection")
	}
	if config.DataTimeout <= 0 || config.DataAttempts <= 0 {
		return nil, errors.New("data timeout and attempts must be greater than zero")
	}
	if len(config.Instances) == 0 {
		return []model.NodeResult{}, nil
	}
	instances := append([]metricsconfig.Instance(nil), config.Instances...)
	sort.Slice(instances, func(i, j int) bool {
		if instances[i].Address != instances[j].Address {
			return instances[i].Address.Less(instances[j].Address)
		}
		return instances[i].EOJ.String() < instances[j].EOJ.String()
	})
	byAddress := map[netip.Addr]*model.NodeResult{}
	orderedAddresses := []netip.Addr{}
	tid := config.InitialTID
	for _, selected := range instances {
		if _, exists := byAddress[selected.Address]; !exists {
			result := &model.NodeResult{Address: selected.Address.String(), NodeProfileEOJ: echonet.NodeProfileEOJ, NodeProfile: model.InstanceResult{EOJ: echonet.NodeProfileEOJ}}
			byAddress[selected.Address] = result
			orderedAddresses = append(orderedAddresses, selected.Address)
		}
		applied, err := selectedProfile(selected, config.Profiles)
		if err != nil {
			return nil, err
		}
		matchState, nextTID, err := verifyProfile(ctx, conn, selected, tid, applied, config)
		if err != nil {
			return nil, err
		}
		instance, nextTID := readInstance(ctx, conn, selected, nextTID, applied, matchState, config)
		tid = nextTID
		byAddress[selected.Address].Instances = append(byAddress[selected.Address].Instances, instance)
	}
	results := make([]model.NodeResult, 0, len(orderedAddresses))
	for _, address := range orderedAddresses {
		results = append(results, *byAddress[address])
	}
	return results, nil
}

func selectedProfile(instance metricsconfig.Instance, profiles *profile.Catalog) (profile.Profile, error) {
	if instance.ProfileID == "" {
		return profile.Profile{}, nil
	}
	if profiles == nil {
		return profile.Profile{}, fmt.Errorf("profile %q requires a profile catalog", instance.ProfileID)
	}
	selected, ok := profiles.Get(instance.ProfileID)
	if !ok {
		return profile.Profile{}, fmt.Errorf("unknown profile %q", instance.ProfileID)
	}
	if !selected.ClassMatches(instance.EOJ) {
		return profile.Profile{}, fmt.Errorf("profile %q class does not match %s", instance.ProfileID, instance.EOJ)
	}
	return selected, nil
}

func verifyProfile(ctx context.Context, conn echonet.PacketConn, selected metricsconfig.Instance, tid uint16, applied profile.Profile, config Config) (string, uint16, error) {
	if applied.ID == "" {
		return "", tid, nil
	}
	matchEPCs := applied.MatchEPCs()
	if len(matchEPCs) == 0 {
		return "matched", tid, nil
	}
	values, nextTID := propertyread.ReadBatch(ctx, conn, selected.Address, selected.EOJ, matchEPCs, tid, propertyReadConfig(config))
	observed := make(map[byte][]byte, len(values))
	for epc, value := range values {
		if value.EDT != nil {
			observed[epc] = value.EDT
		}
	}
	state, detail := profile.MatchState(applied, observed)
	if state == "mismatched" {
		return state, nextTID, &ProfileMismatchError{Address: selected.Address, EOJ: selected.EOJ, ProfileID: applied.ID, Detail: detail}
	}
	debugf(config, "profile %s matched %s/%s: %s", applied.ID, selected.Address, selected.EOJ, state)
	return state, nextTID, nil
}

func readInstance(ctx context.Context, conn echonet.PacketConn, selected metricsconfig.Instance, tid uint16, applied profile.Profile, matchState string, config Config) (model.InstanceResult, uint16) {
	values := make(map[byte]propertyread.Property, len(selected.Properties))
	regular := make([]byte, 0, len(selected.Properties))
	singles := make([]byte, 0)
	for _, property := range selected.Properties {
		if applied.Disabled[property.EPC] {
			values[property.EPC] = propertyread.Property{Status: model.PropertySkipped}
			continue
		}
		if applied.Single[property.EPC] {
			singles = append(singles, property.EPC)
		} else {
			regular = append(regular, property.EPC)
		}
	}
	for _, batch := range batches(regular, selected.BatchSize) {
		got, nextTID := propertyread.ReadBatch(ctx, conn, selected.Address, selected.EOJ, batch, tid, propertyReadConfig(config))
		tid = nextTID
		for epc, value := range got {
			values[epc] = value
		}
	}
	for _, epc := range singles {
		got, nextTID := propertyread.ReadBatch(ctx, conn, selected.Address, selected.EOJ, []byte{epc}, tid, propertyReadConfig(config))
		tid = nextTID
		values[epc] = got[epc]
	}
	return makeInstance(selected, values, applied, matchState, config.Catalog, config.Locale), tid
}

func batches(epcs []byte, batchSize int) [][]byte {
	if len(epcs) == 0 {
		return nil
	}
	if batchSize <= 0 {
		return [][]byte{epcs}
	}
	result := make([][]byte, 0, (len(epcs)+batchSize-1)/batchSize)
	for len(epcs) > 0 {
		size := batchSize
		if size > len(epcs) {
			size = len(epcs)
		}
		result = append(result, epcs[:size])
		epcs = epcs[size:]
	}
	return result
}

func propertyReadConfig(config Config) propertyread.Config {
	return propertyread.Config{Timeout: config.DataTimeout, Attempts: config.DataAttempts, Debugf: config.Debugf, Waitf: config.Waitf}
}

func makeInstance(selected metricsconfig.Instance, values map[byte]propertyread.Property, applied profile.Profile, matchState string, loaded *catalog.Catalog, locale catalog.Locale) model.InstanceResult {
	result := model.InstanceResult{EOJ: selected.EOJ}
	if loaded != nil {
		if class, ok := loaded.ClassForEOJ(selected.EOJ); ok {
			result.ClassName, result.ClassNameEN, result.ClassShortName = pointer(class.NameJA), pointer(class.NameEN), pointer(class.ShortName)
		}
	}
	if applied.ID != "" {
		item := model.ProfileResult{ID: applied.ID, MatchState: matchState}
		item.NameJA, item.NameEN = pointer(applied.NameJA), pointer(applied.NameEN)
		result.Profile = &item
	}
	for _, configured := range selected.Properties {
		property := model.PropertyResult{EPC: echonet.FormatEPC(configured.EPC), Status: model.PropertyNotReturned}
		definition, known := definitionFor(selected.EOJ, configured.EPC, applied, loaded)
		if known {
			property.NameJA, property.NameEN, property.ShortName = pointer(definition.NameJA), pointer(definition.NameEN), pointer(definition.ShortName)
		}
		raw, exists := values[configured.EPC]
		if exists {
			property.Status = raw.Status
			if raw.EDT != nil {
				rawText := echonet.FormatEDT(raw.EDT)
				property.RawEDT = &rawText
				if known {
					decoded := catalog.DecodePropertyDefinition(definition, raw.EDT, locale)
					if decoded.Decoded || hasRawCodec(definition) || hasPrometheusExportPolicy(definition, applied, configured.EPC) {
						property.Value = decoded.Value
						property.Unit = pointer(decoded.Unit)
						property.ValueKind = decoded.Kind
					} else if raw.Status == model.PropertyOK {
						property.Status = model.PropertyDecodeError
					}
				} else {
					property.Value = rawText
				}
			}
		}
		result.GetProperties = append(result.GetProperties, property)
	}
	return result
}

func definitionFor(eoj echonet.EOJ, epc byte, applied profile.Profile, loaded *catalog.Catalog) (catalog.Property, bool) {
	if definition, ok := applied.Definition(epc); ok {
		return definition, true
	}
	if loaded == nil {
		return catalog.Property{}, false
	}
	return loaded.PropertyForEOJ(eoj, epc)
}

func hasRawCodec(definition catalog.Property) bool {
	for _, codec := range definition.Codecs {
		if codec.Kind == "raw" {
			return true
		}
	}
	return false
}

func hasPrometheusExportPolicy(definition catalog.Property, applied profile.Profile, epc byte) bool {
	if definition.Prometheus.Export != "" {
		return true
	}
	policy, ok := applied.PrometheusPolicy(epc)
	return ok && policy.Export != ""
}

func pointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func debugf(config Config, format string, values ...any) {
	if config.Debugf != nil {
		config.Debugf(format, values...)
	}
}
