# Prometheus and MQTT contract

The `exporter` command defaults to the loopback listener `127.0.0.1:13610`.
`/metrics` exposes only the most recent complete collection snapshot; it never
triggers ECHONET Lite traffic. `/healthz` returns `200 OK` while the HTTP
server is running.
`metric_prefix` forms the beginning of a metric name, not a label, and ends in
`_`. The final metric name is `metric_prefix + metric_name`. `metric_name` is
resolved in this order: the instance property `metric_name`, the matching
profile property's `metric_name`, the profile property's `short_name`, then the
catalog property's `short_name`. If none resolves to a name, the exporter uses
`epc_0xNN`, with the EPC written in lowercase hexadecimal.
Instance `labels` are attached to ordinary metrics for that instance. For a
numeric array, the exporter emits one sample per element and adds a zero-based
`item_index` label to each sample. Prometheus exports only properties whose
status is `ok` and whose decoded value has an eligible numeric representation.
Every other property is omitted, including a GET_SNA property with an empty EDT
or no returned EPC.
Exporter health metrics are
`echonet_lite_collection_success`,
`echonet_lite_collection_last_success_timestamp_seconds`, and
`echonet_lite_collection_age_seconds`. They use the configured instance labels.
The success metric reports the latest attempt; the timestamp and age retain only
the last successful collection time, never a previous property value. Before a
first successful collection, only the success metric is emitted with value `0`.
Numeric properties are gauges unless their resolved Prometheus policy sets
`metric_type: counter`; counters retain the device-reported cumulative value
and do not require exporter-side state.

The `mqtt` command is the MQTT publisher daemon. It requires `--config` and
`--broker`; broker URLs use `mqtt://` (or `tcp://`) for plain TCP and `mqtts://`
(or `ssl://`) for TLS. It accepts username/password authentication, client ID,
QoS 0–2, retained-message selection, TLS CA/client credentials, and bounded
exponential reconnect delays. The default property topic prefix is
`echonet_lite`, the default client ID is `echoview`, QoS defaults to `1`,
and property payloads are retained by default.

The `value` in MQTT payloads follows the process locale when it is a decoded
enum label. `LC_ALL`, `LC_MESSAGES`, and `LANG` are checked in that order; only
`ja` locales select Japanese, and English is the fallback for every other or
unset locale.

The publisher emits only after a new collection snapshot is complete. A
property topic is deterministic:
`{topic_prefix}/{metric_prefix}/{eoj}/{epc}`. Its JSON payload always contains
`value`, `raw_edt`, `unit`, `collected_at`, and `status`. MQTT publishes every
property status. A property whose status is not `ok` is published with
`value: null` and no raw EDT as a retained message by default, so a retained
old reading cannot be mistaken for the current state. Availability is retained
on `{topic_prefix}/availability`, with `online` after connecting and `offline`
on graceful shutdown or as the MQTT last will. When a broker is unavailable,
ECHONET Lite collection continues. On reconnection, only the latest complete
snapshot is published; older snapshots are not queued.
