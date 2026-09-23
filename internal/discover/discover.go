// Package discover implements ECHONET Lite node and instance discovery.
package discover

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"time"

	"github.com/dayflower/echoview/internal/echonet"
)

const (
	MulticastAddress = "224.0.23.0"
	Port             = 3610
)

var (
	NodePropertyEPCs     = []byte{0x83, 0xD3, 0xD4}
	InstancePropertyEPCs = []byte{0x80, 0x81, 0x8A, 0x8B, 0x8C, 0x8D}
)

// PropertyStatus records the result of one requested property.
type PropertyStatus string

const (
	StatusOK          PropertyStatus = "ok"
	StatusTimeout     PropertyStatus = "timeout"
	StatusSNA         PropertyStatus = "sna"
	StatusNotReturned PropertyStatus = "not_returned"
)

// Property is the raw result of a property request.
type Property struct {
	EPC    byte
	EDT    []byte
	Status PropertyStatus
}

// Node combines all D5/D6 notifications received from one node profile.
type Node struct {
	Address            netip.Addr
	NodeProfileEOJ     echonet.EOJ
	Instances          map[echonet.EOJ]struct{}
	ReportedCount      *int
	LastSeen           time.Time
	NodeProperties     map[byte]Property
	InstanceProperties map[echonet.EOJ]map[byte]Property
}

// Config controls discovery and subsequent basic-property reads.
type Config struct {
	Targets           []netip.Addr
	DiscoveryTimeout  time.Duration
	DiscoveryAttempts int
	DataTimeout       time.Duration
	DataAttempts      int
	InitialTID        uint16
	// SkipBasicProperties suppresses discover-command-only reads (83, D3, D4
	// and instance identity fields) for callers that only need instance EOJs.
	SkipBasicProperties bool
	Debugf              func(string, ...any)
	Waitf               func(string, ...any)
}

// PacketConn is the UDP surface needed by discovery.  It is deliberately
// small so protocol fixtures can exercise discovery without a network.
type PacketConn interface {
	echonet.PacketConn
}

// Run sends discovery packets through conn, gathers D5/D6 notifications, and
// reads the basic node and instance properties needed by the discover command.
func Run(ctx context.Context, conn PacketConn, config Config) ([]Node, error) {
	if conn == nil {
		return nil, errors.New("ECHONET Lite discovery has no packet connection")
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	destinations := config.Targets
	attempts := config.DiscoveryAttempts
	if len(destinations) == 0 {
		destinations = []netip.Addr{netip.MustParseAddr(MulticastAddress)}
	} else {
		attempts = 1
	}

	nodes := map[nodeKey]*Node{}
	tid := config.InitialTID
	request, err := echonet.BuildInstanceListRequest(tid)
	if err != nil {
		return nil, err
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		for _, address := range destinations {
			target := netip.AddrPortFrom(address, Port)
			if _, err := conn.WriteTo(request, net.UDPAddrFromAddrPort(target)); err != nil {
				return nil, fmt.Errorf("send discovery request to %s: %w", target, err)
			}
			debugf(config, "sent instance-list request %d/%d TID=0x%04X to %s", attempt, attempts, tid, target)
		}
		if err := receiveDiscovery(ctx, conn, tid, config.DiscoveryTimeout, nodes, config); err != nil {
			return nil, err
		}
	}

	result := sortedNodes(nodes)
	if config.SkipBasicProperties {
		return result, nil
	}
	for index := range result {
		tid++
		result[index].NodeProperties = readProperties(ctx, conn, result[index].Address, result[index].NodeProfileEOJ, NodePropertyEPCs, tid, config)
		for _, eoj := range sortedEOJs(result[index].Instances) {
			if eoj == result[index].NodeProfileEOJ {
				continue
			}
			tid++
			if result[index].InstanceProperties == nil {
				result[index].InstanceProperties = make(map[echonet.EOJ]map[byte]Property)
			}
			result[index].InstanceProperties[eoj] = readProperties(ctx, conn, result[index].Address, eoj, InstancePropertyEPCs, tid, config)
		}
	}
	return result, nil
}

type nodeKey struct {
	address netip.Addr
	eoj     echonet.EOJ
}

func receiveDiscovery(ctx context.Context, conn PacketConn, tid uint16, timeout time.Duration, nodes map[nodeKey]*Node, config Config) error {
	waitf(config, "waiting up to %s for discovery responses", timeout)
	expectedD6 := make(map[netip.Addr]struct{}, len(config.Targets))
	for _, target := range config.Targets {
		expectedD6[target] = struct{}{}
	}
	receivedD6 := make(map[netip.Addr]struct{}, len(expectedD6))
	deadline := time.Now().Add(timeout)
	buffer := make([]byte, 65535)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := conn.SetReadDeadline(deadline); err != nil {
			return fmt.Errorf("set discovery read deadline: %w", err)
		}
		n, sender, err := conn.ReadFrom(buffer)
		if err != nil {
			if isTimeout(err) {
				return nil
			}
			return fmt.Errorf("receive discovery packet: %w", err)
		}
		debugf(config, "received UDP packet during discovery from %v", sender)
		udpSender, ok := sender.(*net.UDPAddr)
		senderAddress, validSender := senderIPv4(udpSender)
		if !ok || !validSender {
			debugf(config, "ignored discovery packet from non-IPv4 UDP sender %v", sender)
			continue
		}
		frame, err := echonet.DecodeFrame(buffer[:n])
		if err != nil {
			debugf(config, "ignored malformed discovery packet from %s: %v", senderAddress, err)
			continue
		}
		if !echonet.IsInstanceListResponse(frame, tid) {
			debugf(config, "ignored non-instance-list response from %s (TID=0x%04X, ESV=0x%02X, SEOJ=%s, DEOJ=%s)", senderAddress, frame.TID, frame.ESV, frame.SEOJ, frame.DEOJ)
			continue
		}
		for _, property := range frame.Properties {
			if property.EPC != 0xD5 && property.EPC != 0xD6 {
				continue
			}
			eojs, err := echonet.ParseInstanceList(property.EDT)
			if err != nil {
				debugf(config, "ignored malformed instance list from %s", senderAddress)
				continue
			}
			key := nodeKey{address: senderAddress, eoj: frame.SEOJ}
			node := nodes[key]
			if node == nil {
				node = &Node{Address: key.address, NodeProfileEOJ: frame.SEOJ, Instances: make(map[echonet.EOJ]struct{})}
				nodes[key] = node
			}
			count := len(eojs)
			node.ReportedCount = &count
			node.LastSeen = time.Now()
			for _, eoj := range eojs {
				node.Instances[eoj] = struct{}{}
			}
			if property.EPC == 0xD6 {
				if _, expected := expectedD6[senderAddress]; !expected {
					continue
				}
				receivedD6[senderAddress] = struct{}{}
			}
			debugf(config, "received instance list from %s %s (%d instance(s))", node.Address, node.NodeProfileEOJ, count)
		}
		if len(expectedD6) > 0 && len(receivedD6) == len(expectedD6) {
			return nil
		}
	}
}

