BINARY := polypod
VERSION := 0.6.0
GOFLAGS := -trimpath -ldflags="-s -w -X github.com/costa/polypod/internal/autoupdate.currentVersion=$(VERSION)"
OUT_DIR ?= dist

.PHONY: build build-linux build-arm build-all test clean run run-setup run-headless ingest fmt vet check

build:
	CGO_ENABLED=0 go build $(GOFLAGS) -o $(BINARY) .

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -o $(BINARY) .

build-arm:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -o $(BINARY) .

# Windows currently needs platform-specific replacements for Unix process groups and file locking.
build-all:
	mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -o $(OUT_DIR)/$(BINARY)-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -o $(OUT_DIR)/$(BINARY)-linux-arm64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) -o $(OUT_DIR)/$(BINARY)-darwin-amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -o $(OUT_DIR)/$(BINARY)-darwin-arm64 .

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
	docker build --build-arg VERSION=$(VERSION) -t polypod:$(VERSION) .

mcp-serve: build
	./$(BINARY) mcp serve
