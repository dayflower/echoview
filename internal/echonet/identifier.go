package echonet

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	// ErrInvalidEOJ identifies a malformed EOJ text value.
	ErrInvalidEOJ = errors.New("invalid EOJ")
	// ErrInvalidEPC identifies a malformed EPC text value.
	ErrInvalidEPC = errors.New("invalid EPC")
	// ErrInvalidClassCode identifies a malformed class-code text value.
	ErrInvalidClassCode = errors.New("invalid class code")
	// ErrInvalidEDT identifies a malformed EDT text value.
	ErrInvalidEDT = errors.New("invalid EDT")
)

// ClassCode identifies an ECHONET Lite object class without its instance
// number.
type ClassCode [2]byte

// String returns the canonical class-code representation.
func (c ClassCode) String() string {
	return fmt.Sprintf("0x%02X%02X", c[0], c[1])
}

// ClassCode returns e without its instance number.
func (e EOJ) ClassCode() ClassCode {
	return ClassCode{e[0], e[1]}
}

// ParseEOJ accepts a lowercase 0x prefix and upper- or lowercase hexadecimal
// digits. It preserves compatibility with CLI, catalog, and profile inputs.
func ParseEOJ(value string) (EOJ, error) {
	parsed, err := parseHex(value, 6, ErrInvalidEOJ)
	if err != nil {
		return EOJ{}, err
	}
	return EOJ{byte(parsed >> 16), byte(parsed >> 8), byte(parsed)}, nil
}

// ParseCanonicalEOJ accepts only the canonical EOJ representation.
func ParseCanonicalEOJ(value string) (EOJ, error) {
	result, err := ParseEOJ(value)
	if err != nil || value != result.String() {
		return EOJ{}, invalid(ErrInvalidEOJ, value)
	}
	return result, nil
}

// ParseClassCode accepts a lowercase 0x prefix and upper- or lowercase
// hexadecimal digits.
func ParseClassCode(value string) (ClassCode, error) {
	parsed, err := parseHex(value, 4, ErrInvalidClassCode)
	if err != nil {
		return ClassCode{}, err
	}
	return ClassCode{byte(parsed >> 8), byte(parsed)}, nil
}

// ParseCanonicalClassCode accepts only the canonical class-code
// representation.
func ParseCanonicalClassCode(value string) (ClassCode, error) {
	result, err := ParseClassCode(value)
	if err != nil || value != result.String() {
		return ClassCode{}, invalid(ErrInvalidClassCode, value)
	}
	return result, nil
}

// FormatEPC returns the canonical EPC representation.
func FormatEPC(epc byte) string {
	return fmt.Sprintf("0x%02X", epc)
}

// ParseEPC accepts a lowercase 0x prefix and upper- or lowercase hexadecimal
// digits.
func ParseEPC(value string) (byte, error) {
	parsed, err := parseHex(value, 2, ErrInvalidEPC)
	return byte(parsed), err
}

// ParseCanonicalEPC accepts only the canonical EPC representation.
func ParseCanonicalEPC(value string) (byte, error) {
	result, err := ParseEPC(value)
	if err != nil || value != FormatEPC(result) {
		return 0, invalid(ErrInvalidEPC, value)
	}
	return result, nil
}

// FormatEDT returns the canonical representation for arbitrary EDT bytes.
func FormatEDT(edt []byte) string {
	return "0x" + strings.ToUpper(fmt.Sprintf("%X", edt))
}

// ParseEDT accepts a lowercase 0x prefix followed by a non-empty, even number
// of hexadecimal digits.
func ParseEDT(value string) ([]byte, error) {
	if !strings.HasPrefix(value, "0x") || len(value) < 4 || len(value)%2 != 0 {
		return nil, invalid(ErrInvalidEDT, value)
	}
	result := make([]byte, (len(value)-2)/2)
	for index := range result {
		parsed, err := strconv.ParseUint(value[2+index*2:4+index*2], 16, 8)
		if err != nil {
			return nil, invalid(ErrInvalidEDT, value)
		}
		result[index] = byte(parsed)
	}
	return result, nil
}

func parseHex(value string, digits int, kind error) (uint64, error) {
	if len(value) != digits+2 || !strings.HasPrefix(value, "0x") {
		return 0, invalid(kind, value)
	}
	parsed, err := strconv.ParseUint(value[2:], 16, digits*4)
	if err != nil {
		return 0, invalid(kind, value)
	}
	return parsed, nil
}

func invalid(kind error, value string) error {
	return fmt.Errorf("%w %q", kind, value)
}
