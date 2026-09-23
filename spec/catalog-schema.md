# Echoview catalog schema, version 1

This document defines the YAML catalog consumed by Echoview.  A catalog is a
compiled, runtime-oriented description of ECHONET Lite classes and device
profiles.  It is not an MRA JSON mirror: MRA reference resolution, release
filtering, and source-specific corrections happen when the system catalog is
generated.

`catalog.yaml` contains protocol and device-family information only.  A
particular address, EOJ instance, room, or metric label belongs in
`instances.yaml`, which is specified separately.

## Catalog directories and loading

Catalog documents contain both classes and profiles. Echoview loads catalog
directories in precedence order; later definitions replace or patch earlier
ones. Unless one or more `--catalog-dir DIRECTORY` options are supplied, the
directories are loaded in this order:

1. The OS-wide directory: `/etc/echoview/catalog/` on Linux and other Unix
   systems, `/Library/Application Support/echoview/catalog/` on macOS, and
   `%ProgramData%\\echoview\\catalog\\` on Windows.
2. `catalog/` beside the executable returned by `os.Executable()`. This is not
   relative to the current working directory.
3. `echoview/catalog/` below `os.UserConfigDir()`.

When `--catalog-dir` is supplied, it replaces this entire default list. Its
values are loaded in command-line order. A directory is not traversed
recursively. Its `.yaml` and `.yml` regular files, including symlinks whose
targets are regular files, are loaded in filename order.

A missing directory or a directory with no eligible files produces a warning
on standard error and does not prevent startup. Warnings obey `--quiet`.
Unreadable eligible files, invalid YAML, and schema errors are startup errors.
No readable catalog files is valid: the process uses an empty catalog and
renders unknown values as raw data. `--debug` reports the selected directories,
file order, and merged-definition provenance.

Catalog directories are enumerated and loaded once during process startup.
Echoview does not watch catalog files and has no signal, management endpoint,
or other catalog reload mechanism; restart the process to apply catalog
changes.

There is no catalog-level `extends` or `imports` key in version 1. `extends`
is reserved for class and profile inheritance.

The system catalog is generated from MRA sources. User catalog documents can
add profiles and proprietary properties.
In this repository, run `uv run python tools/generate_echonet_lite_catalog.py
--mra-root /path/to/MRA_v1.4.0` to update the generated base catalog, or add
`--check` to verify it is current. The MRA release directory must be supplied
locally and specified explicitly.
Run `uv run python tools/validate_echonet_lite_catalog.py` to check the
generated YAML against the structural schema without an MRA release. This
check does not resolve inheritance or compare the file with regenerated data.
The generator applies the reviewed Appendix text codecs in
`echonet_lite_text_codec_overrides.yaml`. Use the project Skill
`$echonet-lite-text-codec-overrides` to regenerate that input when updating
the Appendix-derived rules. Run
`uv run python tools/validate_echonet_lite_text_codec_overrides.py` to validate the
overrides document before generating the catalog. It also derives safe
Prometheus export policies from MRA state values and applies reviewed
Appendix exceptions from `echonet_lite_prometheus_overrides.yaml`. Run
`uv run python tools/validate_echonet_lite_prometheus_overrides.py` before adding
an exception. Reviewed counter classifications are stored separately in
`echonet_lite_prometheus_metric_type_overrides.yaml`; validate them with
`uv run python tools/validate_echonet_lite_prometheus_metric_type_overrides.py`.
Its complete format is documented in
[the metric-type override schema](prometheus-metric-type-overrides-schema.md).
Generate unreviewed MRA candidates with
`uv run python tools/generate_echonet_lite_prometheus_metric_type_candidates.py
--mra-root /path/to/MRA_v1.4.0`. Its output is review material only and is
never an input to catalog generation.

Run `uv run python -m unittest discover -s tools -p 'test_*.py'` to test the
generator and document validators. Runtime behavior is tested by the Go
packages that consume the generated catalog.

## Root mapping

Every catalog document has this shape:

```yaml
catalog_format: 1

# Optional documentary metadata.
source:
  mra:
    data_version: "1.3.2"
    release: "R"

classes: {}
profiles: {}
```

