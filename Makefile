# oh-my-graph — build & quality targets.
#
# CI runs: build, vet, fmt, test. The `smoke` target spawns a REAL claude and is
# a manual step only — never wire it into CI (it costs money and needs a login).

BINARY := oh-my-graph
PKG    := ./cmd/oh-my-graph
SMOKE_DIR ?= /tmp/omg-smoke

.PHONY: build test vet fmt fmt-check smoke clean

build: ## Build the oh-my-graph binary.
	go build -o bin/$(BINARY) $(PKG)

test: ## Run the full test suite under the race detector (no real claude).
	go test ./... -race -count=1

vet: ## Run go vet.
	go vet ./...

fmt: ## Format all Go source in place.
	gofmt -w .

fmt-check: ## Fail if any Go source is not gofmt-clean (CI gate).
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed on:"; echo "$$unformatted"; exit 1; \
	fi

# MANUAL ONLY — spawns a real claude on your subscription (a few cents).
# Never add this to CI; all engine logic is covered by the FakeRunner tests.
smoke: build ## Manually run the haiku smoke graph against real claude.
	mkdir -p $(SMOKE_DIR)
	./bin/$(BINARY) run graphs/haiku-smoke.yaml --input dir=$(SMOKE_DIR)

clean: ## Remove build artifacts.
	rm -rf bin

local: fmt-check vet build test ## Local end-to-end checks before a PR (build + test + vet).
	@echo "make local: all local checks passed"
