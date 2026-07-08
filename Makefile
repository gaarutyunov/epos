# Epos — build, model regeneration, and test targets.
#
# The Go project structure is GENERATED from the SysML v2 model by the real
# sysgo binary (SPEC §15.1); the generated scaffold is committed, and CI only
# builds and tests (plus a regenerate-and-diff drift check).

SYSGO ?= sysgo
MODEL := model/epos.sysml
MODEL_JSON := model/model.json

.PHONY: all build test model generate check-generated lint vulncheck release-snapshot tidy epos

GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
GOVULNCHECK   ?= go run golang.org/x/vuln/cmd/govulncheck@latest
GORELEASER    ?= go run github.com/goreleaser/goreleaser/v2@latest

all: build

## build: compile the epos binary
build: epos

epos:
	go build -o bin/epos ./cmd/epos

## model: transform model/epos.sysml -> model/model.json (SysML v2 API JSON)
# Prefers the OMG SysML v2 Pilot serializer; falls back to the offline converter
# where the pilot distribution cannot be downloaded.
model:
	@if scripts/sysml2json.sh $(MODEL) $(MODEL_JSON) 2>/dev/null; then \
		echo "model.json via OMG Pilot serializer"; \
	else \
		echo "pilot serializer unavailable; using offline converter"; \
		python3 scripts/sysml2json.py $(MODEL) $(MODEL_JSON); \
	fi

## generate: regenerate the Go scaffold from the model with the real sysgo binary
generate: model
	$(SYSGO) generate -c sysgo.yaml --out .
	gofmt -w internal cmd

## check-generated: assert the committed scaffold is in sync with the model (CI drift check)
check-generated: generate
	@if ! git diff --quiet -- internal cmd model/model.json; then \
		echo "ERROR: generated scaffold is out of sync with model/epos.sysml"; \
		git --no-pager diff --stat -- internal cmd model/model.json; \
		exit 1; \
	fi
	@echo "generated scaffold is in sync"

## test: run unit + BDD tests (in-process backends; no docker required)
test:
	go test ./...

## test-integration: run the BDD journeys against real containers (zot/gitea/k3s)
test-integration:
	go test ./... -tags=integration

## lint: run golangci-lint (config in .golangci.yml)
lint:
	$(GOLANGCI_LINT) run ./...

## vulncheck: scan dependencies and code for known vulnerabilities
vulncheck:
	$(GOVULNCHECK) ./...

## release-snapshot: build a local, unpublished release with goreleaser
release-snapshot:
	$(GORELEASER) release --snapshot --clean

## tidy: sync go.mod/go.sum
tidy:
	go mod tidy