`catalog_format` is required and must be the integer `1`. `classes` and
`profiles` are mappings and may be omitted when a document does not contribute
either kind of entry. `source` is documentary metadata only: runtime behavior
does not interpret, compare, merge, or validate its contents. Unknown keys at
every level are ignored, but invalid types and values for known runtime fields
are startup errors. A file contains exactly one YAML document.

## Classes

The keys in `classes` are normalized, four-digit class EOJs in uppercase
hexadecimal, such as `"0x0130"`.  They never include the instance number.

```yaml
classes:
  "0x0000":
    kind: superclass
    name: { ja: "機器オブジェクトスーパークラス" }
    properties:
      "0x80":
        name: { en: "Operation status", ja: "動作状態" }
        access: { get: required, set: required, inf: required }
        codec:
          kinds:
            - kind: enum
              size: 1
              values:
                "0x30": { value: true, label: { en: "ON", ja: "ON" } }
                "0x31": { value: false, label: { en: "OFF", ja: "OFF" } }

  "0x0130":
    name: { en: "Home air conditioner", ja: "家庭用エアコン" }
    short_name: homeAirConditioner
    extends: ["0x0000"]
    properties:
      "0xB0":
        name: { en: "Operation mode setting", ja: "運転モード設定" }
        access: { get: required, set: required, inf: required }
        codec:
          kinds:
            - kind: enum
              size: 1
              values:
                "0x41": { value: auto, label: { en: "Automatic", ja: "自動" } }
                "0x42": { value: cooling, label: { en: "Cooling", ja: "冷房" } }

  "0x0EF0":
    name: { en: "Node profile", ja: "ノードプロファイル" }
    short_name: nodeProfile
    properties:
      "0xD6":
        name: { ja: "自ノードインスタンスリストS" }
        access: { get: required }
        codec: { kinds: [{ kind: instance_list }] }
```

Class fields are:

| Field | Type | Meaning |
| --- | --- | --- |
| `kind` | `superclass` (optional) | Marks a non-instantiable superclass. |
| `name` | language mapping | A mapping containing `ja`, `en`, or both. |
| `short_name` | string | Stable programmatic name. |
| `extends` | list of class EOJs | Parent classes, in precedence order. |
| `properties` | EPC mapping | Properties defined directly by the class. |

Device classes generated from MRA inherit `"0x0000"`.  The node profile
(`"0x0EF0"`) does not inherit the device superclass.  A class inheritance
graph must be acyclic.  Parent properties are resolved first; if a child
defines the same EPC, the child's complete property definition replaces the
parent's definition.

## Profiles

A profile describes a device family, product model, or local variant.  It
selects one ECHONET class and can add proprietary EPCs or collection quirks.
It does not identify a physical device.

```yaml
profiles:
  acme_ac_v2:
    class: "0x0130"
    name: { ja: "ACME エアコン V2" }
    match:
      required:
        "0x8A": ["0x001122"]
      optional:
        "0x8B": ["0x334455"]
        "0x8C": ["0x41432D563200000000000000"]
    properties:
      "0xE1":
        name: { en: "Power consumption", ja: "消費電力" }
        access: { get: optional }
        codec: { kinds: [{ kind: uint, bytes: 2, unit: W }] }
        metric_name: power_consumption
    collection:
      interval_seconds: 60
      batch_size: -1
      properties:
        "0x9F": { enabled: false }
        "0xE1": { request: single }

  acme_ac_v2_quiet:
    extends: [acme_ac_v2]
    name: { ja: "ACME エアコン V2（静音機）" }
    collection:
      interval_seconds: 300
```

Profile fields are:

| Field | Type | Meaning |
| --- | --- | --- |
| `class` | class EOJ | The base ECHONET class. Required unless inherited from a parent profile. |
| `extends` | list of profile IDs | Parent profiles, in precedence order. |
| `name` | language mapping | Profile display name. |
| `match` | profile match rules | Identity checks used to validate profile selection. |
| `properties` | EPC mapping | Proprietary properties or complete replacements for inherited properties. |
| `collection` | collection policy | Device-family collection policy. |

Every resolved profile must have exactly one base `class`. Profile inheritance
must be acyclic. A profile property with `codec.kinds` replaces the complete
inherited property definition for the same EPC. A property containing only
`metric_name` and/or `prometheus` is a metadata patch and retains the catalog
codec.

### Profile metric names

