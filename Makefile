BINARY := polypod
VERSION := 0.1.0
GOFLAGS := -trimpath -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: build build-linux test clean run ingest

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

ingest:
	go run ./cmd/ingest/ --config config.yaml --source $(SOURCE)

fmt:
	go fmt ./...

vet:
	go vet ./...

check: fmt vet test
