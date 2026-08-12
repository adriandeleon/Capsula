BINARY := capsula
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test lint run install clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/capsula

test:
	go test ./...

# The race detector matters here: probe fans out goroutines per host.
race:
	go test -race ./...

lint:
	gofmt -l .
	go vet ./...

run: build
	./$(BINARY)

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/capsula

clean:
	rm -f $(BINARY)
