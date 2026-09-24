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
To create and push the next stable tag, run `bash tools/bump-version.sh minor`
(or use `major` or `patch`). The script calculates the next `vX.Y.Z` tag from
the tags on `origin`. It requires a clean working tree and a checked-out branch
whose HEAD matches the branch on `origin`. It creates a local tag and pushes
only that tag; if the push fails, the local tag remains for inspection.
Pushing a `vX.Y.Z` tag runs the release workflow: it runs `make ci`, builds the
artifacts with GoReleaser, and uploads them with checksums to GitHub Releases.
GoReleaser also publishes Linux amd64 and arm64 container images to GHCR and
Docker Hub. The release job needs `packages: write` for GHCR and a
`DOCKERHUB_TOKEN` repository secret for Docker Hub. Docker Hub login defaults
to `dayflower`; set the `DOCKERHUB_USERNAME` repository variable if
the token belongs to a different account with push access to the image.
Create the `dayflower/echoview` Docker Hub repository before the first push.
GHCR packages are private on first publication; set the package visibility
separately if anonymous pulls are desired.
GoReleaser sets the released binary's `--version` output from the tag; an
ordinary `make build` retains the source default.
The CI container job builds snapshot images for both Linux architectures
without publishing them, so image packaging is checked before a tag release.

After GoReleaser publishes a tagged release, the workflow uses
[`brew-up`](https://github.com/dayflower/brew-up) to open a pull request that
updates `Formula/echoview.rb` in `dayflower/homebrew-tap` and enable auto-merge.
The source repository must have a `HOMEBREW_GITHUB_API_TOKEN` secret with
Contents and Pull requests read/write access to that tap repository. GitHub
merges the pull request when the tap repository's merge requirements are met.

To update the tap from an existing release, manually run the Release workflow
with its `release_tag` input. Manual runs default to `dry_run: true`; set it to
`false` to open the tap pull request with auto-merge enabled. A manual run skips
GoReleaser.
