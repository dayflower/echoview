package echonet

import "fmt"

// ParsePropertyMap decodes the compact and bitmap forms of an ECHONET Lite
// property map (9D, 9E, or 9F). The returned EPCs are de-duplicated.
func ParsePropertyMap(edt []byte) (map[byte]struct{}, error) {
	if len(edt) == 0 {
		return nil, fmt.Errorf("%w: empty property map", ErrInvalidFrame)
	}
	count := int(edt[0])
	properties := make(map[byte]struct{})
	if count <= 15 {
		if len(edt) != count+1 {
			return nil, fmt.Errorf("%w: invalid compact property map length", ErrInvalidFrame)
		}
		for _, epc := range edt[1:] {
			properties[epc] = struct{}{}
		}
		return properties, nil
	}
	if len(edt) != 17 {
		return nil, fmt.Errorf("%w: invalid bitmap property map length", ErrInvalidFrame)
	}
	for lowNibble, bits := range edt[1:] {
		for highOffset := range 8 {
			if bits&(1<<highOffset) != 0 {
				properties[byte((highOffset+8)<<4|lowNibble)] = struct{}{}
			}
		}
	}
	return properties, nil
}
