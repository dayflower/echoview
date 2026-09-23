package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPropertyResultKeepsUnavailableValueAsNull(t *testing.T) {
	encoded, err := json.Marshal(PropertyResult{EPC: "0x80", Status: PropertyTimeout})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "\"value\":null") {
		t.Fatalf("JSON = %s; unavailable value must be null", encoded)
	}
}
