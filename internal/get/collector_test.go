package get

import (
	"context"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/metricsconfig"
)

func TestPerCollectionCollectorOpensAndClosesForEveryCollection(t *testing.T) {
	device := echonet.EOJ{0x02, 0x7D, 0x01}
	address := netip.MustParseAddr("192.0.2.10")
	connections := []*closeableFixtureConn{
		{fixtureConn: fixtureConn{source: address, device: device}},
		{fixtureConn: fixtureConn{source: address, device: device}},
	}
	opens := 0
	collector, err := NewPerCollectionCollector(func() (Connection, error) {
		connection := connections[opens]
		opens++
		return connection, nil
	}, Config{DataTimeout: time.Second, DataAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	instance := metricsconfig.Instance{Address: address, EOJ: device, MetricPrefix: "echonet_test_", Properties: []metricsconfig.Property{{EPC: 0xE0}}}
	for range connections {
		if _, err := collector.Collect(context.Background(), instance); err != nil {
			t.Fatal(err)
		}
	}
	if opens != len(connections) {
		t.Fatalf("opened %d connections, want %d", opens, len(connections))
	}
	for index, connection := range connections {
		if connection.closes != 1 {
			t.Fatalf("connection %d closed %d times, want once", index, connection.closes)
		}
	}
}

func TestCollectorVerifiesProfileOnlyBeforeTheFirstCollection(t *testing.T) {
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
	address := netip.MustParseAddr("192.0.2.10")
	conn := &fixtureConn{source: address, device: device}
	collector, err := NewCollector(conn, Config{DataTimeout: time.Second, DataAttempts: 1, Profiles: profiles})
	if err != nil {
		t.Fatal(err)
	}
	instance := metricsconfig.Instance{Address: address, EOJ: device, ProfileID: "test", Properties: []metricsconfig.Property{{EPC: 0xE0}}}
	for range 2 {
		result, err := collector.Collect(context.Background(), instance)
		if err != nil {
			t.Fatal(err)
		}
		if result.Profile == nil || result.Profile.MatchState != "matched" {
			t.Fatalf("profile = %#v", result.Profile)
		}
	}
	if got, want := conn.requests, [][]byte{{0x8A}, {0xE0}, {0xE0}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = % X, want % X", got, want)
	}
}

type closeableFixtureConn struct {
	fixtureConn
	closes int
}

func (c *closeableFixtureConn) Close() error {
	c.closes++
	return nil
}
