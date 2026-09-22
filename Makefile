BINARY := ocbench
LDFLAGS := -s -w -X mbl/ocbench/internal/version.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev) \
	-X mbl/ocbench/internal/version.Commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown) \
	-X mbl/ocbench/internal/version.Date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: build test vet fmt lint cross vendor clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/ocbench

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w ./cmd ./internal

lint: fmt vet

cross:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 ./cmd/ocbench
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 ./cmd/ocbench

vendor:
	go mod vendor

clean:
	rm -rf bin dist
