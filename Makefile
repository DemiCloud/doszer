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

# The DOSBox Pure core embedded for the built-in frontend (internal/builtin):
# built from this release, for linux/amd64 natively and windows/amd64 with
# MinGW-w64 (Fedora: mingw64-gcc-c++; SDL2.dll/SDL3.dll from
# mingw64-sdl2-compat and mingw64-SDL3). The C++ runtime is linked
# statically where its static library is installed (always for Windows;
# on Linux with Fedora's libstdc++-static), so the core needs only the C
# library; otherwise the Linux core also needs libstdc++.so.6.
DBP_URL     := https://codeberg.org/schelling/dosbox-pure.git
DBP_REF     := 1.0-preview8
DBP_SRC     := thirdparty/dosbox-pure
BUILTIN     := internal/builtin/data
MINGW64     := x86_64-w64-mingw32-
MINGW64_BIN := /usr/x86_64-w64-mingw32/sys-root/mingw/bin
JOBS        ?= $(shell nproc 2>/dev/null || echo 4)

CORE_LINUX_CXX := g++$(if $(filter /%,$(shell g++ -print-file-name=libstdc++.a 2>/dev/null)), -static-libstdc++ -static-libgcc)
CORE_LINUX   := $(BUILTIN)/linux_amd64/dosbox_pure_libretro.so
CORE_WINDOWS := $(BUILTIN)/windows_amd64/dosbox_pure_libretro.dll

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

core: $(CORE_LINUX) ## Build and stage the DOSBox Pure core for the host (linux/amd64)

core-all: $(CORE_LINUX) $(CORE_WINDOWS) ## Build and stage the DOSBox Pure core for every embedded platform

$(DBP_SRC)/.fetched:
	rm -rf $(DBP_SRC)
	git init -q $(DBP_SRC)
	git -C $(DBP_SRC) remote add origin $(DBP_URL)
	git -C $(DBP_SRC) fetch -q --depth 1 origin refs/tags/$(DBP_REF)
	git -C $(DBP_SRC) -c advice.detachedHead=false checkout -q FETCH_HEAD
	touch $@

$(CORE_LINUX): $(DBP_SRC)/.fetched
	$(MAKE) -C $(DBP_SRC) -j$(JOBS) BUILDDIR=linux \
		CXX="$(CORE_LINUX_CXX)"
	install -D -m 0644 $(DBP_SRC)/dosbox_pure_libretro.so $@

$(CORE_WINDOWS): $(DBP_SRC)/.fetched
	$(MAKE) -C $(DBP_SRC) -j$(JOBS) BUILDDIR=windows platform=windows \
		CXX="$(MINGW64)g++ -static -static-libgcc -static-libstdc++" STRIP=$(MINGW64)strip
	install -D -m 0644 $(DBP_SRC)/dosbox_pure_libretro.dll $@
	install -m 0644 $(MINGW64_BIN)/SDL2.dll $(MINGW64_BIN)/SDL3.dll $(@D)/

help: ## Show this help message
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make \033[36m<target>\033[0m\n\nTargets:\n"} \
	     /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
