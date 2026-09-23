# ECHONET Lite text codec overrides schema, version 1

`echonet_lite_text_codec_overrides.yaml` is the Appendix-derived input used
when compiling the system catalog. Validate it with:

```sh
uv run python tools/validate_echonet_lite_text_codec_overrides.py
```

The validator checks document structure and codec syntax. It does not verify
the Appendix interpretation or MRA membership; catalog generation performs
the latter integration check.

## Root mapping

The root requires these keys:

```yaml
codec_overrides_format: 1
source:
  appendix:
    document: Appendix_Release_R_r4.pdf
    release: R rev.4
selection:
  classes: [superClass, devices]
  exclude: [nodeProfile]
  rule: Text codecs selected from the Appendix and checked against MRA availability.
classes: {}
```

`source.appendix.document` is a filename, not a path. `classes` maps uppercase
four-digit `0xNNNN` EOJs to a `properties` mapping. Each property key is an
uppercase `0xNN` EPC and requires `name`, `codec`, and `x-source.appendix`.

## Codec kinds

`codec.kinds` is a non-empty list. The supported kinds are:

- `text`: specify either a positive `bytes` value or positive `min_bytes` and
  / or `max_bytes` values. Optional `encoding`, boolean `bom`, and
  `trim_right` values (`nul` and `space`) describe text handling.
- `encoded_text`: specify distinct `length_byte`, `encoding_byte`, and
  `reserved_byte` offsets; a later `text_offset`; positive `max_text_bytes`;
  and a non-empty mapping of uppercase `0xNN` selectors to encoding names.

`x-source.appendix` requires the Appendix filename, a non-empty list of unique
positive physical PDF page numbers, and a concise summary. Unknown normal keys
are rejected; extension keys beginning with `x-` are allowed.
