package echonet

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type receivedPacket struct {
	packet []byte
	sender net.Addr
}

type fixturePacketConn struct {
	received []receivedPacket
	written  []byte
}

func (c *fixturePacketConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	if len(c.received) == 0 {
		return 0, nil, fixtureTimeoutError{}
	}
	next := c.received[0]
	c.received = c.received[1:]
	return copy(buffer, next.packet), next.sender, nil
}

func (c *fixturePacketConn) WriteTo(packet []byte, _ net.Addr) (int, error) {
	c.written = append([]byte(nil), packet...)
	return len(packet), nil
}

func (c *fixturePacketConn) SetReadDeadline(time.Time) error { return nil }

type fixtureTimeoutError struct{}

func (fixtureTimeoutError) Error() string   { return "fixture timeout" }
func (fixtureTimeoutError) Timeout() bool   { return true }
func (fixtureTimeoutError) Temporary() bool { return true }

func TestSendAndWaitIgnoresUnrelatedPackets(t *testing.T) {
	request, err := BuildGetRequest(0x1234, EOJ{0x02, 0x7D, 0x01}, []byte{0x80})
	if err != nil {
		t.Fatal(err)
	}
	unrelated := mustDecodeHex(t, "10811235027D0105FF017201800130")
	matching := mustDecodeHex(t, "10811234027D0105FF017201800130")
	conn := &fixturePacketConn{received: []receivedPacket{
		{packet: unrelated, sender: net.UDPAddrFromAddrPort(netip.MustParseAddrPort("192.0.2.10:3610"))},
		{packet: matching, sender: net.UDPAddrFromAddrPort(netip.MustParseAddrPort("192.0.2.10:3610"))},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var logs []string
	frame, err := (Transport{Conn: conn, Debugf: func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}}).SendAndWait(
		ctx,
		netip.MustParseAddrPort("192.0.2.10:3610"),
		request,
		GetResponseExpectation(netip.MustParseAddr("192.0.2.10"), 0x1234, EOJ{0x02, 0x7D, 0x01}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if frame.TID != 0x1234 || len(conn.received) != 0 {
		t.Fatalf("frame=%#v remaining=%d", frame, len(conn.received))
	}
	if string(conn.written) != string(request) {
		t.Fatalf("sent packet = %X, want %X", conn.written, request)
	}
	joined := strings.Join(logs, "\n")
	for _, want := range []string{
		"sent UDP packet to 192.0.2.10:3610",
		"received UDP packet from 192.0.2.10:3610",
		"ignored non-matching response from 192.0.2.10:3610",
		"accepted response from 192.0.2.10:3610: TID=0x1234",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("debug logs missing %q:\n%s", want, joined)
		}
	}
}

func TestSendAndWaitReturnsTimeout(t *testing.T) {
	conn := &fixturePacketConn{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := (Transport{Conn: conn}).SendAndWait(
		ctx,
		netip.MustParseAddrPort("192.0.2.10:3610"),
		[]byte{1},
		GetResponseExpectation(netip.MustParseAddr("192.0.2.10"), 1, EOJ{}),
	)
	if err != ErrTimeout {
		t.Fatalf("error = %v, want %v", err, ErrTimeout)
	}
}
