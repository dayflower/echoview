// Package dump implements the ECHONET Lite full-state dump workflow.
package dump

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"time"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/collectionpolicy"
	"github.com/dayflower/echoview/internal/discover"
	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/model"
	"github.com/dayflower/echoview/internal/profile"
	"github.com/dayflower/echoview/internal/propertyread"
)

const (
	setPropertyMapEPC byte = 0x9E
	getPropertyMapEPC byte = 0x9F
)

// Target selects all objects at Address or one EOJ at that address.
type Target struct {
	Address netip.Addr
	EOJ     *echonet.EOJ
}

// Assignment attaches an explicit profile to one object.
type Assignment struct {
	Address   netip.Addr
	EOJ       echonet.EOJ
	ProfileID string
}

// Config controls discovery and state reads.
type Config struct {
	DiscoveryTimeout  time.Duration
	DiscoveryAttempts int
	DataTimeout       time.Duration
	DataAttempts      int
	// BatchSize is an optional command-line override. Nil inherits the
	// explicitly assigned profile policy, then the application fallback.
	BatchSize     *int
	InstanceDelay time.Duration
	InitialTID    uint16
	Targets       []Target
	Assignments   []Assignment
	Profiles      *profile.Catalog
	Catalog       *catalog.Catalog
	Locale        catalog.Locale
	Debugf        func(string, ...any)
	Waitf         func(string, ...any)
	Warnf         func(string, ...any)
}

// Run discovers requested objects, reads their Get maps, and then reads every
// advertised EPC. A property failure is retained in the returned result.
func Run(ctx context.Context, conn echonet.PacketConn, config Config) ([]model.NodeResult, error) {
	if conn == nil {
		return nil, errors.New("ECHONET Lite dump has no packet connection")
	}
	if err := validate(config); err != nil {
		return nil, err
	}
	discoveryTargets := make([]Target, 0, len(config.Targets))
	directTargets := map[netip.Addr]map[echonet.EOJ]struct{}{}
	for _, target := range config.Targets {
		if target.EOJ == nil {
			discoveryTargets = append(discoveryTargets, target)
			continue
		}
		if directTargets[target.Address] == nil {
			directTargets[target.Address] = map[echonet.EOJ]struct{}{}
		}
		directTargets[target.Address][*target.EOJ] = struct{}{}
	}
	nodes := []discover.Node{}
	if len(discoveryTargets) > 0 {
		var err error
		nodes, err = discover.Run(ctx, conn, discover.Config{
			Targets: uniqueAddresses(discoveryTargets), DiscoveryTimeout: config.DiscoveryTimeout, DiscoveryAttempts: config.DiscoveryAttempts,
			DataTimeout: config.DataTimeout, DataAttempts: config.DataAttempts, InitialTID: config.InitialTID, SkipBasicProperties: true, Debugf: config.Debugf, Waitf: config.Waitf,
		})
		if err != nil {
			return nil, err
		}
	}
	assignments := make(map[objectKey]profile.Profile, len(config.Assignments))
	found := map[objectKey]struct{}{}
	for _, node := range nodes {
		found[objectKey{node.Address, node.NodeProfileEOJ}] = struct{}{}
		for eoj := range node.Instances {
			found[objectKey{node.Address, eoj}] = struct{}{}
		}
	}
	for address, eojs := range directTargets {
		for eoj := range eojs {
			found[objectKey{address, eoj}] = struct{}{}
		}
	}
	for _, assignment := range config.Assignments {
		p, ok := config.Profiles.Get(assignment.ProfileID)
		if !ok {
			return nil, fmt.Errorf("unknown profile %q", assignment.ProfileID)
		}
		if !p.ClassMatches(assignment.EOJ) {
			return nil, fmt.Errorf("profile %q class does not match %s", assignment.ProfileID, assignment.EOJ)
		}
		key := objectKey{assignment.Address, assignment.EOJ}
		if _, exists := assignments[key]; exists {
			return nil, fmt.Errorf("multiple profiles assigned to %s/%s", assignment.Address, assignment.EOJ)
		}
		assignments[key] = p
		if _, exists := found[key]; !exists {
			warnf(config, "assigned instance was not discovered: %s/%s", assignment.Address, assignment.EOJ)
		}
	}
	tid := config.InitialTID + 0x4000
	results := make([]model.NodeResult, 0, len(nodes))
	lastAddress := netip.Addr{}
	queried := map[objectKey]struct{}{}
	for _, node := range nodes {
		selected := selectedEOJs(node, config.Targets)
		if len(selected) == 0 {
			continue
		}
		result := nodeResult(node, config.Catalog)
		for _, eoj := range selected {
			if lastAddress == node.Address && config.InstanceDelay > 0 {
				time.Sleep(config.InstanceDelay)
			}
			key := objectKey{node.Address, eoj}
			queried[key] = struct{}{}
			instance, nextTID, err := readInstance(ctx, conn, node.Address, eoj, tid, config, assignments[key])
			tid = nextTID
			if err != nil {
				return nil, err
			}
			if eoj == node.NodeProfileEOJ {
				result.NodeProfile = instance
			} else {
				result.Instances = append(result.Instances, instance)
			}
			lastAddress = node.Address
		}
		results = append(results, result)
	}
	for _, address := range sortedAddresses(directTargets) {
		result := model.NodeResult{Address: address.String(), NodeProfileEOJ: echonet.NodeProfileEOJ, NodeProfile: emptyInstance(echonet.NodeProfileEOJ, config.Catalog)}
		hasDirectResult := false
		for _, eoj := range sortedDirectEOJs(directTargets[address]) {
			key := objectKey{address, eoj}
			if _, alreadyRead := queried[key]; alreadyRead {
				continue
			}
			if lastAddress == address && config.InstanceDelay > 0 {
				time.Sleep(config.InstanceDelay)
			}
			instance, nextTID, err := readInstance(ctx, conn, address, eoj, tid, config, assignments[key])
			tid = nextTID
			if err != nil {
				return nil, err
			}
			if eoj == echonet.NodeProfileEOJ {
				result.NodeProfile = instance
			} else {
				result.Instances = append(result.Instances, instance)
			}
			hasDirectResult = true
			lastAddress = address
		}
		if hasDirectResult {
			results = append(results, result)
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Address < results[j].Address })
	return results, nil
}

