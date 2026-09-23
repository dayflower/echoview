package echonet

import (
	"net/netip"
	"testing"
)

func TestGetResponseExpectation(t *testing.T) {
	sender := netip.MustParseAddr("192.0.2.10")
	expectation := GetResponseExpectation(sender, 0x1234, EOJ{0x02, 0x7D, 0x01})
	matching := Frame{TID: 0x1234, SEOJ: EOJ{0x02, 0x7D, 0x01}, DEOJ: ControllerEOJ, ESV: ESVGetRes}
	if !expectation.MatchesResponse(sender, matching) {
		t.Fatal("matching response was rejected")
	}
	for name, frame := range map[string]Frame{
		"tid":  {TID: 1, SEOJ: matching.SEOJ, DEOJ: matching.DEOJ, ESV: matching.ESV},
		"seoj": {TID: matching.TID, SEOJ: EOJ{0x02, 0x7D, 0x02}, DEOJ: matching.DEOJ, ESV: matching.ESV},
		"deoj": {TID: matching.TID, SEOJ: matching.SEOJ, DEOJ: EOJ{0x05, 0xFF, 0x02}, ESV: matching.ESV},
		"esv":  {TID: matching.TID, SEOJ: matching.SEOJ, DEOJ: matching.DEOJ, ESV: ESVInf},
	} {
		t.Run(name, func(t *testing.T) {
			if expectation.MatchesResponse(sender, frame) {
				t.Fatal("unrelated response was accepted")
			}
		})
	}
	if expectation.MatchesResponse(netip.MustParseAddr("192.0.2.11"), matching) {
		t.Fatal("response from another sender was accepted")
	}
}

func TestIsInstanceListResponse(t *testing.T) {
	frame := Frame{TID: 7, SEOJ: NodeProfileEOJ, DEOJ: ControllerEOJ, ESV: ESVInf}
	if !IsInstanceListResponse(frame, 7) {
		t.Fatal("valid INF instance-list response was rejected")
	}
	frame.DEOJ = EOJ{0x05, 0xFF, 0x02}
	if IsInstanceListResponse(frame, 7) {
		t.Fatal("unrelated discovery response was accepted")
	}
}
