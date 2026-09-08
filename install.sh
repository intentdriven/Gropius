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
# It downloads the latest release, installs the .app into /Applications (or into
# ~/Applications when this account cannot write /Applications), and (for
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
#
# That sentence has exactly one exception, and it is refused outside CI. When
# GITHUB_ACTIONS=true, GROPIUS_ASSET_DIR points this run at a local directory,
# and then BOTH the bundle and the SHA256SUMS.txt it is checked against are read
# from that directory: the verification proves the directory is self-consistent
# and NOTHING about where its contents came from. Anywhere else the seam is a
# hard refusal (see ASSET_DIR below), because a caller who can set one
# environment variable would otherwise substitute the whole integrity control
# silently — this is the script the README tells people to pipe into bash.

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
# authorization panel, and before /Applications and the firewall are touched — so an
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

# Elevation, for the firewall grant the server needs (applied at the end of this
# script). Both helpers below raise the native macOS authentication panel rather
# than prompting on the terminal, and the difference is not cosmetic: sudo can
# only ever accept the invoking user's own password, and a standard account is
# not in the sudoers set at all, so the old terminal prompt was unsatisfiable on
# exactly the accounts that most need this. The authentication panel asks for an
# administrator's name AND password, so a standard user can have an
# administrator enter theirs.
#
# Neither helper builds its root command by string interpolation. Every -e
# argument is single-quoted, so bash expands nothing into the AppleScript; the
# binary path travels as an osascript argument and is escaped for the root shell
# by `quoted form of`. Interpolating a path into a command that runs as root
# would be a local privilege-escalation surface in the one script users are told
# to pipe into bash.
#
# Nothing here reads standard input, which under `curl | bash` is the remaining
# text of this script.
#
# osascript is invoked by absolute path, and that is load-bearing rather than
# tidiness. `curl | bash` runs with the invoking user's PATH, which routinely
# puts user-writable directories ahead of /usr/bin — a plain `~/.local/bin` needs
# no privileges to write at all. Unprivileged code already running as the user
# could otherwise drop an `osascript` shim there, and this script would hand it
# the elevation: the shim draws its own authentication panel and harvests the
# administrator password.
#
# That risk is created by this block, not inherited. A counterfeit terminal
# password prompt is something a wary user might distrust; asking through the
# system authentication panel teaches them that a panel is the expected,
# legitimate part of installing, which makes a fake one more convincing. Pinning
# the interpreter is the cost of that trade. `quoted form of` escapes the
# argument; it cannot help when the interpreter itself is attacker-supplied.

# admin_authorize: raise the authentication panel and do nothing with the result.
# Used as a gate: it proves an administrator is present before any work starts.
admin_authorize() {
	/usr/bin/osascript -e 'do shell script "/usr/bin/true" with administrator privileges' >/dev/null 2>&1
}

# firewall_grant BINARY: allow BINARY through the macOS Application Firewall.
# Both socketfilterfw calls share one `do shell script`, so this is one panel and
# not two.
firewall_grant() {
	/usr/bin/osascript \
		-e 'on run argv' \
		-e 'set fw to "/usr/libexec/ApplicationFirewall/socketfilterfw"' \
		-e 'set p to quoted form of (item 1 of argv)' \
		-e 'do shell script fw & " --add " & p & " && " & fw & " --unblockapp " & p with administrator privileges' \
		-e 'end run' \
		-- "$1" >/dev/null 2>&1
}

# Ask for that authorization HERE — before the download, before /Applications is
# touched, before anything is written. Failing at this point costs the user
# nothing; failing at the end (where the prompt used to live) left the app
# installed and quietly unable to serve the LAN, which is the bug this fixes.
#
# The panel is raised whenever the server is being installed, without first
# reading the firewall's state to decide. A grant can be recorded while the
# firewall is switched off and survives the user switching it on later, so
# inspecting the state would only buy a skipped prompt today at the cost of a
# silently missing grant tomorrow.
#
# CI never has a console to answer the panel, and the release gate installs the
# server to check the script still works. Skip the gate there: the grant at the
# end already tolerates failure, and a runner has no firewall to grant through.
if [ "$mode" = "server" ] && [ "${GITHUB_ACTIONS:-}" != "true" ]; then
	echo "$APP needs administrator rights to allow itself through the macOS firewall."
	echo "If this account is not an administrator, one can enter their name and password."
	admin_authorize ||
		die "administrator authorization was declined or failed. Nothing has been downloaded or installed. Re-run this command with an administrator's credentials to hand."
fi

