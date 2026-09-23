// Package echonet provides the ECHONET Lite wire-format and UDP primitives.
package echonet

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidFrame identifies packets that cannot be an ECHONET Lite frame.
	ErrInvalidFrame = errors.New("invalid ECHONET Lite frame")
	// ErrInvalidRequest identifies an attempt to construct an invalid request.
	ErrInvalidRequest = errors.New("invalid ECHONET Lite request")
)

var (
	EHD = [2]byte{0x10, 0x81}

	ControllerEOJ  = EOJ{0x05, 0xFF, 0x01}
	NodeProfileEOJ = EOJ{0x0E, 0xF0, 0x01}
)

const (
	ESVGet    byte = 0x62
	ESVGetSNA byte = 0x52
	ESVGetRes byte = 0x72
	ESVInf    byte = 0x73
)

// EOJ identifies one ECHONET Lite object.
type EOJ [3]byte

// String returns the canonical EOJ representation used by the CLI and JSON.
func (e EOJ) String() string {
	return fmt.Sprintf("0x%02X%02X%02X", e[0], e[1], e[2])
}

// MarshalJSON keeps EOJs as canonical strings instead of JSON byte arrays.
func (e EOJ) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("\"%s\"", e.String())), nil
}

// Property is a single EPC/PDC/EDT item in a frame.
type Property struct {
	EPC byte
	EDT []byte
}

// Frame is an ECHONET Lite format 1 packet.
type Frame struct {
	TID        uint16
	SEOJ       EOJ
	DEOJ       EOJ
	ESV        byte
	Properties []Property
}

// DecodeFrame parses a complete ECHONET Lite packet. Trailing data and
// truncated property data are rejected so callers never act on partial frames.
func DecodeFrame(packet []byte) (Frame, error) {
	if len(packet) < 12 || packet[0] != EHD[0] || packet[1] != EHD[1] {
		return Frame{}, fmt.Errorf("%w: header", ErrInvalidFrame)
	}
	frame := Frame{
		TID:  uint16(packet[2])<<8 | uint16(packet[3]),
		SEOJ: EOJ(packet[4:7]),
		DEOJ: EOJ(packet[7:10]),
		ESV:  packet[10],
	}
	opc := int(packet[11])
	offset := 12
	frame.Properties = make([]Property, 0, opc)
	for range opc {
		if offset+2 > len(packet) {
			return Frame{}, fmt.Errorf("%w: missing EPC or PDC", ErrInvalidFrame)
		}
		epc, pdc := packet[offset], int(packet[offset+1])
		offset += 2
		if offset+pdc > len(packet) {
			return Frame{}, fmt.Errorf("%w: truncated EDT", ErrInvalidFrame)
		}
		edt := append([]byte(nil), packet[offset:offset+pdc]...)
		frame.Properties = append(frame.Properties, Property{EPC: epc, EDT: edt})
		offset += pdc
	}
	if offset != len(packet) {
		return Frame{}, fmt.Errorf("%w: trailing data", ErrInvalidFrame)
	}
	return frame, nil
}

// Encode serializes a frame after checking its field sizes.
func (f Frame) Encode() ([]byte, error) {
	if len(f.Properties) > 255 {
		return nil, fmt.Errorf("%w: too many properties", ErrInvalidRequest)
	}
	packet := make([]byte, 0, 12+len(f.Properties)*2)
	packet = append(packet, EHD[:]...)
	packet = append(packet, byte(f.TID>>8), byte(f.TID))
	packet = append(packet, f.SEOJ[:]...)
	packet = append(packet, f.DEOJ[:]...)
	packet = append(packet, f.ESV, byte(len(f.Properties)))
	for _, property := range f.Properties {
		if len(property.EDT) > 255 {
			return nil, fmt.Errorf("%w: EDT for EPC 0x%02X exceeds 255 bytes", ErrInvalidRequest, property.EPC)
		}
		packet = append(packet, property.EPC, byte(len(property.EDT)))
		packet = append(packet, property.EDT...)
	}
	return packet, nil
}

// BuildGetRequest builds a GET request with one or more EPCs.
func BuildGetRequest(tid uint16, destination EOJ, epcs []byte) ([]byte, error) {
	if len(epcs) == 0 || len(epcs) > 255 {
		return nil, fmt.Errorf("%w: GET must request 1..255 EPCs", ErrInvalidRequest)
	}
	properties := make([]Property, len(epcs))
	for index, epc := range epcs {
		properties[index] = Property{EPC: epc}
	}
	return (Frame{
		TID:        tid,
		SEOJ:       ControllerEOJ,
		DEOJ:       destination,
		ESV:        ESVGet,
		Properties: properties,
	}).Encode()
}

// BuildInstanceListRequest requests D6 from a node profile.
func BuildInstanceListRequest(tid uint16) ([]byte, error) {
	return BuildGetRequest(tid, NodeProfileEOJ, []byte{0xD6})
}

// ParseInstanceList decodes the EDT of a D5 or D6 property.  An instance list
// starts with the number of EOJs and is followed by exactly that many
// three-byte EOJs.
func ParseInstanceList(edt []byte) ([]EOJ, error) {
	if len(edt) == 0 {
		return nil, fmt.Errorf("%w: empty instance list", ErrInvalidFrame)
	}
	count := int(edt[0])
	if count > 84 || len(edt) != 1+count*3 {
		return nil, fmt.Errorf("%w: invalid instance list length", ErrInvalidFrame)
	}
	eojs := make([]EOJ, count)
	for index := range count {
		offset := 1 + index*3
		copy(eojs[index][:], edt[offset:offset+3])
	}
	return eojs, nil
}
