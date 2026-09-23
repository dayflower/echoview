package main

import (
	"strconv"
)

// optionalInt records whether a command-line integer was explicitly supplied.
type optionalInt struct {
	value int
	set   bool
}

func (v *optionalInt) String() string {
	if v == nil || !v.set {
		return ""
	}
	return strconv.Itoa(v.value)
}

func (v *optionalInt) Set(input string) error {
	value, err := strconv.Atoi(input)
	if err != nil {
		return err
	}
	v.value, v.set = value, true
	return nil
}

func (v optionalInt) pointer() *int {
	if !v.set {
		return nil
	}
	value := v.value
	return &value
}
