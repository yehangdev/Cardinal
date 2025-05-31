.PHONY: build test

build:
	go build -o bin/cardinal ./cmd/cardinal

test:
	go test ./...
