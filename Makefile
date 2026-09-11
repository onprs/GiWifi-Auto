BINARY := giwifi-auto
BUILD_DIR := bin
VERSION ?= dev
GO ?= go

.PHONY: build test vet race fmt check cross-linux

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-X main.version=$(VERSION)" -o $(BUILD_DIR)/$(BINARY) ./cmd/giwifi-auto

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

race:
	$(GO) test -race ./...

fmt:
	gofmt -w ./cmd ./internal

check: fmt vet test race

cross-linux:
	@test -n "$(GOARCH)" || (echo "请指定 GOARCH，例如 GOARCH=mipsle" >&2; exit 1)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) $(GO) build -trimpath -ldflags "-X main.version=$(VERSION)" -o $(BUILD_DIR)/$(BINARY)-$(GOARCH) ./cmd/giwifi-auto