tmp="$(/usr/bin/mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
zip="$tmp/$ASSET"

# Where the release assets come from. Normally the latest published Release;
# GROPIUS_ASSET_DIR points this run at a local directory holding the same asset
# names instead. That is what lets the release workflow run THIS script against
# the artefacts it has just built, before they are published — the only moment
# an installer broken in the tagged tree can still be stopped
# (iss-2609081257343394).
#
# It is a CI-ONLY seam and it is refused everywhere else, because `fetch` serves
# BOTH the bundle and the SHA256SUMS.txt the bundle is verified against: point
# it at a directory and the checksum step compares bytes with their own digest,
# which is no integrity control at all. An attacker-authored zip plus a matching
# checksums file would otherwise install, have its quarantine cleared, get a
# firewall rule and be launched, printing "Checksum OK." on the way past.
#
# GITHUB_ACTIONS is a weak gate — it is only an environment variable, and a
# caller who sets one can set two. It is not trying to stop that caller; it
# stops the seam from being reachable by accident, by a stray export, or by a
# tutorial that tells someone to set it, and it makes the substitution loud when
# it does happen.
ASSET_DIR="${GROPIUS_ASSET_DIR:-}"
if [ -n "$ASSET_DIR" ]; then
	[ "${GITHUB_ACTIONS:-}" = "true" ] ||
		die "GROPIUS_ASSET_DIR is a CI-only seam for the release workflow's installer gate, and is refused outside GitHub Actions. It makes this script install from a local directory and verify the checksums against a file in that same directory, so the verification would prove nothing about where the bundle came from. Unset it and rerun to install the published release."
	echo "warning: GROPIUS_ASSET_DIR is set — installing from $ASSET_DIR, NOT from the published GitHub Release." >&2
	echo "warning: the checksums are read from that same directory, so the \"Checksum OK.\" below proves only that the directory is self-consistent. It proves NOTHING about the origin of what is being installed, and no attestation is checked." >&2
fi

