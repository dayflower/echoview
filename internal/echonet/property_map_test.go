package echonet

import "testing"

func TestParsePropertyMap(t *testing.T) {
	tests := []struct {
		name string
		edt  []byte
		want []byte
	}{
		{"compact", []byte{3, 0x80, 0x9F, 0xE0}, []byte{0x80, 0x9F, 0xE0}},
		{"bitmap", []byte{16, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1}, []byte{0x88, 0x8F}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParsePropertyMap(test.edt)
			if err != nil {
				t.Fatal(err)
			}
			for _, epc := range test.want {
				if _, ok := got[epc]; !ok {
					t.Fatalf("map %#v does not contain 0x%02X", got, epc)
				}
			}
		})
	}
	for _, edt := range [][]byte{
		nil,
		{1, 0x80, 0x9F},
		{16, 0x80},
	} {
		if _, err := ParsePropertyMap(edt); err == nil {
			t.Fatalf("ParsePropertyMap(% X) succeeded for malformed EDT", edt)
		}
	}
}
