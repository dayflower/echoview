# Prometheus metric-type overrides schema, version 1

`echonet_lite_prometheus_metric_type_overrides.yaml` records reviewed MRA
properties that export as Prometheus counters. It is separate from
`echonet_lite_prometheus_overrides.yaml`, which contains Appendix-derived
numeric representations.

Generate review candidates with:

```sh
uv run python tools/generate_echonet_lite_prometheus_metric_type_candidates.py \
  --mra-root /path/to/MRA_v1.4.0 \
  --output /tmp/prometheus-metric-type-candidates.yaml
```

The candidate file is not a generator input. Review each candidate for a
monotonically increasing scalar measurement, then add approved entries to the
reviewed overrides file. Validate the reviewed file before catalog generation:

```sh
uv run python tools/validate_echonet_lite_prometheus_metric_type_overrides.py
```

The document has this form:

```yaml
prometheus_metric_type_overrides_format: 1
source:
  mra:
    release: R
    data_version: "1.3.2"
    classifier: scalar-measured-cumulative-v1
classes:
  "0x0279":
    properties:
      "0xE1":
        prometheus:
          metric_type: counter
```

`release` and `data_version` must match the MRA source passed to the catalog
generator. `classifier` identifies the candidate-generation rule used during
review. Class and EPC keys use uppercase hexadecimal with the `0x` prefix.
Only `metric_type: counter` is valid in this document; omitted properties keep
the default Prometheus gauge type. Vendor-specific classifications belong in a
profile rather than this MRA-derived input.
