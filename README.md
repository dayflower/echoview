# echoview

`echoview` is a command-line tool for discovering, reading, and exporting
[ECHONET Lite](https://echonet.jp/english/) device data on an IPv4 network. It
turns device properties into human-readable output, JSON, Prometheus metrics,
or retained MQTT messages—without tying collection to a particular vendor or
home-automation platform.

## What it does

- Discovers ECHONET Lite nodes and device instances.
- Reads every advertised readable property from a selected device.
- Decodes values using the bundled Machine Readable Appendix (MRA) catalog.
- Generates a starter configuration for recurring collection.
- Prints a one-time collection, streams periodic results, or exposes them to
  Prometheus and MQTT.
- Supports device profiles for vendor-specific names, codecs, and collection
  rules.

## Requirements

- Go 1.25 or later to build from source.
- An IPv4 network that can reach the target ECHONET Lite devices on UDP port
  `3610`.

The installed catalog directory is loaded by default, so ordinary use does not
require Python, `uv`, or an external MRA download.

## Install

On macOS arm64, install with Homebrew:

```sh
brew install dayflower/tap/echoview
echoview --version
```

The Homebrew package includes the catalog, so no separate catalog setup is
needed. Prebuilt archives and Linux packages are available on
[GitHub Releases](https://github.com/dayflower/echoview/releases), alongside
`checksums.txt`:

| Platform | Artifacts |
| --- | --- |
| Linux amd64 and arm64 | `.tar.gz`, `.deb`, `.rpm` |
| macOS arm64 | `.tar.gz` |

Each archive contains `echoview`, `README.md`, `LICENSE`, and a `catalog/`
directory beside the executable. The archive catalog contains the generated
base catalog and the vendor profiles from `examples/profiles/`. Extract the
archive as a unit so the executable-relative catalog remains available.

The Linux packages install the executable at `/usr/bin/echoview` and both the
base catalog and example vendor profiles at `/etc/echoview/catalog/`. The
profiles become available for explicit assignment; they are never selected
automatically. Catalog files under `/etc/echoview/catalog/` are treated as
package configuration files so local edits are preserved during upgrades.

## Build from source

From the repository root:

```sh
make build
./bin/echoview --version
```

The binary is written to `bin/echoview`, with its packaged catalog in
`bin/catalog/`. At startup, Echoview layers the OS-wide catalog directory,
the executable-relative `catalog/` directory, and the user catalog directory.

## Quick start

The examples below use the installed `echoview` command. If you built from
source, use `./bin/echoview` instead. To choose the local interface used for
ECHONET Lite traffic, pass `--interface` to a command. This is especially
useful on hosts with Wi-Fi, Ethernet, VPN, or container interfaces.

### 1. Discover devices

```sh
echoview discover --interface 192.168.1.10
```

Discovery uses multicast by default. If multicast is unavailable, query one
or more known addresses directly:

```sh
echoview discover --target 192.168.1.20 --target 192.168.1.21
```

### 2. Inspect one device

Use an address to discover and read its node profile and device instances:

```sh
echoview dump --target 192.168.1.20
```

To read a particular object only, include its EOJ. EOJs use uppercase
hexadecimal with a `0x` prefix:

```sh
echoview dump --target 192.168.1.20/0x027D01 --show-raw
```

`dump` accepts `--format text`, `--format json`, and
`--format metrics-config`.

### 3. Generate a collection configuration

Generate a starter file from an inspected device, then review the selected
properties and metric names before using it in production:

```sh
echoview dump \
  --target 192.168.1.20 \
  --format metrics-config > instances.yaml
```

### 4. Read configured properties once

```sh
echoview get --config instances.yaml
echoview get --config instances.yaml --format json
```

## Recurring collection

All recurring commands use the same `instances.yaml` configuration. Collection
defaults can be shared at the root, while an instance can override its own
interval and batch size.

### Watch the terminal

```sh
echoview watch --config instances.yaml
echoview watch --config instances.yaml --format jsonl
```

### Expose Prometheus metrics

```sh
echoview exporter --config instances.yaml
curl http://127.0.0.1:13610/metrics
```

The exporter listens on `127.0.0.1:13610` by default and serves `/metrics`
and `/healthz`. Metric scrapes read the latest completed collection snapshot;
they never initiate ECHONET Lite traffic themselves.

To listen elsewhere, specify both the address and port as needed:

```sh
echoview exporter \
  --config instances.yaml \
  --listen-address 0.0.0.0 \
  --listen-port 13610
```

Binding to a non-loopback address makes metrics reachable by other hosts. Put
an appropriate firewall or reverse proxy in front of that endpoint.

### Publish to MQTT

```sh
echoview mqtt \
  --config instances.yaml \
  --broker mqtts://broker.example.net:8883 \
  --username echoview \
  --password 'change-me'
```

MQTT property messages are retained by default. Topics follow this shape:

```text
{topic-prefix}/{metric-prefix}/{eoj}/{epc}
```

The default topic prefix is `echonet_lite`; use `--topic-prefix` to change it.
The availability topic defaults to `{topic-prefix}/availability`.

## Configuration

`get`, `watch`, `exporter`, and `mqtt` consume an instances configuration in
format 2. A minimal example looks like this:

```yaml
instances_format: 2
defaults:
  collection:
    interval_seconds: 60
    batch_size: -1
instances:
  - address: "192.168.1.20"
    eoj: "0x027D01"
    metric_prefix: "echonet_storage_battery_"
    labels:
      node_ip: "192.168.1.20"
      eoj: "0x027D01"
      class: "storage_battery"
    interval_seconds: 60
    batch_size: 8
    properties:
      - epc: "0xE0"
        metric_name: remaining_capacity
```

`address`, `eoj`, `metric_prefix`, and `properties` are required.
`interval_seconds` and `batch_size` inherit from `defaults.collection`, a
selected profile, or the application fallback when omitted. `metric_prefix`
must end in `_`; the exporter creates metric names by appending the resolved
property name. Property items must use the object form shown above, even when
only `epc` is set.

Format 1 configurations are not supported. Move `collection_defaults` into
`defaults.collection` and set `instances_format: 2` when migrating.

For the complete schema and resolution rules, see
[the metrics configuration contract](notes/metrics-config-contract.md).

### Catalogs and profiles

Catalog documents combine class definitions and optional vendor profiles. By
default, Echoview reads the OS-wide catalog directory, `catalog/` beside the
executable, and the user catalog directory, in that order. To use a different
set, repeat `--catalog-dir`; supplied directories replace the defaults and are
merged in the specified order:

```sh
echoview dump \
  --catalog-dir /opt/echoview/base \
  --catalog-dir ./local-catalog \
  --target 192.168.1.20
```

Missing or empty catalog directories produce warnings only. If no catalog file
is readable, Echoview continues in raw mode. Catalog files are read once when
the process starts; restart a long-running command after changing a catalog.

A profile is never chosen automatically. To use a vendor-specific profile for
matching, decoding, or collection rules, assign its ID in an instance:

```yaml
profile: sharp_storage_battery_jw_wb2521
```

For a one-off `dump`, use a catalog directory containing that profile and an
explicit assignment:

```sh
echoview dump \
  --target 192.168.1.20/0x027D01 \
  --catalog-dir ./catalog \
  --instance-profile 192.168.1.20/0x027D01=sharp_storage_battery_jw_wb2521
```

## Output, retries, and logging

- Requested results are always written to standard output. Diagnostics and
  non-fatal warnings go to standard error, keeping JSON and JSONL safe for
  pipelines.
- `--quiet` suppresses non-fatal diagnostics; `--debug` enables detailed
  protocol logging.
- `--data-timeout` and `--data-attempts` control property reads. An attempt
  count is a total, not an additional retry count.
- `--discovery-timeout` and `--discovery-attempts` control multicast discovery
  for `discover` and address-only `dump` operations.
- Periodic commands open UDP port `3610` for each collection by default. Use
  `--keep-binding` when a long-lived binding is required by your environment.

Decoded text follows the process locale: `ja` locales select Japanese labels;
English is the fallback. JSON retains both Japanese and English catalog names.

The documented exit codes are `0` for completed work (including partial
results), `2` for invalid command-line input, `3` for invalid startup
configuration, and `4` for fatal runtime failures.

## Reference

- [CLI contract](notes/cli-contract.md) — commands, flags, streams, and exit codes.
- [JSON output contract](notes/json-output-contract.md) — machine-readable result shape.
- [Prometheus and MQTT contract](notes/publish-contract.md) — publication semantics and topics.
- [Catalog schema](spec/catalog-schema.md) — generated catalog format.
- [Development guide](notes/development.md) — local checks, catalog generation, and releases.

Run `echoview <command> --help` for the authoritative option list.

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE).
