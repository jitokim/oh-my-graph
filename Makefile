# oh-my-graph — build & quality targets.
#
# CI runs: build, vet, fmt, test. Smoke targets spawn a REAL model CLI and are
# manual only — never wire them into CI (they spend plan allowance and need a login).

BINARY := oh-my-graph
PKG    := ./cmd/oh-my-graph
# SMOKE_DIR is the PARENT directory. Each smoke run gets its own fresh, empty
# directory inside it (mktemp -d), because a shared directory let a later smoke
# pass on a haiku.txt an earlier smoke wrote. Nothing in it is ever deleted.
SMOKE_DIR ?= /tmp/omg-smoke

.PHONY: build test vet fmt fmt-check smoke smoke-codex clean

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
	@mkdir -p "$(SMOKE_DIR)"
	@dir=$$(mktemp -d "$(SMOKE_DIR)/claude.XXXXXX") || exit 1; \
	echo "smoke: fresh directory $$dir"; \
	./bin/$(BINARY) run graphs/haiku-smoke.yaml --input "dir=$$dir"; \
	status=$$?; \
	echo "smoke: haiku.txt is in $$dir"; \
	exit $$status

# MANUAL ONLY — spawns real Codex using the saved login.
smoke-codex: build ## Manually run the haiku smoke graph against real Codex.
	@mkdir -p "$(SMOKE_DIR)"
	@dir=$$(mktemp -d "$(SMOKE_DIR)/codex.XXXXXX") || exit 1; \
	echo "smoke-codex: fresh directory $$dir"; \
	./bin/$(BINARY) --runtime codex run graphs/haiku-smoke.yaml --input "dir=$$dir"; \
	status=$$?; \
	echo "smoke-codex: haiku.txt is in $$dir"; \
	exit $$status

clean: ## Remove build artifacts.
	rm -rf bin

local: fmt-check vet build test ## Local end-to-end checks before a PR (build + test + vet).
	@echo "make local: all local checks passed"
