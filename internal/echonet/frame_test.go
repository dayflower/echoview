package echonet

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type fixtureProperty struct {
	EPC byte   `json:"epc"`
	EDT string `json:"edt"`
}

type protocolFixture struct {
	Name       string            `json:"name"`
	Packet     string            `json:"packet"`
	Valid      bool              `json:"valid"`
	TID        uint16            `json:"tid"`
	SEOJ       string            `json:"seoj"`
	DEOJ       string            `json:"deoj"`
	ESV        byte              `json:"esv"`
	Properties []fixtureProperty `json:"properties"`
}

func TestProtocolFixtures(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "protocol_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []protocolFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			packet, err := hex.DecodeString(fixture.Packet)
			if err != nil {
				t.Fatal(err)
			}
			frame, err := DecodeFrame(packet)
			if !fixture.Valid {
				if err == nil {
					t.Fatal("DecodeFrame accepted an invalid fixture")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if frame.TID != fixture.TID || frame.SEOJ.String() != fixture.SEOJ || frame.DEOJ.String() != fixture.DEOJ || frame.ESV != fixture.ESV {
				t.Fatalf("decoded frame = %#v", frame)
			}
			if len(frame.Properties) != len(fixture.Properties) {
				t.Fatalf("property count = %d, want %d", len(frame.Properties), len(fixture.Properties))
			}
			for index, expected := range fixture.Properties {
				wantEDT, err := hex.DecodeString(expected.EDT)
				if err != nil {
					t.Fatal(err)
				}
				got := frame.Properties[index]
				if got.EPC != expected.EPC || string(got.EDT) != string(wantEDT) {
					t.Fatalf("property %d = %#v, want EPC=0x%02X EDT=%X", index, got, expected.EPC, wantEDT)
				}
			}
			encoded, err := frame.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != string(packet) {
				t.Fatalf("Encode() = %X, want %X", encoded, packet)
			}
		})
	}
}

func TestBuildGetRequest(t *testing.T) {
	packet, err := BuildGetRequest(0x1234, EOJ{0x02, 0x7D, 0x01}, []byte{0x80, 0x9F})
	if err != nil {
		t.Fatal(err)
	}
	const want = "1081123405FF01027D01620280009F00"
	if got := string(packet); got != string(mustDecodeHex(t, want)) {
		t.Fatalf("BuildGetRequest() = %X, want %s", packet, want)
	}
	if _, err := BuildGetRequest(0, EOJ{}, nil); err == nil {
		t.Fatal("BuildGetRequest accepted no EPCs")
	}
}

func TestParseInstanceListRejectsMalformedLengthAndMaximum(t *testing.T) {
	eojs, err := ParseInstanceList([]byte{2, 0x01, 0x30, 0x01, 0x05, 0xFF, 0x01})
	if err != nil || len(eojs) != 2 || eojs[0] != (EOJ{0x01, 0x30, 0x01}) || eojs[1] != (EOJ{0x05, 0xFF, 0x01}) {
		t.Fatalf("ParseInstanceList() = %#v, %v", eojs, err)
	}
	for _, edt := range [][]byte{
		{2, 0x01, 0x30, 0x01},
		append([]byte{85}, make([]byte, 85*3)...),
	} {
		if _, err := ParseInstanceList(edt); err == nil {
			t.Fatalf("ParseInstanceList(% X) accepted malformed EDT", edt)
		}
	}
}

func mustDecodeHex(t *testing.T, value string) []byte {
	t.Helper()
	result, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
