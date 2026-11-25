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
PLATFORMS := linux/amd64 linux/arm64 windows/amd64

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

# redumper (github.com/superg/redumper, GPL-3.0), embedded for `doszer dump`:
# its official release binaries, checked against these SHA-256 sums. The
# linux/amd64 build also carries the Windows one, run through WSL interop
# (WSL can't reach optical drives itself).
REDUMPER_REF := b773
REDUMPER_URL := https://github.com/superg/redumper/releases/download/$(REDUMPER_REF)
REDUMPER_SHA256_linux-x64   := d9b543f1ef85c53998fb8b6d8820e909cb291c2cc914b4d2ee73604742940fc1
REDUMPER_SHA256_linux-arm64 := 13271218ec182399c29a36d9373febfec0de2beda2a6519a7108aade81c0a19f
REDUMPER_SHA256_windows-x64 := 9c12c1eb6d576176c451a5de1b0b7611057b076374a446af7f3c9f99f9f3256f
REDUMPER_DL  := thirdparty/redumper

REDUMPER_HOST := $(BUILTIN)/linux_$(HOST_ARCH)/redumper $(if $(filter amd64,$(HOST_ARCH)),$(BUILTIN)/linux_amd64/redumper.exe)
REDUMPER_ALL  := $(BUILTIN)/linux_amd64/redumper $(BUILTIN)/linux_amd64/redumper.exe \
                 $(BUILTIN)/linux_arm64/redumper $(BUILTIN)/windows_amd64/redumper.exe

# Redump's IBM PC datfile, snapshotted at build time for `doszer dump`'s
# verification (internal/redump; doszer fetches newer ones at run time).
# Not pinned: it's a snapshot of a database that grows. `make dat` again
# to refresh it.
REDUMP_DAT_URL := http://redump.org/datfile/pc/
REDUMP_DAT     := internal/redump/data/pc.dat.gz

# Every core build writes the same output file in $(DBP_SRC).
.NOTPARALLEL:

.PHONY: build test vet install release clean help core core-all redumper redumper-all dat icon

build: core redumper $(REDUMP_DAT) ## Development build → build/doszer (debug symbols; version = "dev" if no tags)
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

release: clean core-all redumper-all dat ## Stripped, trimpath, multi-arch release archives → dist/ + checksums.txt
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

# The icon, rendered from assets/doszer.svg (doszer-small.svg below 32
# pixels) with ImageMagick: the Windows .ico, linked into doszer.exe as a
# resource (windres, MinGW-w64), and the window icon the built-in
# frontend sets. The outputs are committed, so building needs neither.
ICON_SIZES := 32 48 64 128 256
ICON_TMP   := build/icon
PNGFLAGS   := -define png:exclude-chunks=date,time +set date:create +set date:modify

icon: ## Render the icon (assets/) into the .exe resource and the window icon
	mkdir -p $(ICON_TMP)
	for n in 16 24; do magick -background none assets/doszer-small.svg -resize $${n}x$${n} $(PNGFLAGS) $(ICON_TMP)/$$n.png; done
	for n in $(ICON_SIZES); do magick -background none assets/doszer.svg -resize $${n}x$${n} $(PNGFLAGS) $(ICON_TMP)/$$n.png; done
	go run ./assets/icogen assets/doszer.ico $(foreach n,16 24 $(ICON_SIZES),$(ICON_TMP)/$(n).png)
	cp $(ICON_TMP)/64.png internal/libretro/icon.png
	echo '1 ICON "assets/doszer.ico"' > $(ICON_TMP)/doszer.rc
	$(MINGW64)windres -O coff -i $(ICON_TMP)/doszer.rc -o cmd/doszer/rsrc_windows_amd64.syso

core: $(call CORE_LINUX,$(HOST_ARCH)) ## Build and stage the DOSBox Pure core for this host (linux)

core-all: $(CORES) ## Build and stage the DOSBox Pure core for every embedded platform

dat: ## Download Redump's current PC datfile and stage it for embedding
	mkdir -p thirdparty
	curl -fsSL -A doszer -o thirdparty/redump-pc.zip $(REDUMP_DAT_URL)
	go run ./internal/redump/gen thirdparty/redump-pc.zip $(REDUMP_DAT)

$(REDUMP_DAT):
	$(MAKE) dat

redumper: $(REDUMPER_HOST) ## Fetch and stage redumper for this host (linux)

redumper-all: $(REDUMPER_ALL) ## Fetch and stage redumper for every embedded platform

$(REDUMPER_DL)/%.zip:
	mkdir -p $(@D)
	curl -fsSL -o $@.tmp $(REDUMPER_URL)/redumper-$(REDUMPER_REF)-$*.zip
	echo "$(REDUMPER_SHA256_$*)  $@.tmp" | sha256sum -c --quiet
	mv $@.tmp $@

$(BUILTIN)/linux_amd64/redumper: $(REDUMPER_DL)/linux-x64.zip
$(BUILTIN)/linux_arm64/redumper: $(REDUMPER_DL)/linux-arm64.zip
$(BUILTIN)/linux_amd64/redumper.exe $(BUILTIN)/windows_amd64/redumper.exe: $(REDUMPER_DL)/windows-x64.zip
$(BUILTIN)/%/redumper:
	mkdir -p $(@D)
	unzip -p $< '*/bin/redumper' > $@.tmp
	mv $@.tmp $@

$(BUILTIN)/%/redumper.exe:
	mkdir -p $(@D)
	unzip -p $< '*/bin/redumper.exe' > $@.tmp
	mv $@.tmp $@

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