A profile property may specify optional `metric_name`. It is the stable metric
name suffix for that EPC and must be a valid Prometheus metric-name suffix.
It does not include the instance `metric_prefix` or a leading separator. An
instance property's `metric_name` takes precedence over this field. When both
are absent, exporters use the profile property's `short_name`, then the
resolved catalog property's `short_name`, and finally `epc_0xNN` with a
lowercase EPC. This final fallback names a metric only; it does not make an
unknown raw EDT eligible for numeric export.

### Profile match rules

`match` validates that a profile is appropriate for an observed object.  It
does not affect discovery visibility: an object is retained even if no profile
matches.  Its shape is:

```yaml
match:
  required:
    "0x8A": ["0x001122"]
  optional:
    "0x8B": ["0x334455"]
    "0x8C": ["0x41432D563200000000000000"]
```

`required` and `optional` are EPC mappings.  Each value is a non-empty list
of complete EDT byte strings in uppercase hexadecimal (`0x` followed by an
even number of hex digits).  The values for one EPC are alternatives: any one
matching value satisfies that EPC.  An EPC must not appear in both mappings.

Rules compare raw EDT bytes rather than decoded catalog values.  This keeps
matching stable if a codec changes and makes it suitable for binary identifiers
as well as text.  Standard superclass identifiers commonly used here are:

| EPC | Identifier | Standard availability | Typical rule group |
| --- | --- | --- | --- |
| `0x8A` | Member ID / manufacturer code | Get required | `required` |
| `0x8B` | Business facility code | Get optional | `optional` |
| `0x8C` | Product code | Get optional | `optional` |
| `0x8D` | Production number | Get optional | Usually omit; it identifies one physical unit. |

After reading the required identity EPCs, an implementation reports one of
these match states:

| State | Meaning |
| --- | --- |
| `matched` | Every required condition matched and every available optional condition matched. |
| `unknown` | No condition mismatched, but at least one optional condition could not be evaluated because its EPC was unavailable or unreadable. |
| `mismatched` | A retrieved value was not allowed, or a required condition could not be evaluated. |

Profiles are never selected automatically. A profile is used only when an
`instances.yaml` entry names it or `dump --instance-profile` assigns it. Match
rules validate that explicit selection; they do not select candidates and do
not hide a discovered object when validation fails.

## Properties

The keys in `properties` are uppercase hexadecimal EPCs, such as `"0x80"`.
An EPC definition has the following fields:

| Field | Required | Meaning |
| --- | --- | --- |
| `name` | yes | A language mapping with at least `ja` or `en`. |
| `short_name` | no | Stable programmatic name. |
| `access` | no | Mapping of `get`, `set`, and/or `inf` to an MRA access value. |
| `codec` | yes | Declarative decoder and display strategy. |
| `prometheus` | no | Export policy for an enum or raw value. |

Access values are `required`, `optional`, `notApplicable`, `required_c`, and
`required_o`.  They describe the standard or profile expectation only.  The
device's Get property map (`0x9F`) remains the authority for whether an EPC
is actually collected.

Every codec has a non-empty, ordered `kinds` list.  Each entry is a complete
candidate decoder.  The decoder selects the first entry whose EDT length and
value constraints match.  `raw` is a fallback only and must be the final
entry when combined with other kinds.

The loader validates every `kind` recursively, including a single
`array.items` codec and nested `array.items.kinds`. The only supported kinds
are `enum`, `uint`, `int`, `array`, `raw`, `time`, `date`, `date_time`,
`text`, `encoded_text`, `bitmap`, `property_map`, and `instance_list`. An
unknown kind is a startup error that identifies the input file, class or
profile, EPC, and nested codec path where available.

Supported codec kinds in version 1 are:

