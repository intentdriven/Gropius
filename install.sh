#!/usr/bin/env bash
#
# One-line installer for Gropius.
#
#   Server (menu-bar, Apple Silicon only):
#     curl -fsSL https://raw.githubusercontent.com/REPPL/Gropius/main/install.sh | bash
#
#   Client (GropiusChat, universal):
#     curl -fsSL https://raw.githubusercontent.com/REPPL/Gropius/main/install.sh | bash -s -- client
#
# It downloads the latest release, installs the .app into /Applications, and (for
# the server) allows it through the macOS firewall and launches it. A binary
# fetched by curl is not Gatekeeper-quarantined, so no right-click-to-open dance.
set -euo pipefail

REPO="REPPL/Gropius"

# Public half of the minisign key the release workflow signs SHA256SUMS.txt with.
# Safe to publish — it only verifies. The private half lives solely in the repo's
# MINISIGN_SECRET_KEY CI secret. If this is still the placeholder, signing has not
# been set up yet and the installer refuses rather than pretend to verify.
#
# One-time setup (run once, locally):
#   minisign -G -W -p minisign.pub -s minisign.key   # -W = passwordless key for CI
#   gh secret set MINISIGN_SECRET_KEY < minisign.key  # add the private key to the repo
#   # then paste the SECOND line of minisign.pub (the RW... string) below, commit,
#   # and cut a release so the signed SHA256SUMS.txt is published.
MINISIGN_PUBKEY="RWRu9Q1BSiXUYoRkMdeRsed/fwzd+puRnJ0MvYMck3Ef3VWsuIRkXO92"

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

# The server needs Apple Silicon (MLX runs on Metal). The client is universal.
if [ "$mode" = "server" ] && [ "$(uname -m)" != "arm64" ]; then
	die "the Gropius server needs Apple Silicon (this Mac is $(uname -m)). The GropiusChat client is universal: rerun with 'client'."
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
zip="$tmp/$ASSET"

# fetch <asset-name> <dest>: download a release asset, trying the public URL
# first and falling back to gh (transient errors, or a private fork).
fetch() {
	local name="$1" dest="$2"
	if curl -fsSL -o "$dest" "https://github.com/$REPO/releases/latest/download/$name" 2>/dev/null; then
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

# Verify the download is exactly what the release workflow built and signed,
# BEFORE unpacking it, clearing its quarantine, or copying it into /Applications.
# The chain: minisign proves SHA256SUMS.txt was signed by the repo's private key
# (which never leaves CI); the checksum then proves this .zip matches that file.
if [ "${MINISIGN_PUBKEY#RWQPLACEHOLDER}" = "$MINISIGN_PUBKEY" ]; then
	command -v minisign >/dev/null 2>&1 ||
		die "minisign is required to verify the download. Install it with: brew install minisign"
	echo "Verifying signature…"
	fetch "SHA256SUMS.txt" "$tmp/SHA256SUMS.txt"
	fetch "SHA256SUMS.txt.minisig" "$tmp/SHA256SUMS.txt.minisig"
	minisign -Vm "$tmp/SHA256SUMS.txt" -x "$tmp/SHA256SUMS.txt.minisig" -P "$MINISIGN_PUBKEY" >/dev/null 2>&1 ||
		die "signature verification FAILED — the download does not match the repo's signing key. Refusing to install."
	# The signature covers SHA256SUMS.txt; now confirm THIS asset's hash is the
	# one it vouches for. --ignore-missing skips the other app's line.
	( cd "$tmp" && shasum -a 256 -c --ignore-missing SHA256SUMS.txt ) >/dev/null 2>&1 ||
		die "checksum mismatch for $ASSET — the download is corrupt or tampered. Refusing to install."
	echo "Signature and checksum OK."
else
	die "this installer has no signing key configured yet (MINISIGN_PUBKEY is a placeholder). Set up minisign signing — see the comments at the top of install.sh — before distributing it."
fi

echo "Installing $APP.app to /Applications…"
ditto -x -k "$zip" "$tmp/extract" || die "could not unpack $ASSET."
[ -d "$tmp/extract/$APP.app" ] || die "$ASSET did not contain $APP.app."
# Safe to clear the quarantine now: we have cryptographically verified this .app
# is the exact artefact the release workflow built and signed. (curl downloads
# are usually not quarantined anyway, but a proxy or prior run might have tagged
# it, which would otherwise block launch.)
xattr -dr com.apple.quarantine "$tmp/extract/$APP.app" 2>/dev/null || true
rm -rf "/Applications/$APP.app"
cp -R "$tmp/extract/$APP.app" /Applications/

if [ "$mode" = "client" ]; then
	echo "Installed /Applications/$APP.app."
	echo "Open it, then point it at your Gropius server's address (the server's Connect tab shows it)."
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
  • Chat client:   curl -fsSL https://raw.githubusercontent.com/REPPL/Gropius/main/install.sh | bash -s -- client
  • Shared cache:  every account on this Mac can share one copy of each model —
                   see 'make install-shared' in the repo.
DONE