type objectKey struct {
	address netip.Addr
	eoj     echonet.EOJ
}

func validate(config Config) error {
	if config.DiscoveryTimeout <= 0 || config.DataTimeout <= 0 {
		return errors.New("timeouts must be greater than zero")
	}
	if config.DiscoveryAttempts <= 0 || config.DataAttempts <= 0 {
		return errors.New("attempt counts must be greater than zero")
	}
	if config.BatchSize != nil && *config.BatchSize == 0 {
		return errors.New("batch size must not be zero")
	}
	if config.InstanceDelay < 0 {
		return errors.New("instance delay must not be negative")
	}
	if len(config.Targets) == 0 {
		return errors.New("at least one target is required")
	}
	if len(config.Assignments) > 0 && config.Profiles == nil {
		return errors.New("instance profiles require a profile catalog")
	}
	return nil
}

func uniqueAddresses(targets []Target) []netip.Addr {
	values := map[netip.Addr]struct{}{}
	for _, target := range targets {
		values[target.Address] = struct{}{}
	}
	result := make([]netip.Addr, 0, len(values))
	for address := range values {
		result = append(result, address)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Less(result[j]) })
	return result
}
func sortedAddresses(values map[netip.Addr]map[echonet.EOJ]struct{}) []netip.Addr {
	result := make([]netip.Addr, 0, len(values))
	for address := range values {
		result = append(result, address)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Less(result[j]) })
	return result
}
func sortedDirectEOJs(values map[echonet.EOJ]struct{}) []echonet.EOJ {
	result := make([]echonet.EOJ, 0, len(values))
	for eoj := range values {
		result = append(result, eoj)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}
func selectedEOJs(node discover.Node, targets []Target) []echonet.EOJ {
	values := map[echonet.EOJ]struct{}{}
	for _, target := range targets {
		if target.Address != node.Address {
			continue
		}
		if target.EOJ != nil {
			if *target.EOJ == node.NodeProfileEOJ {
				values[*target.EOJ] = struct{}{}
				continue
			}
			if _, exists := node.Instances[*target.EOJ]; exists {
				values[*target.EOJ] = struct{}{}
			}
			continue
		}
		values[node.NodeProfileEOJ] = struct{}{}
		for eoj := range node.Instances {
			values[eoj] = struct{}{}
		}
	}
	result := make([]echonet.EOJ, 0, len(values))
	for eoj := range values {
		result = append(result, eoj)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}

func nodeResult(node discover.Node, loaded *catalog.Catalog) model.NodeResult {
	count := node.ReportedCount
	return model.NodeResult{Address: node.Address.String(), NodeProfileEOJ: node.NodeProfileEOJ, NodeProfile: emptyInstance(node.NodeProfileEOJ, loaded), ReportedInstanceCount: count, LastSeenUnix: node.LastSeen.Unix()}
}

func readInstance(ctx context.Context, conn echonet.PacketConn, address netip.Addr, eoj echonet.EOJ, tid uint16, config Config, applied profile.Profile) (model.InstanceResult, uint16, error) {
	hasProfile := applied.ID != ""
	matchState := ""
	properties, mapProperty, tid := readMap(ctx, conn, address, eoj, tid, config)
	if mapProperty.Status != model.PropertyOK {
		debugf(config, "Get property map unavailable for %s %s: %s", address, eoj, mapProperty.Status)
	}
	// The property maps describe an object. Keep them in output as headings,
	// but never include them in ordinary value-read batches.
	properties[getPropertyMapEPC] = struct{}{}
	values := map[byte]propertyread.Property{getPropertyMapEPC: mapProperty}
	for _, epc := range applied.MatchEPCs() {
		properties[epc] = struct{}{}
	}
	for epc := range properties {
		if epc != setPropertyMapEPC && epc != getPropertyMapEPC && applied.Disabled[epc] {
			values[epc] = propertyread.Property{Status: model.PropertySkipped}
		}
	}
	if hasProfile && len(applied.MatchEPCs()) > 0 {
		matchValues, nextTID := propertyread.ReadBatch(ctx, conn, address, eoj, applied.MatchEPCs(), tid, propertyReadConfig(config))
		tid = nextTID
		for epc, value := range matchValues {
			values[epc] = value
		}
		observed := map[byte][]byte{}
		for epc, value := range values {
			if value.EDT != nil {
				observed[epc] = value.EDT
			}
		}
		state, detail := profile.MatchState(applied, observed)
		if state == "mismatched" {
			return model.InstanceResult{}, tid, fmt.Errorf("profile %q did not match %s/%s: %s", applied.ID, address, eoj, detail)
		}
		matchState = state
		debugf(config, "applied profile %s to %s/%s: %s", applied.ID, address, eoj, state)
	}
	requested := make([]byte, 0, len(properties))
	for epc := range properties {
		if epc != setPropertyMapEPC && epc != getPropertyMapEPC && !applied.Disabled[epc] {
			requested = append(requested, epc)
		}
	}
	sort.Slice(requested, func(i, j int) bool { return requested[i] < requested[j] })
	resolved, err := collectionpolicy.Resolve(applied.Collection, collectionpolicy.Policy{BatchSize: config.BatchSize})
	if err != nil {
		return model.InstanceResult{}, tid, err
	}
	for _, batch := range batches(requested, resolved.BatchSize, applied.Single) {
		got, nextTID := propertyread.ReadBatch(ctx, conn, address, eoj, batch, tid, propertyReadConfig(config))
		tid = nextTID
		for epc, value := range got {
			values[epc] = value
		}
	}
	return makeInstance(eoj, properties, values, applied, matchState, config.Catalog, config.Locale), tid, nil
}

func readMap(ctx context.Context, conn echonet.PacketConn, address netip.Addr, eoj echonet.EOJ, tid uint16, config Config) (map[byte]struct{}, propertyread.Property, uint16) {
	got, nextTID := propertyread.ReadBatch(ctx, conn, address, eoj, []byte{getPropertyMapEPC}, tid, propertyReadConfig(config))
	property := got[getPropertyMapEPC]
	if property.EDT == nil {
		return map[byte]struct{}{}, property, nextTID
	}
	values, err := echonet.ParsePropertyMap(property.EDT)
	if err != nil {
		debugf(config, "ignored malformed Get property map from %s %s", address, eoj)
		property.Status = model.PropertyDecodeError
		return map[byte]struct{}{}, property, nextTID
	}
	return values, property, nextTID
}

func propertyReadConfig(config Config) propertyread.Config {
	return propertyread.Config{Timeout: config.DataTimeout, Attempts: config.DataAttempts, Debugf: config.Debugf, Waitf: config.Waitf}
}
func batches(epcs []byte, batchSize int, single map[byte]bool) [][]byte {
	regular := []byte{}
	singleBatches := [][]byte{}
	for _, epc := range epcs {
		if single[epc] {
			singleBatches = append(singleBatches, []byte{epc})
		} else {
			regular = append(regular, epc)
		}
	}
	if batchSize < 0 {
		if len(regular) > 0 {
			return append([][]byte{regular}, singleBatches...)
		}
		return singleBatches
	}
	out := [][]byte{}
	for len(regular) > 0 {
		size := batchSize
		if size > len(regular) {
			size = len(regular)
		}
		out = append(out, regular[:size])
		regular = regular[size:]
	}
	return append(out, singleBatches...)
}

func makeInstance(eoj echonet.EOJ, epcs map[byte]struct{}, values map[byte]propertyread.Property, applied profile.Profile, matchState string, loaded *catalog.Catalog, locale catalog.Locale) model.InstanceResult {
	instance := emptyInstance(eoj, loaded)
	if applied.ID != "" {
		profileResult := model.ProfileResult{ID: applied.ID, MatchState: matchState}
		if applied.NameJA != "" {
			value := applied.NameJA
			profileResult.NameJA = &value
		}
		if applied.NameEN != "" {
			value := applied.NameEN
			profileResult.NameEN = &value
		}
		instance.Profile = &profileResult
	}
	ordered := make([]byte, 0, len(epcs))
	for epc := range epcs {
		ordered = append(ordered, epc)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for _, epc := range ordered {
		definition, known := definitionFor(eoj, epc, applied, loaded)
		property := model.PropertyResult{EPC: echonet.FormatEPC(epc), Status: model.PropertyNotReturned}
		if known {
			property.NameJA = pointer(definition.NameJA)
			property.NameEN = pointer(definition.NameEN)
			property.ShortName = pointer(definition.ShortName)
		}
		raw, found := values[epc]
		if found {
			property.Status = raw.Status
			if raw.EDT != nil {
				rawText := echonet.FormatEDT(raw.EDT)
				property.RawEDT = &rawText
				decoded := catalog.DecodedValue{Raw: rawText, Value: rawText}
				if known {
					decoded = catalog.DecodePropertyDefinition(definition, raw.EDT, locale)
				}
				if decoded.Decoded {
					property.Value = decoded.Value
					if decoded.Unit != "" {
						property.Unit = pointer(decoded.Unit)
					}
				} else {
					property.Value = decoded.Value
				}
			}
		}
		instance.GetProperties = append(instance.GetProperties, property)
	}
	return instance
}
func emptyInstance(eoj echonet.EOJ, loaded *catalog.Catalog) model.InstanceResult {
	result := model.InstanceResult{EOJ: eoj}
	if loaded == nil {
		return result
	}
	class, ok := loaded.ClassForEOJ(eoj)
	if !ok {
		return result
	}
	result.ClassName = pointer(class.NameJA)
	result.ClassNameEN = pointer(class.NameEN)
	result.ClassShortName = pointer(class.ShortName)
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
func pointer(value string) *string {
	if value == "" {
		return nil
	}
	result := value
	return &result
}
func debugf(config Config, format string, values ...any) {
	if config.Debugf != nil {
		config.Debugf(format, values...)
	}
}
func warnf(config Config, format string, values ...any) {
	if config.Warnf != nil {
		config.Warnf(format, values...)
	}
}
