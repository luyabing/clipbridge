GO ?= go
VERSION ?= 1.0.0
LDFLAGS := -s -w -X main.version=$(VERSION)
LINUX_BIN := dist/clipbridge_linux_amd64
WINDOWS_BIN := dist/clipbridge_windows_amd64.exe

.PHONY: all check test build linux windows deb clean

all: check build

check:
	./scripts/check.sh

test:
	$(GO) test ./...

build: linux windows

deb: linux
	VERSION=$(VERSION) ./scripts/build-deb.sh

linux:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(LINUX_BIN) .

windows:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS) -H windowsgui" -o $(WINDOWS_BIN) .

clean:
	rm -rf dist
