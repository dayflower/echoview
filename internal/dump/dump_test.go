package dump

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/model"
	"github.com/dayflower/echoview/internal/profile"
	"github.com/dayflower/echoview/internal/propertyread"
	"github.com/dayflower/echoview/internal/testfixture"
)

func TestRunReadsPropertyMapAndNormalizesSNAPartialValues(t *testing.T) {
	device := echonet.EOJ{0x02, 0x7D, 0x01}
	conn := &fixtureConn{source: netip.MustParseAddr("192.0.2.10"), device: device}
	results, err := Run(context.Background(), conn, Config{
		Targets:           []Target{{Address: conn.source, EOJ: &device}},
		DiscoveryTimeout:  time.Second,
		DiscoveryAttempts: 1,
		DataTimeout:       time.Second,
		DataAttempts:      1,
		InitialTID:        7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].Instances) != 1 {
		t.Fatalf("results = %#v", results)
	}
	properties := results[0].Instances[0].GetProperties
	if len(properties) != 4 {
		t.Fatalf("properties = %#v", properties)
	}
	if properties[0].EPC != "0x80" || properties[0].Status != model.PropertyOK || properties[0].Value != "0x30" {
		t.Fatalf("SNA value = %#v", properties[0])
	}
	if properties[3].EPC != "0xE0" || properties[3].Status != model.PropertySNA || properties[3].Value != nil {
		t.Fatalf("SNA without EDT = %#v", properties[3])
	}
	if conn.unexpectedMapRead {
		t.Fatal("0x9E or 0x9F was included in a normal value GET")
	}
	if conn.discoveryRequest {
		t.Fatal("an explicit IP/EOJ target must not send a discovery request")
	}
}

