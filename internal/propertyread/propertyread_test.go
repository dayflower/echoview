package propertyread

import (
	"context"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/model"
	"github.com/dayflower/echoview/internal/testfixture"
)

func TestReadBatchCharacterization(t *testing.T) {
	address := netip.MustParseAddr("192.0.2.10")
	device := echonet.EOJ{0x02, 0x7D, 0x01}
	for _, tc := range testfixture.PropertyReadCases() {
		t.Run(tc.Name, func(t *testing.T) {
			conn := &testfixture.PropertyReadConn{Source: address, Device: device, Steps: tc.Steps}
			properties, nextTID := ReadBatch(context.Background(), conn, address, device, tc.EPCs, 0x1234, Config{
				Timeout:  time.Second,
				Attempts: tc.Attempts,
			})
			if nextTID != 0x1235 {
				t.Fatalf("next TID = 0x%04X, want 0x1235", nextTID)
			}
			if len(conn.Requests) != tc.WantWrites {
				t.Fatalf("request count = %d, want %d", len(conn.Requests), tc.WantWrites)
			}
			for epc, wantStatus := range tc.WantStatuses {
				property := properties[epc]
				if property.Status != model.PropertyStatus(wantStatus) || !reflect.DeepEqual(property.EDT, tc.WantEDTs[epc]) {
					t.Errorf("property 0x%02X = {status:%s edt:% X}, want {status:%s edt:% X}", epc, property.Status, property.EDT, wantStatus, tc.WantEDTs[epc])
				}
			}
		})
	}
}

func TestReadBatchNormalizesResponseStatusesPerProperty(t *testing.T) {
	address := netip.MustParseAddr("192.0.2.10")
	device := echonet.EOJ{0x02, 0x7D, 0x01}
	for _, tc := range []struct {
		name       string
		esv        byte
		properties []echonet.Property
		want       map[byte]model.PropertyStatus
	}{
		{
			name: "GET_SNA distinguishes non-empty empty and absent EPCs",
			esv:  echonet.ESVGetSNA,
			properties: []echonet.Property{
				{EPC: 0xE0, EDT: []byte{0x30}},
				{EPC: 0xE1},
			},
			want: map[byte]model.PropertyStatus{0xE0: model.PropertyOK, 0xE1: model.PropertySNA, 0xE2: model.PropertySNA},
		},
		{
			name: "GET_RES retains empty returned EDT and marks only absent EPC not returned",
			esv:  echonet.ESVGetRes,
			properties: []echonet.Property{
				{EPC: 0xE0},
			},
			want: map[byte]model.PropertyStatus{0xE0: model.PropertyOK, 0xE1: model.PropertyNotReturned},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &testfixture.PropertyReadConn{Source: address, Device: device, Steps: []testfixture.PropertyReadStep{{Kind: "response", ESV: tc.esv, Properties: tc.properties}}}
			properties, _ := ReadBatch(context.Background(), conn, address, device, []byte{0xE0, 0xE1, 0xE2}, 0x1234, Config{Timeout: time.Second, Attempts: 1})
			for epc, want := range tc.want {
				if got := properties[epc].Status; got != want {
					t.Errorf("property 0x%02X status = %q, want %q", epc, got, want)
				}
			}
			if tc.esv == echonet.ESVGetRes && properties[0xE0].EDT == nil {
				t.Fatal("GET_RES with an empty returned EDT must retain the property value")
			}
		})
	}
}
