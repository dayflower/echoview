package testfixture

import (
	"net/netip"
	"testing"
	"time"

	"github.com/dayflower/echoview/internal/echonet"
)

func TestPropertyReadConnScriptResults(t *testing.T) {
	request, err := echonet.BuildGetRequest(7, echonet.EOJ{0x02, 0x7D, 0x01}, []byte{0xE0})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		step      PropertyReadStep
		wantWrite bool
		wantRead  bool
	}{
		{"response", PropertyReadStep{Kind: "response", ESV: echonet.ESVGetRes, Properties: []echonet.Property{{EPC: 0xE0, EDT: []byte{0x2A}}}}, true, true},
		{"timeout", PropertyReadStep{Kind: "timeout"}, true, false},
		{"read error", PropertyReadStep{Kind: "read_error"}, true, false},
		{"write error", PropertyReadStep{Kind: "write_error"}, false, false},
		{"unknown", PropertyReadStep{Kind: "unknown"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &PropertyReadConn{Source: netip.MustParseAddr("192.0.2.10"), Device: echonet.EOJ{0x02, 0x7D, 0x01}, Steps: []PropertyReadStep{tc.step}}
			_, writeErr := conn.WriteTo(request, nil)
			if (writeErr == nil) != tc.wantWrite {
				t.Fatalf("WriteTo error = %v, want success=%t", writeErr, tc.wantWrite)
			}
			if !tc.wantWrite {
				return
			}
			buffer := make([]byte, 1024)
			n, _, readErr := conn.ReadFrom(buffer)
			if (readErr == nil) != tc.wantRead {
				t.Fatalf("ReadFrom error = %v, want success=%t", readErr, tc.wantRead)
			}
			if tc.wantRead {
				frame, err := echonet.DecodeFrame(buffer[:n])
				if err != nil || frame.TID != 7 || frame.Properties[0].EPC != 0xE0 {
					t.Fatalf("response = %#v, %v", frame, err)
				}
			}
		})
	}
}

func TestPropertyReadConnDefaultsToTimeout(t *testing.T) {
	conn := &PropertyReadConn{}
	buffer := make([]byte, 1)
	if _, _, err := conn.ReadFrom(buffer); err == nil {
		t.Fatal("ReadFrom returned no error for an empty script")
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.WriteTo([]byte{0x00}, nil); err == nil {
		t.Fatal("WriteTo accepted a malformed frame")
	}
	if len(PropertyReadCases()) == 0 {
		t.Fatal("PropertyReadCases returned no cases")
	}
}
