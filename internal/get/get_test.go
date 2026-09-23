package get

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/metricsconfig"
	"github.com/dayflower/echoview/internal/model"
	"github.com/dayflower/echoview/internal/profile"
	"github.com/dayflower/echoview/internal/propertyread"
)

func TestRunUsesOnlyConfiguredPropertiesAndCollectionPolicy(t *testing.T) {
	profiles := loadProfile(t, `
catalog_format: 1
profiles:
  test:
    class: "0x027D"
    properties:
      "0xE0":
        name: { ja: テスト, en: Test }
        codec: { kinds: [{ kind: uint, bytes: 1, unit: W }] }
    collection:
      batch_size: 1
      properties:
        "0xE1": { request: single }
        "0xE2": { enabled: false }
`)
	device := echonet.EOJ{0x02, 0x7D, 0x01}
	conn := &fixtureConn{source: netip.MustParseAddr("192.0.2.10"), device: device}
	results, err := Run(context.Background(), conn, Config{
		DataTimeout: time.Second, DataAttempts: 1, InitialTID: 3, Profiles: profiles,
		Instances: []metricsconfig.Instance{{
			Address: conn.source, EOJ: device, ProfileID: "test", MetricPrefix: "echonet_test_", IntervalSeconds: 60, BatchSize: 1,
			Properties: []metricsconfig.Property{{EPC: 0xE0}, {EPC: 0xE1}, {EPC: 0xE2}, {EPC: 0xE3}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := conn.requests, [][]byte{{0xE0}, {0xE3}, {0xE1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = % X, want % X", got, want)
	}
	properties := results[0].Instances[0].GetProperties
	if len(properties) != 4 {
		t.Fatalf("properties = %#v", properties)
	}
	if properties[0].Value != "42" || properties[0].Unit == nil || *properties[0].Unit != "W" || properties[0].Status != model.PropertyOK {
		t.Fatalf("decoded property = %#v", properties[0])
	}
	if properties[1].Status != model.PropertyOK || properties[1].Value != "0x30" {
		t.Fatalf("SNA property = %#v", properties[1])
	}
	if properties[2].Status != model.PropertySkipped || properties[2].Value != nil {
		t.Fatalf("disabled property = %#v", properties[2])
	}
	if properties[3].Status != model.PropertyNotReturned || properties[3].Value != nil {
		t.Fatalf("partial response property = %#v", properties[3])
	}
}

func TestRunRetriesTimeoutOnly(t *testing.T) {
	device := echonet.EOJ{0x02, 0x7D, 0x01}
	conn := &fixtureConn{source: netip.MustParseAddr("192.0.2.10"), device: device, timeoutE0: true}
	results, err := Run(context.Background(), conn, Config{DataTimeout: time.Second, DataAttempts: 2, Instances: []metricsconfig.Instance{{Address: conn.source, EOJ: device, MetricPrefix: "echonet_test_", IntervalSeconds: 1, Properties: []metricsconfig.Property{{EPC: 0xE0}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(conn.requests) != 2 || results[0].Instances[0].GetProperties[0].Status != model.PropertyTimeout {
		t.Fatalf("requests = % X, result = %#v", conn.requests, results)
	}
}

func TestBatchesNormalizesUnboundedProfileBatchSizes(t *testing.T) {
	size := 2
	for _, tc := range []struct {
		name      string
		epcs      []byte
		batchSize int
		want      [][]byte
	}{
		{name: "empty", want: nil},
		{name: "zero-value is unbounded", epcs: []byte{0x80, 0x81}, want: [][]byte{{0x80, 0x81}}},
		{name: "negative is unbounded", epcs: []byte{0x80, 0x81}, batchSize: -1, want: [][]byte{{0x80, 0x81}}},
		{name: "positive splits", epcs: []byte{0x80, 0x81, 0x82}, batchSize: size, want: [][]byte{{0x80, 0x81}, {0x82}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := batches(tc.epcs, tc.batchSize); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("batches() = % X, want % X", got, tc.want)
			}
		})
	}
}

func TestMakeInstanceRetainsUnmappedEnumForPrometheusPolicy(t *testing.T) {
	loaded := loadCatalog(t, `
catalog_format: 1
classes:
  "0x027D":
    name: {ja: test}
    properties:
      "0xE0":
        name: {ja: mode}
        short_name: mode
        prometheus:
          export: enum_map
          enum_map:
            values: {"0x30": 1}
        codec:
          kinds:
            - kind: enum
              size: 1
              values: {"0x30": {value: on}}
`)
	selected := metricsconfig.Instance{EOJ: echonet.EOJ{0x02, 0x7D, 0x01}, Properties: []metricsconfig.Property{{EPC: 0xE0}}}
	result := makeInstance(selected, map[byte]propertyread.Property{0xE0: {EDT: []byte{0x7F}, Status: model.PropertyOK}}, profile.Profile{}, "", loaded, catalog.LocaleEnglish)
	property := result.GetProperties[0]
	if property.Status != model.PropertyOK || property.ValueKind != "raw" || property.RawEDT == nil || *property.RawEDT != "0x7F" {
		t.Fatalf("property = %#v", property)
	}
}

func TestRunVerifiesConfiguredProfileBeforeReadingProperties(t *testing.T) {
	profiles := loadProfile(t, `
catalog_format: 1
profiles:
  test:
    class: "0x027D"
    match:
      required:
        "0x8A": ["0x000005"]
`)
	device := echonet.EOJ{0x02, 0x7D, 0x01}
	conn := &fixtureConn{source: netip.MustParseAddr("192.0.2.10"), device: device}
	results, err := Run(context.Background(), conn, Config{DataTimeout: time.Second, DataAttempts: 1, Profiles: profiles, Instances: []metricsconfig.Instance{{Address: conn.source, EOJ: device, ProfileID: "test", Properties: []metricsconfig.Property{{EPC: 0xE0}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := conn.requests, [][]byte{{0x8A}, {0xE0}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = % X, want % X", got, want)
	}
	if state := results[0].Instances[0].Profile.MatchState; state != "matched" {
		t.Fatalf("profile state = %q, want matched", state)
	}
}

func TestRunReturnsProfileMismatchBeforeReadingProperties(t *testing.T) {
	profiles := loadProfile(t, `
catalog_format: 1
profiles:
  test:
    class: "0x027D"
    match:
      required:
        "0x8A": ["0x000006"]
`)
	device := echonet.EOJ{0x02, 0x7D, 0x01}
	conn := &fixtureConn{source: netip.MustParseAddr("192.0.2.10"), device: device}
	_, err := Run(context.Background(), conn, Config{DataTimeout: time.Second, DataAttempts: 1, Profiles: profiles, Instances: []metricsconfig.Instance{{Address: conn.source, EOJ: device, ProfileID: "test", Properties: []metricsconfig.Property{{EPC: 0xE0}}}}})
	var mismatch *ProfileMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error = %v, want ProfileMismatchError", err)
	}
	if got, want := conn.requests, [][]byte{{0x8A}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = % X, want % X", got, want)
	}
}

type fixtureConn struct {
	source    netip.Addr
	device    echonet.EOJ
	queue     [][]byte
	requests  [][]byte
	timeoutE0 bool
}

func (c *fixtureConn) WriteTo(packet []byte, _ net.Addr) (int, error) {
	frame, err := echonet.DecodeFrame(packet)
	if err != nil {
		return 0, err
	}
	epCs := make([]byte, len(frame.Properties))
	for i, property := range frame.Properties {
		epCs[i] = property.EPC
	}
	c.requests = append(c.requests, epCs)
	if c.timeoutE0 {
		return len(packet), nil
	}
	properties, esv := []echonet.Property{}, echonet.ESVGetRes
	for _, epc := range epCs {
		switch epc {
		case 0xE0:
			properties = append(properties, echonet.Property{EPC: epc, EDT: []byte{42}})
		case 0xE1:
			esv = echonet.ESVGetSNA
			properties = append(properties, echonet.Property{EPC: epc, EDT: []byte{0x30}})
		case 0x8A:
			properties = append(properties, echonet.Property{EPC: epc, EDT: []byte{0x00, 0x00, 0x05}})
		}
	}
	response, err := (echonet.Frame{TID: frame.TID, SEOJ: c.device, DEOJ: echonet.ControllerEOJ, ESV: esv, Properties: properties}).Encode()
	if err != nil {
		return 0, err
	}
	c.queue = append(c.queue, response)
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

func loadProfile(t *testing.T, source string) *profile.Catalog {
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

func loadCatalog(t *testing.T, source string) *catalog.Catalog {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}
