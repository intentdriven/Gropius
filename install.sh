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
# While the repo is private, prefix the curl with an auth header and keep gh
# logged in, e.g.:
#   curl -fsSL -H "Authorization: Bearer $(gh auth token)" \
#     https://raw.githubusercontent.com/REPPL/Gropius/main/install.sh | bash
#
# It downloads the latest release, installs the .app into /Applications, and (for
# the server) allows it through the macOS firewall and launches it. A binary
# fetched by curl is not Gatekeeper-quarantined, so no right-click-to-open dance.
set -euo pipefail

REPO="REPPL/Gropius"

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

echo "Downloading $APP…"
url="https://github.com/$REPO/releases/latest/download/$ASSET"
if curl -fsSL -o "$zip" "$url" 2>/dev/null; then
	: # public download succeeded
elif command -v gh >/dev/null 2>&1; then
	# Private repo: let gh handle auth. Downloads into $tmp as $ASSET.
	echo "Direct download failed (repo is private) — using gh…"
	gh release download -R "$REPO" --pattern "$ASSET" --dir "$tmp" --clobber ||
		die "gh could not download $ASSET. Run 'gh auth login' and try again."
else
	die "could not download $ASSET. The repo may be private — install the GitHub CLI (brew install gh && gh auth login), or make the repo public."
fi

echo "Installing $APP.app to /Applications…"
ditto -x -k "$zip" "$tmp/extract" || die "could not unpack $ASSET."
[ -d "$tmp/extract/$APP.app" ] || die "$ASSET did not contain $APP.app."
# Belt and braces: curl downloads are not quarantined, but a proxy or prior run
# might have tagged it. Clearing it keeps Gatekeeper from blocking launch.
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