```yaml
# Exact EDT-to-value mapping.  Keys are complete EDT byte strings.
codec:
  kinds:
    - kind: enum
      size: 1             # optional, in bytes
      values:
        "0x41": { value: auto, label: { en: "Automatic", ja: "自動" } }

# Integers are decoded from a complete big-endian EDT.
codec:
  kinds:
    - kind: uint          # uint or int
      bytes: 2            # 1 through 8
      scale: "0.1"        # optional decimal string; never a YAML float
      offset: "0"         # optional decimal string; never a YAML float
      unit: "°C"          # optional
      minimum: "0"        # optional raw integer value, before scale
      maximum: "500"      # optional raw integer value, before scale

# Array vocabulary follows the MRA names. Every item occupies `itemSize` bytes.
# `items` is either one kind or a nested codec with ordered `kinds`.
codec:
  kinds:
    - kind: array
      itemSize: 2
      minItems: 4
      maxItems: 4
      items: { kind: uint, bytes: 2, unit: W }

# Keep the EDT intact while recording that richer MRA data exists.
codec:
  kinds:
    - kind: raw
      mra_type: bitmap    # optional diagnostic metadata

# Local civil time. Two bytes are hour and minute; three add seconds.
# `maximum_hour: 255` is used by the Appendix's relative-time fields.
codec:
  kinds:
    - kind: time
      bytes: 2
      maximum_hour: 23    # optional; default is 23

# Local calendar values. The year is an unsigned two-byte integer.
codec:
  kinds:
    - kind: date
      bytes: 4            # year(2), month, day
    - kind: date_time
      bytes: 6            # year(2), month, day, hour, minute; 7 adds seconds

# Fixed or bounded text.  Omitted `encoding` means `us-ascii`.
codec:
  kinds:
    - kind: text
      bytes: 12           # or min_bytes and max_bytes
      trim_right: [nul, space] # optional

# Name and address values whose Appendix rule explicitly requires UTF-8.
codec:
  kinds:
    - kind: text
      encoding: utf-8
      min_bytes: 1
      max_bytes: 255
      bom: false

# Character data which declares its encoding in the EDT itself.
codec:
  kinds:
    - kind: encoded_text
      length_byte: 0
      encoding_byte: 1
      reserved_byte: 2
      text_offset: 3
      max_text_bytes: 244
      encodings: { "0x01": us-ascii, "0x08": utf-8 }

# A number plus an MRA-defined sentinel state.
codec:
  kinds:
    - kind: int
      bytes: 1
      unit: Celsius
      minimum: "-127"
      maximum: "125"
    - kind: enum
      size: 1
      values:
        "0x7E": { value: unmeasurable, label: { en: Unmeasurable, ja: 計測不能 } }

# Protocol-specific decoders.
codec: { kinds: [{ kind: bitmap, size: 2, bits: {} }] }
codec: { kinds: [{ kind: property_map }] }
codec: { kinds: [{ kind: instance_list }] }
```

`state` values from MRA compile to `enum`; simple MRA `number` values compile
to `uint` or `int`; MRA `time`, `date`, and `date-time` values compile to the
corresponding temporal kinds; MRA `array` compiles recursively to `array`;
MRA `oneOf` compiles recursively to multiple `kinds`. Time values are local
civil values and deliberately carry no timezone. MRA `object` and unsupported
formats compile to `raw` until a dedicated codec is added. A decoder must
preserve the raw EDT even when it can provide a decoded value.

For `text`, `encoding` is optional and defaults to `us-ascii`; it is written
only when the property has a different fixed encoding.  `encoded_text` has no
fixed encoding: it selects one through the specified EDT byte and `encodings`
mapping.

### Prometheus export policy

Successful `uint` and `int` values are Prometheus gauges unless a property
sets `metric_type: counter`. Text and calendar values are not metrics. A
property-level `prometheus` mapping opts an enum or raw EDT into one of these
explicit representations:

```yaml
# Semantic state values. Unknown EDT values export as -1.
prometheus:
  export: enum_map
  enum_map:
    values:
      "0x30": 1
      "0x31": 0

# A complete fixed-width EDT as an unsigned big-endian state code.
prometheus:
  export: raw_uint
  raw_uint: { bytes: 1 }

# A reviewed, monotonically increasing scalar measurement.
prometheus:
  metric_type: counter
```

`enum_map` values are exactly representable signed integers, except `-1`,
which is reserved for an unmapped EDT. `raw_uint.bytes` is required and must
be from 1 through 6, so the resulting integer is exactly representable by a
Prometheus float64.
`raw_uint` is appropriate for fixed-width string states such as `auto` and
`cooling`, where MRA does not establish portable semantic integers.

