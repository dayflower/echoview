.DEFAULT_GOAL := ci

GO ?= go
UV ?= uv
BUILD_DIR ?= bin
BINARY ?= $(BUILD_DIR)/echoview
CATALOG_SOURCE ?= catalog/echonet_lite_catalog.yaml
CATALOG_DIR ?= $(dir $(BINARY))catalog
PACKAGED_CATALOG ?= $(CATALOG_DIR)/echonet_lite_catalog.yaml

.PHONY: build catalog catalog-check check-mra-root ci ci-go ci-python test test-python \
	validate-overrides validate-catalog lint-go lint-python format-check-go \
	format-check-python vet staticcheck

# ci runs all checks available without external MRA data.
ci: ci-go ci-python validate-overrides validate-catalog

ifneq ($(strip $(MRA_ROOT)),)
ci: catalog-check
endif

ci-go: test lint-go format-check-go

ci-python: test-python lint-python format-check-python

build:
	mkdir -p "$(dir $(BINARY))" "$(CATALOG_DIR)"
	$(GO) build -o "$(BINARY)" ./cmd/echoview
	cp "$(CATALOG_SOURCE)" "$(PACKAGED_CATALOG)"

catalog: check-mra-root
	$(UV) run python tools/generate_echonet_lite_catalog.py --mra-root "$(MRA_ROOT)"

catalog-check: check-mra-root
	$(UV) run python tools/generate_echonet_lite_catalog.py --mra-root "$(MRA_ROOT)" --check

check-mra-root:
	@test -n "$(strip $(MRA_ROOT))" || { echo "MRA_ROOT must be set (for example: MRA_ROOT=/path/to/MRA_v1.4.0 make catalog)" >&2; exit 2; }

test:
	$(GO) test ./...

test-python:
	$(UV) run python -m unittest discover -s tools -p 'test_*.py'

validate-overrides:
	$(UV) run python tools/validate_echonet_lite_text_codec_overrides.py
	$(UV) run python tools/validate_echonet_lite_prometheus_overrides.py
	$(UV) run python tools/validate_echonet_lite_prometheus_metric_type_overrides.py

validate-catalog:
	$(UV) run python tools/validate_echonet_lite_catalog.py

lint-go: vet staticcheck

lint-python:
	$(UV) run ruff check tools/

format-check-go:
	@files="$$(find cmd internal -name '*.go' -exec gofmt -l {} + | sort)"; \
	if [ -n "$$files" ]; then printf '%s\n' "$$files"; exit 1; fi

format-check-python:
	$(UV) run ruff format --check tools/

vet:
	$(GO) vet ./...

staticcheck:
	$(GO) tool staticcheck ./...
