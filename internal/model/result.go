// Package model contains stable results shared by every command and daemon.
package model

import "github.com/dayflower/echoview/internal/echonet"

// PropertyStatus explains why a property has, or does not have, a value.
type PropertyStatus string

const (
	PropertyOK          PropertyStatus = "ok"
	PropertyTimeout     PropertyStatus = "timeout"
	PropertySNA         PropertyStatus = "sna"
	PropertyUnsupported PropertyStatus = "unsupported"
	PropertySkipped     PropertyStatus = "skipped"
	PropertyDecodeError PropertyStatus = "decode_error"
	PropertyNotReturned PropertyStatus = "not_returned"
)

// PropertyResult preserves the raw EDT, optional decoded value, and selected
// codec kind. Value is intentionally not omitted: unavailable values are
// encoded as null.
type PropertyResult struct {
	EPC         string         `json:"epc"`
	NameJA      *string        `json:"name_ja"`
	NameEN      *string        `json:"name_en"`
	ShortName   *string        `json:"short_name"`
	RawEDT      *string        `json:"raw_edt,omitempty"`
	Value       any            `json:"value"`
	ValueNameJA *string        `json:"value_name_ja,omitempty"`
	Unit        *string        `json:"unit,omitempty"`
	ValueKind   string         `json:"value_kind,omitempty"`
	Status      PropertyStatus `json:"status"`
}

// ProfileResult identifies an explicitly applied catalog profile.
type ProfileResult struct {
	ID         string  `json:"id"`
	NameJA     *string `json:"name_ja"`
	NameEN     *string `json:"name_en"`
	MatchState string  `json:"match_state"`
}

// InstanceResult is one node-profile or device instance result.
type InstanceResult struct {
	EOJ            echonet.EOJ      `json:"eoj"`
	ClassName      *string          `json:"class_name"`
	ClassNameEN    *string          `json:"class_name_en"`
	ClassShortName *string          `json:"class_short_name"`
	Profile        *ProfileResult   `json:"profile"`
	GetProperties  []PropertyResult `json:"get_properties"`
}

// NodeResult is the JSON-compatible result for a discovered or queried node.
type NodeResult struct {
	Address               string           `json:"address"`
	NodeProfileEOJ        echonet.EOJ      `json:"node_profile_eoj"`
	NodeProfile           InstanceResult   `json:"node_profile"`
	ReportedInstanceCount *int             `json:"reported_instance_count"`
	Instances             []InstanceResult `json:"instances"`
	LastSeenUnix          int64            `json:"last_seen_unix"`
}
