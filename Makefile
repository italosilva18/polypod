BINARY := polypod
VERSION := 0.1.0
GOFLAGS := -trimpath -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: build build-linux test clean run run-setup ingest

build:
	go build $(GOFLAGS) -o $(BINARY) .

build-linux:
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -o $(BINARY) .

test:
	go test ./...

clean:
	rm -f $(BINARY)

run: build
	./$(BINARY) config.yaml

run-setup: build
	./$(BINARY) --setup

ingest:
	go run ./cmd/ingest/ --config config.yaml --source $(SOURCE)

fmt:
	go fmt ./...

vet:
	go vet ./...

check: fmt vet test
