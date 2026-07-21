GO ?= go
PKG := ./...

.PHONY: build fmt vet test race compatibility-test clean

build:
	$(GO) build $(PKG)

fmt:
	gofmt -w .

vet:
	$(GO) vet $(PKG)

test:
	$(GO) test $(PKG)

race:
	$(GO) test -race $(PKG)

# Runs the Python-vs-Go differential/compatibility test suite.
# Requires the Python reference venv at ../venv (see docs/python-baseline.md).
compatibility-test:
	PWNAGOTCHI_PY=$(CURDIR)/../venv/bin/pwnagotchi \
	PWNAGOTCHI_PYTHON=$(CURDIR)/../venv/bin/python3 \
	PWNAGOTCHI_REPO_ROOT=$(CURDIR)/.. \
	$(GO) test -tags=compatibility -run TestCompat ./tests/...

clean:
	$(GO) clean $(PKG)
