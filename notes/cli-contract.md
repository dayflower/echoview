# CLI contract

The executable is named `echoview`. It has the subcommands `discover`,
`dump`, `get`, `watch`, `exporter`, and `mqtt`.
`prometheus` is an accepted alias for `exporter`, and `publisher` is an
accepted alias for `mqtt`.

All commands write requested results only to standard output. Non-fatal
diagnostics are written only to standard error and use the timestamped levels
`[waiting]`, `[info]`, `[warning]`, and `[debug]`. `--quiet` suppresses the
non-debug levels. `--debug` enables detailed `[debug]` logs even when `--quiet`
is also set. Errors always use standard error and are never suppressed.
Machine-readable formats must never be mixed with logs.

`discover` accepts `--interface ADDRESS`, repeatable `--target ADDRESS`,
`--discovery-timeout DURATION` (default `20s`), and
`--discovery-attempts COUNT` (default `1`). It also accepts
`--data-timeout DURATION` (default `20s`) and `--data-attempts COUNT`
(default `1`) for the basic node and instance properties displayed by that
command. Explicit targets use one discovery attempt; the discovery-attempts
setting applies only to multicast discovery.

Every catalog-using command accepts repeatable `--catalog-dir DIRECTORY`.
When it is omitted, Echoview loads the OS-wide directory, the `catalog/`
directory beside the executable, and the user configuration directory in that
order. When it is present, the supplied directories replace the default list
and are read in command-line order. Each directory contributes only its
non-recursive, filename-sorted `.yaml` and `.yml` regular files (including
symlinks to regular files). `--catalog` and `--profiles` are not supported.

A missing directory or one containing no eligible catalog files emits a
warning to standard error and collection continues; `--quiet` suppresses that
warning. No readable catalog is valid and uses an empty catalog with raw
rendering for unknown values. An unreadable eligible file, invalid YAML, or
schema error is a startup configuration error (exit code 3). `--debug` shows
the selected directories, file order, and merged-definition provenance.
Catalog directories are loaded once during startup. `watch`, `exporter`, and
`mqtt` do not monitor or reload them; restart the process to use changed
catalogs.

`dump`, `get`, `watch`, `exporter`, and `mqtt` accept `--data-timeout DURATION`
(default `20s`) and `--data-attempts COUNT` (default `1`) for data GETs.
Commands that also discover instances additionally accept the discovery
options. An attempt is a total attempt count, not an extra retry count.

`dump` requires one or more `--target` values. A target is either an IPv4
address, which reads the node profile and every discovered instance at that
address, or `ADDRESS/0xGGCCII`, which reads only that instance. It first reads
the target's Get property map (`0x9F`) and then requests every advertised EPC.
An `ADDRESS/EOJ` target skips discovery entirely. An address-only target reads
the self-node instance list (`0xD6`) and stops waiting once every explicitly
targeted address has supplied a valid `0xD6` response.
`--batch-size` sets the maximum number of normal EPCs in a Get request. When
omitted, it inherits an explicitly assigned profile's batch policy and then
the negative application fallback, which requests all compatible EPCs
together. A negative explicit value has the same meaning, a positive value is
the maximum, and zero is invalid. An explicit value takes precedence over
profile batch size. `--instance-delay` waits between objects at the same
address.

`dump --format` accepts `text` (the default), `json`, and `metrics-config`.
The latter emits a format-2 `instances.yaml` starter document with the
discovered address, EOJ, advertised metric EPCs, a generated metric prefix
ending in `_`, `node_ip`/`eoj`/`class` labels, and a 60-second default
collection interval. It
omits node-profile objects and the `0x9D`/`0x9E`/`0x9F` property maps. Each generated property uses the
object form with an `epc` key and, where available, a commented suggested
`metric_name` resolved from the selected profile or catalog. Repeatable
`--instance-profile ADDRESS/0xGGCCII=PROFILE_ID` applies an explicit profile;
the profile class must match the EOJ and its raw match properties are checked
before the profile is used for decoding and collection policy. A profile that
merely exists in a catalog directory is never selected automatically.

The Get and Set property maps (`0x9F` and `0x9E`) are not included in normal
value-read batches. Text output retains them as property-map headings rather
than marking them `not_returned`; `--show-raw` adds `0x9F`'s raw EDT to its
heading. JSON always retains raw EDT in its `raw_edt` field.

`get --config PATH` reads every instance in a format-2 metrics configuration
file once. It requests and outputs only the configured properties, except that
an explicit profile's match properties are read first and are not output. A
profile mismatch is an error. The command applies the configured profile when
decoding values and accepts `text` (the default) or `json` output. It does not
use `interval_seconds`, but it uses the same resolved batch policy as periodic
collection.

Text output and decoded enum labels use the process locale. The command checks
`LC_ALL`, then `LC_MESSAGES`, then `LANG`; `ja` locales select Japanese labels
and all other, unsupported, or unset locales select English. If that label is
unavailable, the other catalog translation is used. JSON retains both catalog
translations for class and property names.

`exporter --config PATH` starts that periodic-collection runtime and serves its
snapshots as Prometheus text at `/metrics`. It performs no ECHONET Lite traffic
while handling a scrape. It also serves `/healthz`. The default listener is
`127.0.0.1:13610`; `--listen-address ADDRESS` and `--listen-port PORT` select a
different address and port. It accepts the same `--interface`,
`--catalog-dir`, data timeout, and data attempt options as `get`.

By default, `watch`, `exporter`, and `mqtt` open UDP port 3610 immediately
before each instance collection and close it after all responses, retries, and
timeouts for that collection have finished. `--keep-binding` preserves the
legacy behavior of binding the port for the lifetime of the command.

Before the first periodic collection of an instance with an explicit profile,
`watch`, `exporter`, and `mqtt` read the profile's match properties once. A
matched or unknown profile continues collection. A mismatch permanently
disables only that instance for the process lifetime, writes an info log, and
removes it from subsequent scheduler snapshots.

Only a timeout consumes another attempt. A send/receive error, malformed reply,
or SNA is recorded and does not retry. A partial reply is a successful reply;
properties absent from it are marked `not_returned`. Properties disabled by a
profile collection policy are not requested and are marked `skipped`.

Exit code 0 means the command completed, including partial node/property
results. Exit code 2 means invalid command-line input. Exit code 3 means an
invalid startup configuration or unreadable required file. Exit code 4 means a
fatal runtime failure that prevented the command from producing its intended
result. `--help` and `--version` write to standard output and exit 0.
