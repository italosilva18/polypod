BINARY := polypod
VERSION := 0.4.0
GOFLAGS := -trimpath -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: build build-linux build-arm test clean run run-setup run-headless ingest fmt vet check

build:
	go build $(GOFLAGS) -o $(BINARY) .

build-linux:
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -o $(BINARY) .

build-arm:
	GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -o $(BINARY) .

test:
	go test ./...

clean:
	rm -f $(BINARY)

run: build
	./$(BINARY) config.yaml

run-setup: build
	./$(BINARY) --setup

run-headless: build
	./$(BINARY) -p "$(PROMPT)"

ingest:
	go run ./cmd/ingest/ --config config.yaml --source $(SOURCE)

fmt:
	go fmt ./...

vet:
	go vet ./...

check: fmt vet test

docker:
	docker build -t polypod:$(VERSION) .

mcp-serve: build
	./$(BINARY) mcp serve
