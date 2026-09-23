package discover

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayflower/echoview/internal/echonet"
)

type discoveryFixture struct {
	Name              string   `json:"name"`
	Packet            string   `json:"packet"`
	ValidInstanceList bool     `json:"valid_instance_list"`
	EOJs              []string `json:"eojs"`
}

func TestDiscoveryFixtures(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "discovery_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []discoveryFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			packet, err := hex.DecodeString(fixture.Packet)
			if err != nil {
				t.Fatal(err)
			}
			frame, err := echonet.DecodeFrame(packet)
			if err != nil {
				t.Fatal(err)
			}
			eojs, err := echonet.ParseInstanceList(frame.Properties[0].EDT)
			if !fixture.ValidInstanceList {
				if err == nil {
					t.Fatal("malformed list was accepted")
				}
				return
			}
			if err != nil || len(eojs) != len(fixture.EOJs) {
				t.Fatalf("ParseInstanceList() = %#v, %v", eojs, err)
			}
			for index, eoj := range eojs {
				if eoj.String() != fixture.EOJs[index] {
					t.Fatalf("EOJ %d = %s, want %s", index, eoj, fixture.EOJs[index])
				}
			}
		})
	}
}

func TestRunMergesNotificationsAndPreservesPartialAndSNAResults(t *testing.T) {
	conn := &discoveryConn{source: netip.MustParseAddr("192.0.2.10"), respondData: true}
	conn.discoveryPackets = [][]byte{
		mustPacket(t, "108100070EF00105FF017301D50702027D01013001"),
		mustPacket(t, "108100070EF00105FF017301D60401027D01"),
		[]byte{0x10, 0x81}, // malformed frames are ignored.
	}
	nodes, err := Run(context.Background(), conn, Config{
		Targets:           []netip.Addr{netip.MustParseAddr("192.0.2.10")},
		DiscoveryTimeout:  time.Second,
		DiscoveryAttempts: 3,
		DataTimeout:       time.Second,
		DataAttempts:      1,
		InitialTID:        7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if conn.discoveryWrites != 1 {
		t.Fatalf("discovery writes = %d, want 1 for explicit targets", conn.discoveryWrites)
	}
	if len(conn.discoveryDestinations) != 1 {
		t.Fatalf("discovery destinations = %#v, want one", conn.discoveryDestinations)
	}
	if destination := conn.discoveryDestinations[0]; destination != netip.AddrPortFrom(conn.source, Port) {
		t.Fatalf("discovery destination = %s, want %s", destination, netip.AddrPortFrom(conn.source, Port))
	}
	if len(nodes) != 1 || len(nodes[0].Instances) != 2 {
		t.Fatalf("nodes = %#v", nodes)
	}
	node := nodes[0]
	if node.ReportedCount == nil || *node.ReportedCount != 1 {
		t.Fatalf("reported count = %v, want latest D6 count 1", node.ReportedCount)
	}
	if got := node.NodeProperties[0x83]; got.Status != StatusOK || string(got.EDT) != "id" {
		t.Fatalf("identifier = %#v", got)
	}
	if got := node.NodeProperties[0xD3]; got.Status != StatusNotReturned {
		t.Fatalf("partial D3 = %#v", got)
	}
	storage := echonet.EOJ{0x02, 0x7D, 0x01}
	if got := node.InstanceProperties[storage][0x80]; got.Status != StatusOK || string(got.EDT) != "0" {
		t.Fatalf("SNA value = %#v", got)
	}
	if got := node.InstanceProperties[storage][0x81]; got.Status != StatusSNA || got.EDT != nil {
		t.Fatalf("SNA missing value = %#v", got)
	}
}

func TestRunRetriesOnlyTimeouts(t *testing.T) {
	conn := &discoveryConn{source: netip.MustParseAddr("192.0.2.10")}
	conn.discoveryPackets = [][]byte{mustPacket(t, "108100070EF00105FF017301D60100")}
	_, err := Run(context.Background(), conn, Config{
		Targets:           []netip.Addr{netip.MustParseAddr("192.0.2.10")},
		DiscoveryTimeout:  time.Second,
		DiscoveryAttempts: 1,
		DataTimeout:       time.Second,
		DataAttempts:      2,
		InitialTID:        7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if conn.nodePropertyWrites != 2 {
		t.Fatalf("node GET writes = %d, want 2 timeout attempts", conn.nodePropertyWrites)
	}
}

func TestRunUsesConfiguredMulticastAttempts(t *testing.T) {
	conn := &discoveryConn{source: netip.MustParseAddr("192.0.2.10"), respondData: true}
	conn.discoveryPackets = [][]byte{mustPacket(t, "108100070EF00105FF017301D60100")}
	_, err := Run(context.Background(), conn, Config{
		DiscoveryTimeout:  time.Second,
		DiscoveryAttempts: 3,
		DataTimeout:       time.Second,
		DataAttempts:      1,
		InitialTID:        7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if conn.discoveryWrites != 3 {
		t.Fatalf("discovery writes = %d, want 3 multicast attempts", conn.discoveryWrites)
	}
	if got, want := conn.discoveryDestinations[0], netip.AddrPortFrom(netip.MustParseAddr(MulticastAddress), Port); got != want {
		t.Fatalf("discovery destination = %s, want %s", got, want)
	}
}

func TestSortedNodesOrdersAddressThenNodeProfileEOJ(t *testing.T) {
	firstAddress := netip.MustParseAddr("192.0.2.10")
	secondAddress := netip.MustParseAddr("192.0.2.20")
	firstEOJ := echonet.EOJ{0x0E, 0xF0, 0x01}
	secondEOJ := echonet.EOJ{0x0E, 0xF0, 0x02}
	nodes := map[nodeKey]*Node{
		{address: secondAddress, eoj: firstEOJ}: {Address: secondAddress, NodeProfileEOJ: firstEOJ},
		{address: firstAddress, eoj: secondEOJ}: {Address: firstAddress, NodeProfileEOJ: secondEOJ},
		{address: firstAddress, eoj: firstEOJ}:  {Address: firstAddress, NodeProfileEOJ: firstEOJ},
	}
	got := sortedNodes(nodes)
	want := []struct {
		address netip.Addr
		eoj     echonet.EOJ
	}{
		{firstAddress, firstEOJ},
		{firstAddress, secondEOJ},
		{secondAddress, firstEOJ},
	}
	if len(got) != len(want) {
		t.Fatalf("sorted node count = %d, want %d", len(got), len(want))
	}
	for index, expected := range want {
		if got[index].Address != expected.address || got[index].NodeProfileEOJ != expected.eoj {
			t.Fatalf("node %d = %#v, want address %s EOJ %s", index, got[index], expected.address, expected.eoj)
		}
	}
}

type discoveryConn struct {
	source                netip.Addr
	discoveryPackets      [][]byte
	readQueue             [][]byte
	discoveryWrites       int
	discoveryDestinations []netip.AddrPort
	nodePropertyWrites    int
	respondData           bool
}

func (c *discoveryConn) WriteTo(packet []byte, destination net.Addr) (int, error) {
	frame, err := echonet.DecodeFrame(packet)
	if err != nil {
		return 0, err
	}
	if len(frame.Properties) == 1 && frame.Properties[0].EPC == 0xD6 {
		c.discoveryWrites++
		if udpDestination, ok := destination.(*net.UDPAddr); ok {
			c.discoveryDestinations = append(c.discoveryDestinations, udpDestination.AddrPort())
		}
		c.readQueue = append(c.readQueue, c.discoveryPackets...)
		c.discoveryPackets = nil
		return len(packet), nil
	}
	if frame.DEOJ == echonet.NodeProfileEOJ {
		c.nodePropertyWrites++
		if c.respondData {
			c.readQueue = append(c.readQueue, responsePacket(tidFrame(frame, echonet.ESVGetRes, []echonet.Property{{EPC: 0x83, EDT: []byte("id")}})))
		}
		return len(packet), nil
	}
	if !c.respondData {
		return len(packet), nil
	}
	if frame.DEOJ == (echonet.EOJ{0x02, 0x7D, 0x01}) {
		c.readQueue = append(c.readQueue, responsePacket(tidFrame(frame, echonet.ESVGetSNA, []echonet.Property{{EPC: 0x80, EDT: []byte("0")}})))
		return len(packet), nil
	}
	c.readQueue = append(c.readQueue, responsePacket(tidFrame(frame, echonet.ESVGetRes, []echonet.Property{{EPC: 0x80, EDT: []byte{0x30}}})))
	return len(packet), nil
}

func (c *discoveryConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	if len(c.readQueue) == 0 {
		return 0, nil, timeoutError{}
	}
	packet := c.readQueue[0]
	c.readQueue = c.readQueue[1:]
	return copy(buffer, packet), net.UDPAddrFromAddrPort(netip.AddrPortFrom(c.source, Port)), nil
}

func (c *discoveryConn) SetReadDeadline(time.Time) error { return nil }

type timeoutError struct{}

func (timeoutError) Error() string   { return "fixture timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func tidFrame(request echonet.Frame, esv byte, properties []echonet.Property) echonet.Frame {
	return echonet.Frame{TID: request.TID, SEOJ: request.DEOJ, DEOJ: echonet.ControllerEOJ, ESV: esv, Properties: properties}
}

func responsePacket(frame echonet.Frame) []byte {
	packet, err := frame.Encode()
	if err != nil {
		panic(err)
	}
	return packet
}

func mustPacket(t *testing.T, value string) []byte {
	t.Helper()
	packet, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return packet
}