# fetch <asset-name> <dest>: download a release asset, trying the public URL
# first and falling back to gh (transient errors, or a private fork).
fetch() {
	local name="$1" dest="$2"
	if [ -n "$ASSET_DIR" ]; then
		/bin/cp "$ASSET_DIR/$name" "$dest" ||
			die "could not read $name from $ASSET_DIR."
		return 0
	fi
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

echo "Downloading ${APP}…"
fetch "$ASSET" "$zip"

# Verify the download is exactly what the release workflow built, BEFORE
# unpacking it, clearing its quarantine, or copying it into /Applications. The
# checksums file comes from the same Release as the asset; --ignore-missing
# skips the other app's line.
echo "Verifying checksum…"
fetch "SHA256SUMS.txt" "$tmp/SHA256SUMS.txt"
# /usr/bin/shasum, not shasum: this line is the only integrity control in the
# whole install path, so the binary that runs it must not be one PATH chose. A
# planted shim is the hostile case, but the ordinary one matters too — a
# Homebrew coreutils or another implementation earlier on PATH need not accept
# `--ignore-missing`, and a checksum check that silently stops checking is worse
# than none, because it still prints reassurance. Verified against this exact
# binary: a matching file exits 0, while a wrong hash, a checksums file naming
# no downloaded file, an empty file and an HTML error page each exit non-zero.
# It fails closed on all four.
#
# The output is captured rather than discarded so the failure says which of
# those fired. Sending it to /dev/null made every cause look identical, and this
# is the one message a user most needs to be able to act on.
if ! checksum_output="$( cd "$tmp" && /usr/bin/shasum -a 256 -c --ignore-missing SHA256SUMS.txt 2>&1 )"; then
	echo "$checksum_output" >&2
	die "checksum mismatch for $ASSET — the download is corrupt or tampered. Refusing to install."
fi
echo "Checksum OK."

# Choose where the bundle goes. /Applications is root:admin and group-writable,
# so a standard (non-admin) account cannot write it — and on a Mac several
# people share, the account that most needs the chat client is exactly the one
# without admin rights. ~/Applications is the per-user location macOS already
# understands: Spotlight and Launchpad index it, and it needs no privileges.
#
# Chosen over elevating on purpose, and the firewall grant above is not a
# precedent for doing so here. That grant is a system-wide setting with no
# per-user equivalent, so it has to be made as an administrator. A destination
# does have a per-user equivalent, and elevating to write /Applications would
# install the app for every account when only one asked for it.
if [ -w /Applications ]; then
	DEST="/Applications"
else
	DEST="$HOME/Applications"
	mkdir -p "$DEST" || die "no write access to /Applications, and $DEST could not be created."
	echo "No write access to /Applications (this account is not an administrator) — installing to $DEST instead."
fi

echo "Installing ${APP}.app to ${DEST}…"
/usr/bin/ditto -x -k "$zip" "$tmp/extract" || die "could not unpack $ASSET."
[ -d "$tmp/extract/$APP.app" ] || die "$ASSET did not contain $APP.app."
# Safe to clear the quarantine now: we have cryptographically verified this .app
# is the exact artifact the release workflow built and signed. (curl downloads
# are usually not quarantined anyway, but a proxy or prior run might have tagged
# it, which would otherwise block launch.)
/usr/bin/xattr -dr com.apple.quarantine "$tmp/extract/$APP.app" 2>/dev/null || true
# Quit a running copy first. LaunchServices' `open` activates an already-running
# process instead of launching the new binary, so an upgrade over a live app
# would report success while the old version keeps running.
if /usr/bin/pgrep -qf "$DEST/$APP.app/Contents/MacOS/" 2>/dev/null; then
	echo "Quitting the running ${APP}…"
	/usr/bin/osascript -e "quit app \"$APP\"" >/dev/null 2>&1 || true
	for _ in $(seq 1 20); do
		/usr/bin/pgrep -qf "$DEST/$APP.app/Contents/MacOS/" || break
		sleep 0.5
	done
	if /usr/bin/pgrep -qf "$DEST/$APP.app/Contents/MacOS/" 2>/dev/null; then
		echo "warning: $APP is still running; quit it and relaunch to finish the upgrade." >&2
	fi
fi
# Stage the new bundle beside the old one, then swap. Copying straight over the
# installed app means deleting it BEFORE knowing the replacement can be written:
# a copy that then fails — a full disk, a locked file, a revoked permission —
# leaves the machine with no app at all, turning an upgrade into a destroyed
# install. Staging first keeps the working copy until the new one is complete,
# and the final move is a rename within one directory.
staged="$DEST/.$APP.app.incoming.$$"
rm -rf "$staged"
cp -R "$tmp/extract/$APP.app" "$staged" || {
	rm -rf "$staged"
	die "could not write $APP.app to $DEST — the installed copy is untouched."
}
rm -rf "$DEST/$APP.app"
mv "$staged" "$DEST/$APP.app" || {
	rm -rf "$staged"
	die "could not move $APP.app into place in $DEST."
}

if [ "$mode" = "client" ]; then
	echo "Installed $DEST/$APP.app."
	echo "Open it, then point it at your Gropius server: the address from the server's Connect tab"
	echo "without the trailing /v1 (GropiusChat adds the path itself)."
	open "$DEST/$APP.app"
	exit 0
fi

# Server: allow it through the macOS Application Firewall so other machines on the
# LAN can reach it. Without this the firewall accepts the handshake but drops the
# data — loopback works, the LAN sees an empty response. This needs administrator
# rights, which were already authorized at the top of this script.
#
# macOS caches that authorization for about five minutes, so this second panel is
# usually collapsed into the first and the user sees no prompt here. A slow
# download can outlive the cache, in which case the panel appears once more —
# hence the line below, so a returning prompt is expected rather than alarming.
#
# The grant is keyed to the binary's code identity, and the bundle is ad-hoc
# signed, so its identity changes with every build. Re-running this script for an
# update therefore has to make the grant again; it is not a one-off.
#
# Skipped in CI along with the gate above: a runner has no console to answer a
# panel, and an authentication prompt with nobody to answer it would hang the
# release gate rather than fail it.
BIN="$DEST/$APP.app/Contents/MacOS/gropius"
if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
	echo "Skipping the firewall grant: no console to authorize it in CI."
elif firewall_grant "$BIN"; then
	echo "Firewall configured."
else
	echo "warning: could not configure the firewall automatically." >&2
	echo "Other machines may see an empty response until an administrator runs:" >&2
	echo "  sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add '$BIN'" >&2
	echo "  sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp '$BIN'" >&2
fi

open "$DEST/$APP.app"
cat <<'DONE'

Gropius is running in the menu bar. Click its icon to open the control panel,
download a model, and copy the address other machines should point at.

Optional:
  • Chat client:   curl -fsSL https://raw.githubusercontent.com/intentdriven/Gropius/main/install.sh | bash -s -- client
  • Shared cache:  every account on this Mac can share one copy of each model —
                   see 'make install-shared' in the repo.
DONE
