package echonet

import "testing"

func TestIdentifierParsers(t *testing.T) {
	cases := []struct {
		name    string
		parse   func(string) error
		valid   []string
		invalid []string
	}{
		{
			name:    "EOJ",
			parse:   func(value string) error { _, err := ParseEOJ(value); return err },
			valid:   []string{"0x000000", "0xFFFFFF", "0x027d01"},
			invalid: []string{"027D01", "0X027D01", "0x027D0", "0x027D010", "0x027G01"},
		},
		{
			name:    "class code",
			parse:   func(value string) error { _, err := ParseClassCode(value); return err },
			valid:   []string{"0x0000", "0xFFFF", "0x027d"},
			invalid: []string{"027D", "0X027D", "0x027", "0x027D0", "0x02Gd"},
		},
		{
			name:    "EPC",
			parse:   func(value string) error { _, err := ParseEPC(value); return err },
			valid:   []string{"0x00", "0xFF", "0xe0"},
			invalid: []string{"E0", "0XE0", "0xE", "0xE00", "0xGG"},
		},
		{
			name:    "EDT",
			parse:   func(value string) error { _, err := ParseEDT(value); return err },
			valid:   []string{"0x00", "0xFF", "0x0a1B"},
			invalid: []string{"00", "0X00", "0x", "0x0", "0xGG"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, value := range tc.valid {
				if err := tc.parse(value); err != nil {
					t.Errorf("parse(%q) = %v, want success", value, err)
				}
			}
			for _, value := range tc.invalid {
				if err := tc.parse(value); err == nil {
					t.Errorf("parse(%q) succeeded, want error", value)
				}
			}
		})
	}
}

func TestCanonicalIdentifierParsersAndFormatters(t *testing.T) {
	if _, err := ParseCanonicalEOJ("0x027D01"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCanonicalEOJ("0x027d01"); err == nil {
		t.Fatal("ParseCanonicalEOJ accepted lowercase input")
	}
	if _, err := ParseCanonicalClassCode("0x027D"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCanonicalClassCode("0x027d"); err == nil {
		t.Fatal("ParseCanonicalClassCode accepted lowercase input")
	}
	if _, err := ParseCanonicalEPC("0xE0"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCanonicalEPC("0xe0"); err == nil {
		t.Fatal("ParseCanonicalEPC accepted lowercase input")
	}
	if got := (EOJ{0x02, 0x7D, 0x01}).ClassCode().String(); got != "0x027D" {
		t.Fatalf("ClassCode().String() = %q", got)
	}
	if got := FormatEPC(0xE0); got != "0xE0" {
		t.Fatalf("FormatEPC() = %q", got)
	}
	if got := FormatEDT([]byte{0x0A, 0x1B}); got != "0x0A1B" {
		t.Fatalf("FormatEDT() = %q", got)
	}
}
