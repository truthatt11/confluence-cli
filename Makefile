VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)
GOBUILD   := go build -trimpath -ldflags "$(LDFLAGS)"
SOURCES   := $(shell find cmd internal skill -type f) go.mod go.sum
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64
PREFIX    ?= /usr/local
BINDIR    ?= $(PREFIX)/bin

.PHONY: build install uninstall test cover dist clean

# Always rebuilds, so a newly created tag's version is picked up.
build:
	$(GOBUILD) -o bin/cfl ./cmd/cfl

# Rebuilt only when a source changed. That keeps `make build && sudo make install`
# from compiling as root, which would leave a root-owned bin/ behind and usually
# fails anyway because sudo resets PATH and cannot find go.
bin/cfl: $(SOURCES)
	$(GOBUILD) -o $@ ./cmd/cfl

# Installs to $(PREFIX)/bin, /usr/local/bin by default. Without sudo:
#   make install PREFIX=$HOME/.local
install: bin/cfl
	install -d "$(DESTDIR)$(BINDIR)"
	install -m 0755 bin/cfl "$(DESTDIR)$(BINDIR)/cfl"

uninstall:
	rm -f "$(DESTDIR)$(BINDIR)/cfl"

test:
	go vet ./...
	go test -race ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

# Static binaries for every platform in dist/. File names carry no version so
# that https://github.com/<owner>/<repo>/releases/latest/download/<name> keeps
# working; the version is compiled in and shown by `cfl --version`.
dist:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "dist/cfl_$$os"_"$$arch$$ext"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GOBUILD) -o dist/cfl_$$os"_"$$arch$$ext ./cmd/cfl || exit 1; \
	done

clean:
	rm -rf bin dist coverage.out