func TestReadInstanceMarksDisabledPropertySkipped(t *testing.T) {
	device := echonet.EOJ{0x02, 0x7D, 0x01}
	conn := &fixtureConn{source: netip.MustParseAddr("192.0.2.10"), device: device}
	instance, _, err := readInstance(context.Background(), conn, conn.source, device, 7, Config{
		DiscoveryTimeout:  time.Second,
		DiscoveryAttempts: 1,
		DataTimeout:       time.Second,
		DataAttempts:      1,
	}, profile.Profile{Disabled: map[byte]bool{0xE0: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, property := range instance.GetProperties {
		if property.EPC == "0xE0" {
			if property.Status != model.PropertySkipped || property.Value != nil {
				t.Fatalf("disabled property = %#v", property)
			}
			if conn.requestedE0 {
				t.Fatal("disabled property was requested")
			}
			return
		}
	}
	t.Fatalf("disabled property missing from %#v", instance.GetProperties)
}

func TestReadMapMarksMalformedEDTDecodeError(t *testing.T) {
	address := netip.MustParseAddr("192.0.2.10")
	device := echonet.EOJ{0x02, 0x7D, 0x01}
	conn := &testfixture.PropertyReadConn{
		Source: address,
		Device: device,
		Steps: []testfixture.PropertyReadStep{{
			Kind: "response",
			ESV:  echonet.ESVGetRes,
			Properties: []echonet.Property{{
				EPC: getPropertyMapEPC,
				EDT: []byte{0x02, 0x80},
			}},
		}},
	}
	properties, property, nextTID := readMap(context.Background(), conn, address, device, 7, Config{
		DataTimeout:  time.Second,
		DataAttempts: 1,
	})
	if len(properties) != 0 || property.Status != model.PropertyDecodeError || !bytes.Equal(property.EDT, []byte{0x02, 0x80}) || nextTID != 8 {
		t.Fatalf("readMap() = %#v, %#v, %d", properties, property, nextTID)
	}
}

func TestRunKeepsDirectAndDiscoveredResultsOrdered(t *testing.T) {
	directAddress := netip.MustParseAddr("192.0.2.10")
	discoveryAddress := netip.MustParseAddr("192.0.2.20")
	first := echonet.EOJ{0x01, 0x30, 0x01}
	second := echonet.EOJ{0x01, 0x30, 0x02}
	conn := &orderingConn{discoveredEOJ: first}

	results, err := Run(context.Background(), conn, Config{
		Targets: []Target{
			{Address: directAddress, EOJ: &second},
			{Address: discoveryAddress},
			{Address: directAddress, EOJ: &first},
		},
		DiscoveryTimeout:  time.Second,
		DiscoveryAttempts: 1,
		DataTimeout:       time.Second,
		DataAttempts:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Address != directAddress.String() || results[1].Address != discoveryAddress.String() {
		t.Fatalf("result addresses = %#v", results)
	}
	if got := results[0].Instances; len(got) != 2 || got[0].EOJ != first || got[1].EOJ != second {
		t.Fatalf("direct instances = %#v", got)
	}
	if got := results[1].Instances; len(got) != 1 || got[0].EOJ != first {
		t.Fatalf("discovered instances = %#v", got)
	}
}

func TestRunRejectsProfileAssignmentForAnotherClass(t *testing.T) {
	profiles := loadProfiles(t, `
catalog_format: 1
profiles:
  wrong_class:
    class: "0x0130"
`)
	address := netip.MustParseAddr("192.0.2.10")
	device := echonet.EOJ{0x02, 0x7D, 0x01}
	_, err := Run(context.Background(), &fixtureConn{source: address, device: device}, Config{
		DiscoveryTimeout: time.Second, DiscoveryAttempts: 1, DataTimeout: time.Second, DataAttempts: 1,
		Targets: []Target{{Address: address, EOJ: &device}}, Profiles: profiles,
		Assignments: []Assignment{{Address: address, EOJ: device, ProfileID: "wrong_class"}},
	})
	if err == nil || !strings.Contains(err.Error(), "class does not match") {
		t.Fatalf("Run() error = %v, want class mismatch", err)
	}
}

func TestRunRejectsDuplicateProfileAssignments(t *testing.T) {
	profiles := loadProfiles(t, `
catalog_format: 1
profiles:
  device:
    class: "0x027D"
`)
	address := netip.MustParseAddr("192.0.2.10")
	device := echonet.EOJ{0x02, 0x7D, 0x01}
	assignment := Assignment{Address: address, EOJ: device, ProfileID: "device"}
	_, err := Run(context.Background(), &fixtureConn{source: address, device: device}, Config{
		DiscoveryTimeout: time.Second, DiscoveryAttempts: 1, DataTimeout: time.Second, DataAttempts: 1,
		Targets: []Target{{Address: address, EOJ: &device}}, Profiles: profiles,
		Assignments: []Assignment{assignment, assignment},
	})
	if err == nil || !strings.Contains(err.Error(), "multiple profiles assigned") {
		t.Fatalf("Run() error = %v, want duplicate assignment error", err)
	}
}

func TestRunWarnsForUndiscoveredProfileAssignment(t *testing.T) {
	profiles := loadProfiles(t, `
catalog_format: 1
profiles:
  device:
    class: "0x027D"
`)
	address := netip.MustParseAddr("192.0.2.10")
	missingAddress := netip.MustParseAddr("192.0.2.20")
	device := echonet.EOJ{0x02, 0x7D, 0x01}
	var warnings []string
	_, err := Run(context.Background(), &fixtureConn{source: address, device: device}, Config{
		DiscoveryTimeout: time.Second, DiscoveryAttempts: 1, DataTimeout: time.Second, DataAttempts: 1,
		Targets: []Target{{Address: address, EOJ: &device}}, Profiles: profiles,
		Assignments: []Assignment{{Address: missingAddress, EOJ: device, ProfileID: "device"}},
		Warnf:       func(format string, values ...any) { warnings = append(warnings, fmt.Sprintf(format, values...)) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "assigned instance was not discovered: 192.0.2.20/0x027D01") {
		t.Fatalf("warnings = %#v", warnings)
	}
}

func TestRunAttachesProfileOnlyAfterMatchingRead(t *testing.T) {
	profiles := loadProfiles(t, `
catalog_format: 1
profiles:
  device:
    class: "0x027D"
    match:
      required:
        "0x8A": ["0x000005"]
`)
	address := netip.MustParseAddr("192.0.2.10")
	device := echonet.EOJ{0x02, 0x7D, 0x01}
	results, err := Run(context.Background(), &fixtureConn{source: address, device: device, matchProfile: true}, Config{
		DiscoveryTimeout: time.Second, DiscoveryAttempts: 1, DataTimeout: time.Second, DataAttempts: 1,
		Targets: []Target{{Address: address, EOJ: &device}}, Profiles: profiles,
		Assignments: []Assignment{{Address: address, EOJ: device, ProfileID: "device"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].Instances) != 1 || results[0].Instances[0].Profile == nil || results[0].Instances[0].Profile.MatchState != "matched" {
		t.Fatalf("results = %#v", results)
	}
}

func TestMakeInstanceIncludesAppliedProfileMetadata(t *testing.T) {
	eoj := echonet.EOJ{0x05, 0xFF, 0x01}
	definition := catalog.Property{EPC: 0xF2, NameJA: "瞬時売電電力", NameEN: "Instantaneous Electric Power Sold", Codecs: []catalog.Codec{{Kind: "uint", Bytes: 4, Unit: "W"}}}
	instance := makeInstance(eoj, map[byte]struct{}{0xF2: {}}, map[byte]propertyread.Property{0xF2: {EDT: []byte{0, 0, 0, 42}, Status: model.PropertyOK}}, profile.Profile{
		ID: "controller", NameJA: "コントローラー", NameEN: "Controller", Properties: map[byte]catalog.Property{0xF2: definition},
	}, "matched", nil, catalog.LocaleJapanese)
	if instance.Profile == nil || instance.Profile.ID != "controller" || instance.Profile.MatchState != "matched" || instance.Profile.NameJA == nil || *instance.Profile.NameJA != "コントローラー" {
		t.Fatalf("profile = %#v", instance.Profile)
	}
	property := instance.GetProperties[0]
	if property.NameJA == nil || *property.NameJA != "瞬時売電電力" || property.Value != "42" || property.Unit == nil || *property.Unit != "W" {
		t.Fatalf("property = %#v", property)
	}
}

func TestBatchesSortsRegularAndSingleReads(t *testing.T) {
	for _, test := range []struct {
		name      string
		epcs      []byte
		batchSize int
		single    map[byte]bool
		want      [][]byte
	}{
		{"unbounded", []byte{0x80, 0x81, 0xB0}, -1, nil, [][]byte{{0x80, 0x81, 0xB0}}},
		{"split with single", []byte{0x80, 0x81, 0xB0}, 2, map[byte]bool{0xB0: true}, [][]byte{{0x80, 0x81}, {0xB0}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := batches(test.epcs, test.batchSize, test.single); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("batches() = % X, want % X", got, test.want)
			}
		})
	}
}

func loadProfiles(t *testing.T, source string) *profile.Catalog {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := profile.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

type fixtureConn struct {
	source            netip.Addr
	device            echonet.EOJ
	queue             [][]byte
	mapRead           bool
	unexpectedMapRead bool
	discoveryRequest  bool
	requestedE0       bool
	matchProfile      bool
}

func (c *fixtureConn) WriteTo(packet []byte, _ net.Addr) (int, error) {
	frame, err := echonet.DecodeFrame(packet)
	if err != nil {
		return 0, err
	}
	response := func(esv byte, properties []echonet.Property) {
		packet, err := (echonet.Frame{TID: frame.TID, SEOJ: frame.DEOJ, DEOJ: echonet.ControllerEOJ, ESV: esv, Properties: properties}).Encode()
		if err != nil {
			panic(err)
		}
		c.queue = append(c.queue, packet)
	}
	if frame.DEOJ == echonet.NodeProfileEOJ && len(frame.Properties) == 1 && frame.Properties[0].EPC == 0xD6 {
		c.discoveryRequest = true
		response(echonet.ESVGetRes, []echonet.Property{{EPC: 0xD6, EDT: []byte{1, c.device[0], c.device[1], c.device[2]}}})
	} else if frame.DEOJ == c.device && len(frame.Properties) == 1 && frame.Properties[0].EPC == 0x9F && !c.mapRead {
		c.mapRead = true
		response(echonet.ESVGetRes, []echonet.Property{{EPC: 0x9F, EDT: []byte{4, 0x80, 0x9E, 0x9F, 0xE0}}})
	} else if frame.DEOJ == c.device {
		for _, property := range frame.Properties {
			if property.EPC == 0x9E || property.EPC == 0x9F {
				c.unexpectedMapRead = true
			}
			if property.EPC == 0xE0 {
				c.requestedE0 = true
			}
			if property.EPC == 0x8A && c.matchProfile {
				response(echonet.ESVGetRes, []echonet.Property{{EPC: 0x8A, EDT: []byte{0, 0, 5}}})
				return len(packet), nil
			}
		}
		response(echonet.ESVGetSNA, []echonet.Property{{EPC: 0x80, EDT: []byte{0x30}}, {EPC: 0xE0}})
	} else {
		response(echonet.ESVGetRes, nil)
	}
	return len(packet), nil
}

func (c *fixtureConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	if len(c.queue) == 0 {
		return 0, nil, fixtureTimeout{}
	}
	packet := c.queue[0]
	c.queue = c.queue[1:]
	return copy(buffer, packet), net.UDPAddrFromAddrPort(netip.AddrPortFrom(c.source, 3610)), nil
}
func (c *fixtureConn) SetReadDeadline(time.Time) error { return nil }

type fixtureTimeout struct{}

func (fixtureTimeout) Error() string   { return "fixture timeout" }
func (fixtureTimeout) Timeout() bool   { return true }
func (fixtureTimeout) Temporary() bool { return true }

type queuedPacket struct {
	source netip.Addr
	packet []byte
}

type orderingConn struct {
	discoveredEOJ echonet.EOJ
	queue         []queuedPacket
}

func (c *orderingConn) WriteTo(packet []byte, destination net.Addr) (int, error) {
	frame, err := echonet.DecodeFrame(packet)
	if err != nil {
		return 0, err
	}
	udpDestination := destination.(*net.UDPAddr).AddrPort().Addr().Unmap()
	properties := []echonet.Property{{EPC: getPropertyMapEPC, EDT: []byte{0}}}
	if len(frame.Properties) == 1 && frame.Properties[0].EPC == 0xD6 {
		properties = []echonet.Property{{
			EPC: 0xD6,
			EDT: []byte{1, c.discoveredEOJ[0], c.discoveredEOJ[1], c.discoveredEOJ[2]},
		}}
	}
	response, err := (echonet.Frame{
		TID: frame.TID, SEOJ: frame.DEOJ, DEOJ: echonet.ControllerEOJ,
		ESV: echonet.ESVGetRes, Properties: properties,
	}).Encode()
	if err != nil {
		return 0, err
	}
	c.queue = append(c.queue, queuedPacket{source: udpDestination, packet: response})
	return len(packet), nil
}

func (c *orderingConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	if len(c.queue) == 0 {
		return 0, nil, fixtureTimeout{}
	}
	item := c.queue[0]
	c.queue = c.queue[1:]
	return copy(buffer, item.packet), net.UDPAddrFromAddrPort(netip.AddrPortFrom(item.source, 3610)), nil
}

func (c *orderingConn) SetReadDeadline(time.Time) error { return nil }
