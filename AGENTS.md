# Repository Guide

## Purpose

This repository provides an ECHONET Lite command-line tool written in Go and
the source data and Python tooling used to generate its catalog. Treat the
protocol contracts in `notes/` and schemas in `spec/` as the source of truth
for externally visible behavior.

## Layout

- `cmd/echoview/`: CLI entry point and command integration.
- `internal/`: protocol framing, discovery, collection, configuration, and
  output packages. Keep production code outside `cmd/` when it is reusable.
- `tools/`: Python MRA parsing, catalog generation, and data validators.
- `catalog/echonet_lite_catalog.yaml`: generated source catalog; the build copies it
  to `bin/catalog/echonet_lite_catalog.yaml` for executable-relative loading.
- `echonet_lite_*_overrides.yaml`: reviewed source overrides for generation.
- `examples/profiles/`: example vendor profile catalogs.
- `instances.yaml`: a conventional name for user-created local deployment
  configuration, not a maintained repository file. Never add real network
  addresses or credentials unless explicitly requested.
- `notes/` and `spec/`: behavior contracts and data schemas.

## Development Rules

- Write production code, code comments, and repository documentation in
  English.
- Preserve the CLI contracts: requested output belongs on stdout, while debug
  logs and non-fatal warnings belong on stderr. Never mix logs into JSON or
  other machine-readable output.
- Keep ECHONET Lite I/O behind the existing internal packages. Tests must use
  fixtures or fakes rather than requiring a live device or multicast network.
- Preserve stable ordering in text, JSON, and generated YAML when results
  originate from maps or network replies.
- Catalog documents may contain both classes and profiles. Runtime loading is
  directory-based: do not reintroduce the removed `--catalog` or `--profiles`
  flags, catalog overlays, class-level collection policy, or profile
  auto-selection.
- Catalog directories are loaded once at process startup. Do not add file
  watching, signal-triggered reload, or management-endpoint reload behavior.
- Use uppercase hexadecimal notation with a `0x` prefix for EOJs and EPCs,
  consistent with the contracts and schemas.
- Update the relevant contract or schema whenever a user-visible CLI behavior
  or supported YAML shape changes.

## Generated Catalog Data

- Do not edit `catalog/echonet_lite_catalog.yaml` by hand. To update the
  generated catalog, change its inputs and run:

  ```sh
  uv run python tools/generate_echonet_lite_catalog.py --mra-root /path/to/MRA_v1.4.0
  ```

- Validate the generated catalog's structure with `make validate-catalog`.
- Verify a committed catalog is current with the same generation command plus
  `--check` when the matching MRA release is available.
- Validate override documents before generation:

  ```sh
  make validate-overrides
  ```

- Appendix-derived text codec overrides are governed by the
  `echonet-lite-text-codec-overrides` skill. Use that skill to create and
  validate `echonet_lite_text_codec_overrides.yaml`. The skill stops after
  validating the overrides; regenerating and checking the dependent catalog
  is a separate step when the task also includes updating the catalog.

## Verification

Run the narrowest relevant checks while iterating, then run the full suites
before submitting a cross-cutting change:

```sh
gofmt -w <changed-go-files>
uv run ruff format tools/
make ci
MRA_ROOT=/path/to/MRA_v1.4.0 make ci
```

Use the individual `test`, `test-python`, `validate-overrides`,
`validate-catalog`, `lint-go`, `lint-python`, `format-check-go`, and
`format-check-python` Make targets while iterating. With `MRA_ROOT` set,
`make ci` also checks that the generated catalog matches that MRA release.

The Python suite is limited to `test_generate_echonet_lite_catalog.py`,
`test_validate_echonet_lite_text_codec_overrides.py`, and
`test_validate_echonet_lite_prometheus_overrides.py`, plus structural catalog
validator tests. It must not add runtime CLI, catalog decoding, or
profile-application coverage; those belong in Go tests.

For Go changes, add or update table-driven tests next to the package under
test. For parser, protocol, and collector behavior, cover malformed input,
partial responses, timeouts, and deterministic output where applicable.

## Continuous Integration

- Run `make ci` for Go and Python checks and data validation. `ci-go` runs Go
  tests, vet, Staticcheck, and a gofmt check. `ci-python` runs Python unit
  tests, Ruff lint, and Ruff formatting checks. `validate-overrides` checks the
  three override documents; `validate-catalog` checks the generated catalog's
  structural schema. `catalog-check` runs only when `MRA_ROOT` is set.
- GitHub Actions runs `make ci-go` in its Go job and `make ci-python`,
  `make validate-overrides`, and `make validate-catalog` in its Python job.
- Staticcheck is version-pinned as a Go tool dependency in `go.mod` and is
  invoked with `go tool staticcheck`; do not require a globally installed
  Staticcheck binary.
- A GitHub Actions workflow should set up the Go version declared in `go.mod`
  and then run `make ci-go`.
- Use `make build` to create the local `bin/echoview` executable. Keep build
  artifacts out of version control.

## Change Hygiene

- Inspect `git status --short` before editing. This working tree may contain
  user-owned local configuration and generated-data experiments; do not delete
  or overwrite unrelated changes.
- Keep patches focused. Do not reformat unrelated files.
- Do not commit generated output unless its corresponding source input or
  generator behavior changed and the regenerated diff is intentional.
- Use Conventional Commits for commit titles. After the one-line title and a
  blank line, add `Co-authored-by: codex <codex@openai.com>`.
