GO ?= go
PKG := ./...

.PHONY: build fmt vet test race clean

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

clean:
	$(GO) clean $(PKG)
