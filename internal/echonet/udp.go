package echonet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// ErrTimeout means no matching response arrived before the caller's deadline.
var ErrTimeout = errors.New("ECHONET Lite response timed out")

// PacketConn is the subset of net.PacketConn used by Transport.
type PacketConn interface {
	ReadFrom([]byte) (int, net.Addr, error)
	WriteTo([]byte, net.Addr) (int, error)
	SetReadDeadline(time.Time) error
}

// Transport sends ECHONET Lite UDP packets and filters unrelated traffic.
type Transport struct {
	Conn   PacketConn
	Debugf func(string, ...any)
}

// SendAndWait sends packet once and returns the first fully matching response.
// Malformed and unrelated packets are ignored until ctx expires.
func (t Transport) SendAndWait(ctx context.Context, target netip.AddrPort, packet []byte, expected ResponseExpectation) (Frame, error) {
	if t.Conn == nil {
		return Frame{}, errors.New("ECHONET Lite transport has no packet connection")
	}
	if _, err := t.Conn.WriteTo(packet, net.UDPAddrFromAddrPort(target)); err != nil {
		return Frame{}, fmt.Errorf("send ECHONET Lite packet: %w", err)
	}
	t.debugf("sent UDP packet to %s", target)

	buffer := make([]byte, 65535)
	for {
		if deadline, ok := ctx.Deadline(); ok {
			if err := t.Conn.SetReadDeadline(deadline); err != nil {
				return Frame{}, fmt.Errorf("set ECHONET Lite read deadline: %w", err)
			}
		}
		n, sender, err := t.Conn.ReadFrom(buffer)
		if err != nil {
			if ctx.Err() != nil || isTimeout(err) {
				t.debugf("timed out waiting for response from %s", target)
				return Frame{}, ErrTimeout
			}
			return Frame{}, fmt.Errorf("receive ECHONET Lite packet: %w", err)
		}
		senderAddr, ok := sender.(*net.UDPAddr)
		if !ok {
			t.debugf("ignored UDP packet from non-UDP sender %v", sender)
			continue
		}
		t.debugf("received UDP packet from %s", senderAddr.AddrPort())
		frame, err := DecodeFrame(buffer[:n])
		if err != nil {
			t.debugf("ignored malformed ECHONET Lite packet from %s: %v", senderAddr.AddrPort(), err)
			continue
		}
		if !expected.MatchesResponse(senderAddr.AddrPort().Addr(), frame) {
			t.debugf("ignored non-matching response from %s: %s", senderAddr.AddrPort(), formatFrame(frame))
			continue
		}
		t.debugf("accepted response from %s: %s", senderAddr.AddrPort(), formatFrame(frame))
		return frame, nil
	}
}

func (t Transport) debugf(format string, args ...any) {
	if t.Debugf != nil {
		t.Debugf(format, args...)
	}
}

func formatFrame(frame Frame) string {
	properties := ""
	for index, property := range frame.Properties {
		if index > 0 {
			properties += ", "
		}
		properties += fmt.Sprintf("%s=%s", FormatEPC(property.EPC), FormatEDT(property.EDT))
	}
	return fmt.Sprintf("TID=0x%04X SEOJ=%s DEOJ=%s ESV=0x%02X properties=[%s]", frame.TID, frame.SEOJ, frame.DEOJ, frame.ESV, properties)
}

func isTimeout(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}
