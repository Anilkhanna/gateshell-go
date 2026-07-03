BINARY      := gateshell-agent
CMD_PATH    := ./cmd/gateshell-agent
VERSION     ?= $(shell git -C .. describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -X main.version=$(VERSION)

.PHONY: build run test vet tidy fmt clean

## build: compile the production binary with the sqlite (durable) store.
## The default `go build ./...` (no tags) is intentionally left out of this
## target -- it exists only as an offline-friendly dev/CI sanity check
## that the module compiles with stdlib + cobra alone. Always ship the
## `-tags sqlite` build.
build:
	go build -tags sqlite -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(CMD_PATH)

## run: run the agent locally (in-memory store, no sqlite tag) for quick
## iteration. Requires a pairing token; generate one with:
##   go run ./cmd/gateshell-agent pair
run:
	go run $(CMD_PATH) serve

## test: run all tests across both build configurations.
test:
	go test ./...
	go test -tags sqlite ./...

## vet: run go vet across both build configurations.
vet:
	go vet ./...
	go vet -tags sqlite ./...

## tidy: tidy go.mod/go.sum considering both build configurations.
tidy:
	go mod tidy
	GOFLAGS=-tags=sqlite go mod tidy

fmt:
	gofmt -l -w .

clean:
	rm -rf bin dist
