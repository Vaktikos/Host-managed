VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath
export CGO_ENABLED := 0

.PHONY: all build web go test test-go test-web test-integration lint fmt release clean

all: build

## build: frontend + both binaries (UI embedded)
build: web go

web:
	cd web && npm ci --no-audit --no-fund && npm run build
	rm -rf internal/webui/dist && mkdir -p internal/webui/dist
	cp -r web/dist/. internal/webui/dist/
	touch internal/webui/dist/.keep

go:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/hostmanaged ./cmd/hostmanaged
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/hostctl ./cmd/hostctl

## test: unit tests (no root, no libvirt needed)
test: test-go test-web

test-go:
	go vet ./...
	@test -z "$$(gofmt -l . | grep -v node_modules)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	CGO_ENABLED=1 go test -race ./...

test-web:
	cd web && npm run typecheck && npm test

## test-integration: real libvirt/QEMU and LXC (root, disposable host)
test-integration:
	HM_LIBVIRT_INTEGRATION=1 HM_LXC_INTEGRATION=1 go test -count=1 -v ./internal/virt/ ./internal/lxc/

fmt:
	gofmt -w $$(git ls-files '*.go')

## release: tarball with binaries, installer and units plus SHA256SUMS
release: build
	rm -rf dist && mkdir -p dist/host-managed-$(VERSION)
	cp -r bin installer deploy docs README.md LICENSE dist/host-managed-$(VERSION)/ 2>/dev/null || true
	tar -C dist -czf dist/host-managed-$(VERSION)-linux-$$(go env GOARCH).tar.gz host-managed-$(VERSION)
	cd dist && sha256sum *.tar.gz > SHA256SUMS

clean:
	rm -rf bin dist web/dist internal/webui/dist/assets internal/webui/dist/index.html internal/webui/dist/favicon.svg
