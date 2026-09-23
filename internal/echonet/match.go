package echonet

import "net/netip"

// ResponseExpectation is the complete correlation key for a reply to a
// unicast ECHONET Lite request. A frame must satisfy every populated field.
type ResponseExpectation struct {
	SenderIP    netip.Addr
	TID         uint16
	SEOJ        EOJ
	DEOJ        EOJ
	AllowedESVs map[byte]struct{}
}

// MatchesResponse reports whether a received frame belongs to this request.
func (e ResponseExpectation) MatchesResponse(sender netip.Addr, frame Frame) bool {
	if sender != e.SenderIP || frame.TID != e.TID || frame.SEOJ != e.SEOJ || frame.DEOJ != e.DEOJ {
		return false
	}
	_, allowed := e.AllowedESVs[frame.ESV]
	return allowed
}

// GetResponseExpectation constructs the standard correlation key for GET.
func GetResponseExpectation(sender netip.Addr, tid uint16, destination EOJ) ResponseExpectation {
	return ResponseExpectation{
		SenderIP: sender,
		TID:      tid,
		SEOJ:     destination,
		DEOJ:     ControllerEOJ,
		AllowedESVs: map[byte]struct{}{
			ESVGetRes: {},
			ESVGetSNA: {},
		},
	}
}

// IsInstanceListResponse identifies D5/D6 discovery responses. Discovery may
// arrive as either GET_RES or INF and is deliberately not tied to one sender.
func IsInstanceListResponse(frame Frame, tid uint16) bool {
	return frame.TID == tid &&
		(frame.ESV == ESVGetRes || frame.ESV == ESVInf) &&
		frame.SEOJ[0] == NodeProfileEOJ[0] && frame.SEOJ[1] == NodeProfileEOJ[1] &&
		frame.DEOJ == ControllerEOJ
}
