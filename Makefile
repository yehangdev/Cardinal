.PHONY: build test vet check

build:
	go build -o bin/cardinal ./cmd/cardinal

test:
	go test ./...

vet:
	go vet ./...

check: test vet
