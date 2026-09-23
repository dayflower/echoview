# Development

## Checks

Run all local CI checks:

```sh
make ci
```

Run the language-specific checks individually:

```sh
make ci-go
make ci-python
```

The Python target runs unit tests, Ruff lint checks, and Ruff formatting checks.
`make ci` also runs `make validate-overrides` and `make validate-catalog`.
Use `make lint-go`, `make lint-python`, `make format-check-go`, or
`make format-check-python` to run individual checks.

## Catalog generation

The catalog is generated from ECHONET Lite MRA data. When changing catalog
inputs or generator behavior, validate the overrides and regenerate it by
setting `MRA_ROOT` to the directory that contains the MRA release:

```sh
make validate-overrides
MRA_ROOT=/path/to/MRA_v1.4.0 make catalog
MRA_ROOT=/path/to/MRA_v1.4.0 make catalog-check
```

When `MRA_ROOT` is set, `make ci` also checks that the generated catalog
matches that MRA release. Without it, `make ci` still validates the catalog's
structural schema.

Do not edit `catalog/echonet_lite_catalog.yaml` directly. See
[AGENTS.md](../AGENTS.md) for repository conventions and the full verification
matrix.

## Publishing releases

Before tagging a release, run `make ci` and
`goreleaser release --snapshot --clean` to inspect the artifacts under `dist/`.
Pushing a `vX.Y.Z` tag runs the release workflow: it runs `make ci`, builds the
artifacts with GoReleaser, and uploads them with checksums to GitHub Releases.
GoReleaser sets the released binary's `--version` output from the tag; an
ordinary `make build` retains the source default.

After GoReleaser publishes a tagged release, the workflow uses
[`brew-up`](https://github.com/dayflower/brew-up) to open a pull request that
updates `Formula/echoview.rb` in `dayflower/homebrew-tap`. The source repository
must have a `HOMEBREW_GITHUB_API_TOKEN` secret with Contents and Pull requests
read/write access to that tap repository.

To update the tap from an existing release, manually run the Release workflow
with its `release_tag` input. Manual runs default to `dry_run: true`; set it to
`false` to open the tap pull request. A manual run skips GoReleaser.
