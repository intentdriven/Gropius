APP     := Gropius
BUNDLE  := dist/$(APP).app
BIN     := bin/gropius
PKG     := ./cmd/gropius

.PHONY: all test build app icon install run clean fmt vet lint allow-firewall install-shared

all: test build

## test: unit tests with the race detector
test:
	go test -race ./...

## fmt/vet
fmt:
	gofmt -l -w .

vet:
	go vet ./...

lint: fmt vet test

## build: the plain binary. LDFLAGS is empty for dev builds (keeps debug symbols
## for delve); the app/release build overrides it to strip. VERSION is stamped
## into `gropius -version`; the release workflow passes the tag explicitly.
LDFLAGS ?=
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
build:
	go build -ldflags "$(LDFLAGS) -X main.version=$(VERSION)" -o $(BIN) $(PKG)

## app: a real .app bundle (menu-bar app, LAN + Bonjour entitlements).
## Strips debug info (-s -w): a distributed binary needs no DWARF, and it roughly
## halves the download.
app: LDFLAGS = -s -w
app: build icon
	rm -rf $(BUNDLE)
	mkdir -p $(BUNDLE)/Contents/MacOS $(BUNDLE)/Contents/Resources
	cp build/Info.plist $(BUNDLE)/Contents/Info.plist
	# Stamp the bundle version (VERSION without a leading v) so Finder's Get
	# Info matches what the binary reports, before the bundle is signed.
	/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $(patsubst v%,%,$(VERSION))" $(BUNDLE)/Contents/Info.plist
	cp $(BIN)           $(BUNDLE)/Contents/MacOS/gropius
	cp build/AppIcon.icns $(BUNDLE)/Contents/Resources/AppIcon.icns
	# A stable signing identifier matters: Go's linker ad-hoc-signs every binary
	# with the identifier "a.out", so without this every Gropius build looks like
	# a different app to the firewall and to Local Network Privacy — which means a
	# fresh permission prompt on every rebuild.
	codesign --force --deep \
		--identifier dev.gropius.app \
		--sign - $(BUNDLE)
	@echo "built $(BUNDLE)"

## icon: generate AppIcon.icns from the source PNG
icon:
	@build/mkicon.sh

## install: put the app in /Applications, allow it through the firewall, launch it
## A running copy is quit first: `open` activates an already-running process
## instead of launching the new binary, so an upgrade would keep the old
## version serving while claiming success.
install: app
	@if pgrep -qf "/Applications/$(APP).app/Contents/MacOS/" 2>/dev/null; then \
		echo "Quitting the running $(APP)…"; \
		osascript -e 'quit app "$(APP)"' >/dev/null 2>&1 || true; \
		for i in $$(seq 1 20); do \
			pgrep -qf "/Applications/$(APP).app/Contents/MacOS/" || break; \
			sleep 0.5; \
		done; \
		if pgrep -qf "/Applications/$(APP).app/Contents/MacOS/" 2>/dev/null; then \
			echo "warning: $(APP) is still running; quit it and relaunch to finish the upgrade." >&2; \
		fi; \
	fi
	rm -rf /Applications/$(APP).app
	cp -R $(BUNDLE) /Applications/
	$(MAKE) allow-firewall APP_BIN=/Applications/$(APP).app/Contents/MacOS/gropius
	open /Applications/$(APP).app
	@echo "Gropius is running in the menu bar."

## allow-firewall: let the macOS Application Firewall accept LAN connections to
## Gropius. Without this, a locally-built (non-Developer-ID) binary is blocked:
## the firewall accepts the TCP handshake but drops the data, so other machines
## see an empty response while loopback still works. Loopback never needs this.
## Needs sudo; it modifies a security setting, so it prompts for your password.
APP_BIN ?= $(PWD)/$(BIN)
allow-firewall:
	@echo "Allowing Gropius through the macOS firewall (needs your password)…"
	sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add "$(APP_BIN)"
	sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp "$(APP_BIN)"
	@echo "Done. Other machines on your network can now reach Gropius."

## install-shared: let every macOS account on this Mac share one model cache.
##
## Without this, each user account keeps its own copy of every model — a 70B at
## 4-bit costs 40 GB twice. Gropius uses /Users/Shared/Gropius automatically once
## it exists and is writable.
##
## The directory mode is 3775, and both special bits matter:
##   • setgid (the 2) makes new files inherit the `staff` group, so a model one
##     account downloads is writable by the next.
##   • sticky (the 1) means a file can only be deleted or renamed by its owner —
##     without it, any account in `staff` could replace or remove another user's
##     models (or config/registry files) in this group-writable directory.
##
## Crucially, 3775 is applied to DIRECTORIES ONLY. A recursive `chmod -R 3775`
## also rewrites every file to 0775 (group-writable, world-readable) — which,
## re-run after first use, would expose config.json's api_key/hf_token (the app
## writes it 0600) and make model weights modifiable by any staff account. File
## modes are left to the app, which writes secrets 0600 and logs 0600.
install-shared:
	sudo mkdir -p /Users/Shared/Gropius
	sudo chgrp -R staff /Users/Shared/Gropius
	sudo find /Users/Shared/Gropius -type d -exec chmod 3775 {} +
	@echo "Shared model cache ready at /Users/Shared/Gropius."
	@echo "Restart Gropius; every account on this Mac will now share one set of models."

## run: run headless in the foreground (for development)
run: build
	$(BIN) -headless

## clean removes build outputs only. build/AppIcon.icns is a committed asset
## whose 1024px source PNG is deliberately not in the repo (see .gitignore), so
## deleting it would break `make app` on every machine but the one holding the
## source art.
clean:
	rm -rf bin dist build/AppIcon.iconset
