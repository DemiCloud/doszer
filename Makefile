# Project metadata
NAME    := doszer
VERSION := $(shell git describe --tags --abbrev=0 2>/dev/null || echo "dev")

# Install destination
PREFIX  ?= /usr/local
BINDIR  ?= $(PREFIX)/bin

# Linker flags
LDFLAGS         := -X main.version=$(VERSION)
RELEASE_LDFLAGS := $(LDFLAGS) -s -w

# Target platforms for `make release`
PLATFORMS := linux/amd64 linux/arm64 windows/amd64 darwin/amd64 darwin/arm64

# The DOSBox Pure core embedded for the built-in frontend (internal/builtin),
# built from this release tag. Linux cores are cross-built with zig, which
# brings its own C++ runtime (linked statically) and glibc headers: they
# need only glibc $(CORE_GLIBC)+, on any host architecture. The Windows
# core is built with MinGW-w64 (Fedora: mingw64-gcc-c++), beside
# sdl2-compat's SDL2.dll and the SDL3.dll it loads (mingw64-sdl2-compat,
# mingw64-SDL3).
DBP_URL     := https://codeberg.org/schelling/dosbox-pure.git
DBP_REF     := 1.0-preview8
DBP_SRC     := thirdparty/dosbox-pure
BUILTIN     := internal/builtin/data
ZIG         ?= zig
CORE_GLIBC  := 2.28
MINGW64     := x86_64-w64-mingw32-
MINGW64_BIN := /usr/x86_64-w64-mingw32/sys-root/mingw/bin
JOBS        ?= $(shell nproc 2>/dev/null || echo 4)

ZIG_ARCH_amd64 := x86_64
ZIG_ARCH_arm64 := aarch64
HOST_ARCH      := $(shell go env GOARCH)

CORE_LINUX   = $(BUILTIN)/linux_$(1)/dosbox_pure_libretro.so
CORE_WINDOWS := $(BUILTIN)/windows_amd64/dosbox_pure_libretro.dll
CORES        := $(call CORE_LINUX,amd64) $(call CORE_LINUX,arm64) $(CORE_WINDOWS)

# Every core build writes the same output file in $(DBP_SRC).
.NOTPARALLEL:

.PHONY: build test vet install release clean help core core-all

build: core ## Development build → build/doszer (debug symbols; version = "dev" if no tags)
	mkdir -p build
	go mod tidy
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o build/$(NAME) ./cmd/doszer

test: ## Run the test suite
	go test -count=1 ./...

vet: ## Run go vet static analysis
	go vet ./...

install: build ## Copy the built binary to $(DESTDIR)$(BINDIR) (default: /usr/local/bin)
	install -D -m 0755 build/$(NAME) $(DESTDIR)$(BINDIR)/$(NAME)
	@echo "Binary installed to $(DESTDIR)$(BINDIR)/$(NAME)."

release: clean core-all ## Stripped, trimpath, multi-arch release archives → dist/ + checksums.txt
	mkdir -p dist
	go mod tidy
	@set -e; for platform in $(PLATFORMS); do \
		OS=$$(echo $$platform | cut -d/ -f1); \
		ARCH=$$(echo $$platform | cut -d/ -f2); \
		EXT=$$([ "$$OS" = "windows" ] && echo ".exe" || echo ""); \
		OUT=dist/$(NAME)_$${OS}_$${ARCH}$${EXT}; \
		echo "building $$OS/$$ARCH"; \
		GOOS=$$OS GOARCH=$$ARCH CGO_ENABLED=0 go build -trimpath \
			-ldflags "$(RELEASE_LDFLAGS)" -o $$OUT ./cmd/doszer; \
	done
	@echo "==> Checksums"
	cd dist && sha256sum * > checksums.txt && cat checksums.txt

clean: ## Remove build/ and dist/
	rm -rf build dist

core: $(call CORE_LINUX,$(HOST_ARCH)) ## Build and stage the DOSBox Pure core for this host (linux)

core-all: $(CORES) ## Build and stage the DOSBox Pure core for every embedded platform

$(DBP_SRC)/.fetched:
	rm -rf $(DBP_SRC)
	git init -q $(DBP_SRC)
	git -C $(DBP_SRC) remote add origin $(DBP_URL)
	git -C $(DBP_SRC) fetch -q --depth 1 origin refs/tags/$(DBP_REF)
	git -C $(DBP_SRC) -c advice.detachedHead=false checkout -q FETCH_HEAD
	touch $@

$(BUILTIN)/linux_%/dosbox_pure_libretro.so: $(DBP_SRC)/.fetched
	$(MAKE) -C $(DBP_SRC) -j$(JOBS) BUILDDIR=linux-$* CPUFLAGS= STRIP=true \
		CXX="$(ZIG) c++ -target $(ZIG_ARCH_$*)-linux-gnu.$(CORE_GLIBC) -s"
	install -D -m 0644 $(DBP_SRC)/dosbox_pure_libretro.so $@

$(CORE_WINDOWS): $(DBP_SRC)/.fetched
	$(MAKE) -C $(DBP_SRC) -j$(JOBS) BUILDDIR=windows platform=windows \
		CXX="$(MINGW64)g++ -static -static-libgcc -static-libstdc++" STRIP=$(MINGW64)strip
	install -D -m 0644 $(DBP_SRC)/dosbox_pure_libretro.dll $@
	install -m 0644 $(MINGW64_BIN)/SDL2.dll $(MINGW64_BIN)/SDL3.dll $(@D)/

help: ## Show this help message
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make \033[36m<target>\033[0m\n\nTargets:\n"} \
	     /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
