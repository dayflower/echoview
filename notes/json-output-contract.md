# JSON output contract

`dump` and `get` output a JSON array of node objects. A node has `address`,
`node_profile_eoj`, `node_profile`, `reported_instance_count`, `instances`,
and `last_seen_unix`. An instance has `eoj`, class metadata, optional
`profile`, and `get_properties`.

Each property has `epc`, `name_ja`, `name_en`, `short_name`, `value`, and
`status`. A retrieved property additionally has `raw_edt` and can have
`value_name_ja` and `unit`. Configured reads from `get` may also include
`value_kind` when the property has a resolved codec kind; the same property
shape appears in successful `watch --format jsonl` results. `raw_edt`, EOJs,
and EPCs use uppercase hexadecimal with an `0x` prefix. `value` is always
present and is `null` whenever a value is unavailable.

`watch --format jsonl` writes one JSON object per collection event. Each object
has `collected_at`, `address`, `eoj`, `succeeded`, and `result`, plus `error`
when a non-empty error is available. `result` is an instance object with the
property shape above when collection succeeds, and `null` when it fails.

The statuses are `ok`, `timeout`, `sna`, `unsupported`, `skipped`,
`decode_error`, and `not_returned`. `skipped` means a profile collection
policy disabled the property, so no ordinary property GET was sent.
`not_returned` means the property was requested but absent from an otherwise
successful response. In a GET_SNA response, a requested EPC with a non-empty
EDT (PDC of at least one) has status `ok` and retains its decoded value. A
requested EPC with an empty EDT, or one absent from that response, has status
`sna` and `value: null`. The same rule applies to discovery and configured
property collection. A GET_RES response continues to use `not_returned` for a
requested EPC that is absent.
