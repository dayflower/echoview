# Metrics instance configuration contract

The YAML document has `instances_format: 2`, optional
`defaults.collection`, and one `instances` sequence. Each entry identifies
exactly one ECHONET Lite object:

```yaml
instances_format: 2
defaults:
  collection:
    interval_seconds: 120
    batch_size: -1
instances:
  - address: 192.168.1.20
    eoj: "0x027D01"
    profile: sharp_storage_battery_jw_wb2521
    metric_prefix: echonet_storage_battery_
    labels:
      node_ip: "192.168.1.20"
      eoj: "0x027D01"
      class: storage_battery
    interval_seconds: 60
    batch_size: 8
    properties:
      - epc: "0xE0"
        # metric_name: remaining_capacity
```

`address`, `eoj`, `metric_prefix`, and `properties` are required. `profile`,
`labels`, `interval_seconds`, and `batch_size` are optional. EOJs and EPCs
use uppercase hexadecimal with the `0x` prefix. `defaults.collection` may set
only `interval_seconds` and `batch_size` for the whole configuration.
Format 1 is not supported; migrate `collection_defaults` to
`defaults.collection` and set `instances_format: 2`.

`metric_prefix` must end with `_`. The exporter constructs a metric name by
concatenating this prefix with a resolved property metric name; it does not add
another separator. `labels`, when present, is a mapping of valid Prometheus
label names to string values. When a configured property resolves to an array
codec, the exporter reserves `item_index` for the zero-based array element
index and rejects a configured label with that name.
`dump --format metrics-config` prepopulates `node_ip`, `eoj`, and `class`.
`class` is the resolved class `short_name`, or the four-digit class EOJ (such as
`0x027D`) when the catalog does not know the class.

Every `properties` item is an object with required `epc` and optional
`metric_name`. This object form is the only valid configuration syntax; a
scalar EPC item is invalid. Duplicate EPCs within an instance are startup
errors. `metric_name`, when specified, is the per-instance metric-name suffix.
The commented value in a generated template is only a suggestion and does not
override the catalog or profile while it remains a comment.

Unknown keys, duplicate `address`/`eoj` pairs, invalid EOJs or EPCs, an unknown
profile, and a profile whose class does not match the EOJ are startup errors.
The resolved profile policy decides whether an EPC is enabled and whether it is
an explicit `request: single` exception. Enabled EPCs without that exception
are put in batches no larger than the resolved `batch_size`; `single` EPCs are
issued as one-EPC GET requests. An explicitly listed EPC that a profile
disables is not requested and has status `skipped`.

The Get, Set, and status-change announcement property maps (`0x9F`, `0x9E`,
and `0x9D`) are not generated as metric properties. Node-profile instances are
likewise omitted from generated metric configuration.

Collection scalars resolve from low to high precedence: application fallback,
`defaults.collection`, profile `collection`, and the instance field.
The final fallback is `interval_seconds: 60` and an unlimited batch. An
`interval_seconds` value must be positive. A positive `batch_size` is the
maximum number of EPCs per GET request; a negative value means all compatible
batch EPCs in one request; zero is invalid. Omitting either field inherits the
lower-precedence value. Profile property policies cannot set an interval or
batch size, and instance properties cannot add collection policy.
