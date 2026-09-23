// Package testfixture provides protocol fixtures shared by package tests.
package testfixture

import (
	"errors"
	"net"
	"net/netip"
	"time"

	"github.com/dayflower/echoview/internal/echonet"
)

// PropertyReadCase describes one wire-level property read behavior.
type PropertyReadCase struct {
	Name         string
	EPCs         []byte
	Attempts     int
	Steps        []PropertyReadStep
	WantWrites   int
	WantStatuses map[byte]string
	WantEDTs     map[byte][]byte
}

// PropertyReadStep controls the result of one request attempt.
type PropertyReadStep struct {
	Kind       string
	ESV        byte
	Properties []echonet.Property
}

// PropertyReadCases returns the shared characterization cases for get and dump.
func PropertyReadCases() []PropertyReadCase {
	return []PropertyReadCase{
		{
			Name: "timeout is retried",
			EPCs: []byte{0xE0}, Attempts: 2,
			Steps: []PropertyReadStep{
				{Kind: "timeout"},
				{Kind: "response", ESV: echonet.ESVGetRes, Properties: []echonet.Property{{EPC: 0xE0, EDT: []byte{0x2A}}}},
			},
			WantWrites: 2, WantStatuses: map[byte]string{0xE0: "ok"}, WantEDTs: map[byte][]byte{0xE0: {0x2A}},
		},
		{
			Name: "communication error is not retried",
			EPCs: []byte{0xE0}, Attempts: 2,
			Steps: []PropertyReadStep{
				{Kind: "read_error"},
				{Kind: "response", ESV: echonet.ESVGetRes, Properties: []echonet.Property{{EPC: 0xE0, EDT: []byte{0x2A}}}},
			},
			WantWrites: 1, WantStatuses: map[byte]string{0xE0: "timeout"},
		},
		{
			Name:     "send error is not retried",
			EPCs:     []byte{0xE0},
			Attempts: 2,
			Steps: []PropertyReadStep{
				{Kind: "write_error"},
				{Kind: "response", ESV: echonet.ESVGetRes, Properties: []echonet.Property{{EPC: 0xE0, EDT: []byte{0x2A}}}},
			},
			WantWrites: 1, WantStatuses: map[byte]string{0xE0: "timeout"},
		},
		{
			Name: "SNA is not retried and accepts non-empty EDT",
			EPCs: []byte{0xE0, 0xE1}, Attempts: 2,
			Steps: []PropertyReadStep{
				{Kind: "response", ESV: echonet.ESVGetSNA, Properties: []echonet.Property{{EPC: 0xE0, EDT: []byte{0x30}}}},
				{Kind: "response", ESV: echonet.ESVGetRes, Properties: []echonet.Property{{EPC: 0xE1, EDT: []byte{0x31}}}},
			},
			WantWrites: 1, WantStatuses: map[byte]string{0xE0: "ok", 0xE1: "sna"}, WantEDTs: map[byte][]byte{0xE0: {0x30}},
		},
		{
			Name: "partial response marks missing EPC not returned",
			EPCs: []byte{0xE0, 0xE1}, Attempts: 2,
			Steps: []PropertyReadStep{
				{Kind: "response", ESV: echonet.ESVGetRes, Properties: []echonet.Property{{EPC: 0xE0, EDT: []byte{0x2A}}}},
			},
			WantWrites: 1, WantStatuses: map[byte]string{0xE0: "ok", 0xE1: "not_returned"}, WantEDTs: map[byte][]byte{0xE0: {0x2A}},
		},
	}
}

type readResult struct {
	packet []byte
	err    error
}

// PropertyReadConn is a script-driven PacketConn for property read tests.
type PropertyReadConn struct {
	Source   netip.Addr
	Device   echonet.EOJ
	Steps    []PropertyReadStep
	Requests [][]byte
	queue    []readResult
}

// WriteTo records the request and enqueues the scripted result for this attempt.
func (c *PropertyReadConn) WriteTo(packet []byte, _ net.Addr) (int, error) {
	frame, err := echonet.DecodeFrame(packet)
	if err != nil {
		return 0, err
	}
	epcs := make([]byte, len(frame.Properties))
	for i, property := range frame.Properties {
		epcs[i] = property.EPC
	}
	c.Requests = append(c.Requests, epcs)
	index := len(c.Requests) - 1
	if index >= len(c.Steps) {
		c.queue = append(c.queue, readResult{err: TimeoutError{}})
		return len(packet), nil
	}
	step := c.Steps[index]
	switch step.Kind {
	case "write_error":
		return 0, errors.New("fixture write error")
	case "read_error":
		c.queue = append(c.queue, readResult{err: errors.New("fixture read error")})
	case "timeout":
		c.queue = append(c.queue, readResult{err: TimeoutError{}})
	case "response":
		response, err := (echonet.Frame{
			TID: frame.TID, SEOJ: c.Device, DEOJ: echonet.ControllerEOJ,
			ESV: step.ESV, Properties: step.Properties,
		}).Encode()
		if err != nil {
			return 0, err
		}
		c.queue = append(c.queue, readResult{packet: response})
	default:
		return 0, errors.New("unknown fixture step")
	}
	return len(packet), nil
}

// ReadFrom returns the next scripted read result.
func (c *PropertyReadConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	if len(c.queue) == 0 {
		return 0, nil, TimeoutError{}
	}
	result := c.queue[0]
	c.queue = c.queue[1:]
	if result.err != nil {
		return 0, nil, result.err
	}
	return copy(buffer, result.packet), net.UDPAddrFromAddrPort(netip.AddrPortFrom(c.Source, 3610)), nil
}

// SetReadDeadline satisfies echonet.PacketConn.
func (c *PropertyReadConn) SetReadDeadline(time.Time) error { return nil }

// TimeoutError is the timeout returned by scripted fixture reads.
type TimeoutError struct{}

func (TimeoutError) Error() string   { return "fixture timeout" }
func (TimeoutError) Timeout() bool   { return true }
func (TimeoutError) Temporary() bool { return true }
