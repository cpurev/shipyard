GO ?= go
BIN := bin

.PHONY: build test vet lint fmt tidy ci

build:
	mkdir -p $(BIN)
	$(GO) build -o $(BIN)/shipyard ./cmd/shipyard
	$(GO) build -o $(BIN)/shipyard-mcp ./cmd/shipyard-mcp

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

lint:
	golangci-lint run ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

# What CI runs. This target does not deploy.
ci: build vet test lint