func readProperties(ctx context.Context, conn PacketConn, address netip.Addr, destination echonet.EOJ, epcs []byte, tid uint16, config Config) map[byte]Property {
	result := unavailableProperties(epcs, StatusTimeout)
	packet, err := echonet.BuildGetRequest(tid, destination, epcs)
	if err != nil {
		return result
	}
	expected := echonet.GetResponseExpectation(address, tid, destination)
	for attempt := 1; attempt <= config.DataAttempts; attempt++ {
		waitf(config, "waiting up to %s for %s from %s %s", config.DataTimeout, formatEPCs(epcs), address, destination)
		requestCtx, cancel := context.WithTimeout(ctx, config.DataTimeout)
		frame, err := (echonet.Transport{Conn: conn, Debugf: config.Debugf}).SendAndWait(requestCtx, netip.AddrPortFrom(address, Port), packet, expected)
		cancel()
		if err != nil {
			if errors.Is(err, echonet.ErrTimeout) {
				debugf(config, "timed out reading %s from %s %s (attempt %d/%d)", formatEPCs(epcs), address, destination, attempt, config.DataAttempts)
				continue
			}
			debugf(config, "could not read %s from %s %s: %v", formatEPCs(epcs), address, destination, err)
			return result
		}
		responseStatus := StatusNotReturned
		if frame.ESV == echonet.ESVGetSNA {
			responseStatus = StatusSNA
		}
		result = unavailableProperties(epcs, responseStatus)
		for _, property := range frame.Properties {
			if _, requested := result[property.EPC]; !requested {
				continue
			}
			status := StatusOK
			if frame.ESV == echonet.ESVGetSNA && len(property.EDT) == 0 {
				status = StatusSNA
			}
			var edt []byte
			if status == StatusOK {
				edt = cloneEDT(property.EDT)
			}
			result[property.EPC] = Property{EPC: property.EPC, EDT: edt, Status: status}
		}
		return result
	}
	return result
}

func cloneEDT(value []byte) []byte {
	result := make([]byte, len(value))
	copy(result, value)
	return result
}

func unavailableProperties(epcs []byte, status PropertyStatus) map[byte]Property {
	properties := make(map[byte]Property, len(epcs))
	for _, epc := range epcs {
		properties[epc] = Property{EPC: epc, Status: status}
	}
	return properties
}

func validateConfig(config Config) error {
	if config.DiscoveryTimeout <= 0 || config.DataTimeout <= 0 {
		return errors.New("timeouts must be greater than zero")
	}
	if config.DiscoveryAttempts <= 0 || config.DataAttempts <= 0 {
		return errors.New("attempt counts must be greater than zero")
	}
	for _, target := range config.Targets {
		if !target.Is4() {
			return fmt.Errorf("target %s is not an IPv4 address", target)
		}
	}
	return nil
}

func sortedNodes(nodes map[nodeKey]*Node) []Node {
	result := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, *node)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Address != result[j].Address {
			return result[i].Address.Less(result[j].Address)
		}
		return result[i].NodeProfileEOJ.String() < result[j].NodeProfileEOJ.String()
	})
	return result
}

func sortedEOJs(eojs map[echonet.EOJ]struct{}) []echonet.EOJ {
	result := make([]echonet.EOJ, 0, len(eojs))
	for eoj := range eojs {
		result = append(result, eoj)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}

func debugf(config Config, format string, args ...any) {
	if config.Debugf != nil {
		config.Debugf(format, args...)
	}
}

func waitf(config Config, format string, args ...any) {
	if config.Waitf != nil {
		config.Waitf(format, args...)
	}
}

func formatEPCs(epcs []byte) string {
	result := ""
	for index, epc := range epcs {
		if index > 0 {
			result += ","
		}
		result += echonet.FormatEPC(epc)
	}
	return result
}

func isTimeout(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

func senderIPv4(sender *net.UDPAddr) (netip.Addr, bool) {
	if sender == nil {
		return netip.Addr{}, false
	}
	address, ok := netip.AddrFromSlice(sender.IP)
	if !ok || !address.Is4() {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}
