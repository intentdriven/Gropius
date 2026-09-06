#!/usr/bin/env bash
#
# One-line installer for Gropius.
#
#   Server (menu-bar, Apple Silicon only):
#     curl -fsSL https://raw.githubusercontent.com/intentdriven/Gropius/main/install.sh | bash
#
#   Client (GropiusChat, universal):
#     curl -fsSL https://raw.githubusercontent.com/intentdriven/Gropius/main/install.sh | bash -s -- client
#
# It downloads the latest release, installs the .app into /Applications, and (for
# the server) allows it through the macOS firewall and launches it. A binary
# fetched by curl is not Gatekeeper-quarantined, so no right-click-to-open dance.
set -euo pipefail

REPO="intentdriven/Gropius"

# Trust model: the download is checked against the SHA256SUMS.txt published on
# the same GitHub Release, and every asset carries a GitHub build-provenance
# attestation binding it to the release workflow run. There is no offline
# signing key. To check provenance yourself before running this script:
#   gh attestation verify Gropius.app.zip --repo intentdriven/Gropius
# Building from source (see the README) is the escape hatch.

mode="${1:-server}"
case "$mode" in
server)
	APP="Gropius"
	ASSET="Gropius.app.zip"
	;;
client)
	APP="GropiusChat"
	ASSET="GropiusChat.app.zip"
	;;
*)
	echo "usage: install.sh [server|client]" >&2
	exit 2
	;;
esac

die() {
	echo "error: $*" >&2
	exit 1
}

[ "$(uname -s)" = "Darwin" ] || die "Gropius is macOS only."

# Both bundles declare macOS 26 as their minimum, so Launch Services refuses
# them on anything older. Refuse here instead — before the download, before the
# sudo prompt, and before /Applications and the firewall are touched — so an
# unsupported Mac is turned away rather than half-installed. The major lives in
# this one variable; build/Info.plist is the value it must match.
MIN_MACOS_MAJOR=26
# `|| macos_version=""` is load-bearing: under `set -e` a bare assignment takes
# the command substitution's status, so a missing sw_vers would abort the script
# with no message at all instead of reaching the refusal below. An unreadable or
# non-numeric version leaves the major empty or unusable, and `[` refuses then
# too.
macos_version="$(sw_vers -productVersion 2>/dev/null)" || macos_version=""
macos_major="${macos_version%%.*}"
[ "${macos_major:-0}" -ge "$MIN_MACOS_MAJOR" ] ||
	die "$APP requires macOS $MIN_MACOS_MAJOR (this Mac runs ${macos_version:-an unreadable version})."

# The server needs Apple Silicon (MLX runs on Metal). The client is universal.
# `uname -m` reports x86_64 in a Rosetta-translated shell (common with x86_64
# Homebrew), so also ask the kernel whether the hardware is Apple Silicon.
if [ "$mode" = "server" ] && [ "$(uname -m)" != "arm64" ] &&
	[ "$(sysctl -n hw.optional.arm64 2>/dev/null)" != "1" ]; then
	die "the Gropius server needs Apple Silicon (this Mac is $(uname -m)). The GropiusChat client is universal: rerun with 'client'."
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
zip="$tmp/$ASSET"

# fetch <asset-name> <dest>: download a release asset, trying the public URL
# first and falling back to gh (transient errors, or a private fork).
fetch() {
	local name="$1" dest="$2"
	# -q first: ignore any curlrc that could re-point the connection while the
	# URL still reads github.com; --proto pins HTTPS end to end, redirects
	# included. The asset and the checksums that verify it come from this same
	# origin, so the transport is the thing to pin.
	if curl -q --proto =https --proto-redir =https -fsSL -o "$dest" "https://github.com/$REPO/releases/latest/download/$name" 2>/dev/null; then
		return 0
	elif command -v gh >/dev/null 2>&1; then
		echo "Direct download of $name failed — retrying via gh…"
		gh release download -R "$REPO" --pattern "$name" --dir "$(dirname "$dest")" --clobber
	else
		die "could not download $name from the latest release. Check your network, or install the GitHub CLI (brew install gh) and retry."
	fi
}

echo "Downloading $APP…"
fetch "$ASSET" "$zip"

# Verify the download is exactly what the release workflow built, BEFORE
# unpacking it, clearing its quarantine, or copying it into /Applications. The
# checksums file comes from the same Release as the asset; --ignore-missing
# skips the other app's line.
echo "Verifying checksum…"
fetch "SHA256SUMS.txt" "$tmp/SHA256SUMS.txt"
( cd "$tmp" && shasum -a 256 -c --ignore-missing SHA256SUMS.txt ) >/dev/null 2>&1 ||
	die "checksum mismatch for $ASSET — the download is corrupt or tampered. Refusing to install."
echo "Checksum OK."

echo "Installing $APP.app to /Applications…"
ditto -x -k "$zip" "$tmp/extract" || die "could not unpack $ASSET."
[ -d "$tmp/extract/$APP.app" ] || die "$ASSET did not contain $APP.app."
# Safe to clear the quarantine now: we have cryptographically verified this .app
# is the exact artifact the release workflow built and signed. (curl downloads
# are usually not quarantined anyway, but a proxy or prior run might have tagged
# it, which would otherwise block launch.)
xattr -dr com.apple.quarantine "$tmp/extract/$APP.app" 2>/dev/null || true
# Quit a running copy first. LaunchServices' `open` activates an already-running
# process instead of launching the new binary, so an upgrade over a live app
# would report success while the old version keeps running.
if pgrep -qf "/Applications/$APP.app/Contents/MacOS/" 2>/dev/null; then
	echo "Quitting the running $APP…"
	osascript -e "quit app \"$APP\"" >/dev/null 2>&1 || true
	for _ in $(seq 1 20); do
		pgrep -qf "/Applications/$APP.app/Contents/MacOS/" || break
		sleep 0.5
	done
	if pgrep -qf "/Applications/$APP.app/Contents/MacOS/" 2>/dev/null; then
		echo "warning: $APP is still running; quit it and relaunch to finish the upgrade." >&2
	fi
fi
rm -rf "/Applications/$APP.app"
cp -R "$tmp/extract/$APP.app" /Applications/

if [ "$mode" = "client" ]; then
	echo "Installed /Applications/$APP.app."
	echo "Open it, then point it at your Gropius server: the address from the server's Connect tab"
	echo "without the trailing /v1 (GropiusChat adds the path itself)."
	open "/Applications/$APP.app"
	exit 0
fi

# Server: allow it through the macOS Application Firewall so other machines on the
# LAN can reach it. Without this the firewall accepts the handshake but drops the
# data — loopback works, the LAN sees an empty response. This needs sudo.
BIN="/Applications/$APP.app/Contents/MacOS/gropius"
echo "Allowing $APP through the macOS firewall (needs your password)…"
if sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add "$BIN" >/dev/null &&
	sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp "$BIN" >/dev/null; then
	echo "Firewall configured."
else
	echo "warning: could not configure the firewall automatically." >&2
	echo "Other machines may see an empty response until you run:" >&2
	echo "  sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add '$BIN'" >&2
	echo "  sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp '$BIN'" >&2
fi

open "/Applications/$APP.app"
cat <<'DONE'

Gropius is running in the menu bar. Click its icon to open the control panel,
download a model, and copy the address other machines should point at.

Optional:
  • Chat client:   curl -fsSL https://raw.githubusercontent.com/intentdriven/Gropius/main/install.sh | bash -s -- client
  • Shared cache:  every account on this Mac can share one copy of each model —
                   see 'make install-shared' in the repo.
DONE