The generator emits `enum_map` for boolean and integer MRA state names, and
`raw_uint` only for string-only fixed-width MRA state values. Unsupported or
raw MRA types do not receive an automatic policy. An
`echonet_lite_prometheus_overrides.yaml` entry is an Appendix-derived policy
replacement and must include `x-source` with the Appendix filename, physical
pages, and a concise English source summary.

A profile may attach `prometheus` to an inherited EPC without replacing its
codec. Profile `enum_map.values` merge with the generated map; an existing EDT
entry is replaced. Set `replace: true` alongside `values` to discard the
generated map before applying the profile entries. A profile policy's
`export`, `metric_type`, `raw_uint`, and any explicitly supplied map fields
override the generated policy. `metric_type` may be `gauge` or `counter`; an
omitted value defaults to `gauge`.

## Profile collection policy

`collection` may appear only on a profile. It controls polling; it does not
change the protocol definition of a property. Class-level `collection` and
the `collection.default` level are not part of catalog format 1.

```yaml
collection:
  interval_seconds: 60
  batch_size: 16
  properties:
    "0x9F":
      enabled: false
    "0xE1":
      request: single
```

The profile collection mapping and every entry in `properties` are policy
patches. Its scalar fields are optional and inherit from the less-specific
policy. Property policy supports only `enabled` and `request`; it cannot set
an interval or batch size. Fields are:

| Field | Type | Meaning |
| --- | --- | --- |
| `enabled` | boolean | Whether the EPC is eligible for scheduled collection. |
| `interval_seconds` | positive integer | Instance collection interval. Only valid on the profile collection mapping. |
| `request` | `batch` or `single` | Read with compatible EPCs, or issue one Get request for this EPC. |
| `batch_size` | non-zero integer | A positive value is the maximum EPCs in a batch request. A negative value reads all compatible EPCs in one batch. Only valid on the profile collection mapping; zero is invalid. |

The application resolves the scalar policy from application fallback,
`instances.yaml` `defaults.collection`, profile collection, and instance
fields, in that order. A disabled property is never scheduled, even if an
instance explicitly lists it. A property absent from the device's `0x9F` map
is never scheduled, regardless of profile policy.

## Explicit CLI profile assignment

The Go `echoview dump` command accepts explicit assignments alongside a
profile catalog:

```sh
echoview dump \
  --target '192.168.1.20/0x027D01' \
  --target '192.168.1.20/0x05FF01' \
  --catalog-dir ./catalog \
  --instance-profile '192.168.1.20/0x027D01=sharp_storage_battery_jw_wb2521' \
  --instance-profile '192.168.1.20/0x05FF01=sharp_solar_power_system_controller'
```

Each assignment is an IPv4 address, a six-digit instance EOJ, and a profile
identifier. The command verifies that the profile's class matches the EOJ
class. It then reads every EPC in the profile's `match` rules and compares raw
EDT values. A `mismatched` result is an error; an unavailable optional match
EPC produces `unknown` and keeps the explicitly assigned profile applied. The
selected profile controls property decoding, display, and collection policy
for that EOJ only.

## Catalog merge rules

All catalog documents are merged before class and profile inheritance is
resolved. This permits a later file to define a parent. Catalog definitions
merge by identifier in directory and filename order; later definitions have
higher precedence, including definitions in one directory.

* A class or profile absent from the lower layer is added.
* Scalar fields present in the higher layer replace lower-layer values.
* `extends` replaces the lower-layer list when it is present.
* A class `properties.<epc>` is a complete property definition and replaces
  the lower-layer definition for that EPC.
* A profile `properties.<epc>` with a codec is a complete property definition
  and replaces the lower-layer definition. A profile property containing only
  `metric_name` and/or `prometheus` is a metadata patch.
* `match.required.<epc>` and `match.optional.<epc>` merge by EPC.  A
  higher-layer list replaces the lower-layer list for the same EPC.  A profile
  must still not place one EPC in both groups after inheritance and layering.
* A profile `collection` mapping merges field by field. Its
  `collection.properties.<epc>` entries merge by EPC.
* Version 1 has no deletion syntax.  Set `enabled: false` to suppress
  collection; use a replacement property definition when interpretation must
  differ.

The loader must reject malformed EOJs and EPCs, unknown codec kinds, cyclic
inheritance, profiles without one resolved base class, and conflicting class
values inherited by a profile. Errors include the input file and relevant
class or profile ID, plus the EPC and nested codec path when available.
