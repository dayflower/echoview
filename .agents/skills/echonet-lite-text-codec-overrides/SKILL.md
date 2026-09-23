---
name: echonet-lite-text-codec-overrides
description: "Generate echonet_lite_text_codec_overrides.yaml from an ECHONET Lite Appendix PDF and its matching MRA release. Use when refreshing the repository's Appendix-derived text codecs; do not use for ordinary catalog generation."
---

# ECHONET Lite Text Codec Overrides

Generate the committed `echonet_lite_text_codec_overrides.yaml` directly. This
file is the maintained result of interpreting the Appendix; there is no
intermediate rule table or generator script.

## Inputs

Use the Appendix PDF and MRA release named by the user.

The Appendix is authoritative for text representation. The MRA is
authoritative for whether an EOJ/EPC exists in the selected release and for
localized property names. Restrict the selection to `superClass` and
`devices`; exclude `nodeProfile`.

## Generate the overrides

Start with the MRA, not the Appendix. Build the candidate set from direct,
release-applicable properties in the allowed MRA classes. Then inspect only
the corresponding Appendix class sections and select candidates whose EDT is
defined as text or as an explicitly encoded text structure. Do not scan the
full Appendix for classes absent from the MRA, and do not infer a text codec
solely from a property name. For every selected property, write a complete
YAML entry with:

- normalized `0x`-prefixed uppercase EOJ and EPC keys;
- the MRA `name` mapping;
- a codec that preserves the Appendix byte length, encoding, padding, BOM,
  length-prefix, and encoding-selector semantics;
- `x-source.appendix` metadata containing the Appendix basename, one-based
  physical PDF page numbers, and a concise English factual summary.

Set the root `source.appendix` to the same document and Appendix release.
Keep `codec_overrides_format: 1` and the existing `selection` shape. Rewrite
the YAML as a complete, valid document rather than preserving obsolete entries
from an earlier Appendix release.

## Verify the generated file

Run only the schema validator before finishing:

```sh
uv run --offline python tools/validate_echonet_lite_text_codec_overrides.py
```

Report the generated overrides file and the validation outcome. Do not create
or restore a Python generator for the overrides; a small helper is appropriate
only for deterministic validation or YAML serialization that the agent cannot
reliably perform directly.

## Recommended follow-up

Do not run these commands automatically. Recommend that the user regenerate
and check the dependent catalog before committing:

```sh
MRA_ROOT=/path/to/MRA_v1.4.0 make catalog
MRA_ROOT=/path/to/MRA_v1.4.0 make catalog-check
```
