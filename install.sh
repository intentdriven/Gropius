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
# This is a BOOTSTRAP, and only a bootstrap: it does what has to happen before a
# Gropius binary exists on this Mac. It downloads the latest release, verifies
# it against the checksums published beside it, clears the quarantine attribute,
# and then — for the server — hands over to `gropius install` inside the bundle
# it just verified. Everything after that is the binary's own work: the staged
# swap, the firewall grant, the MLX runtime, the per-user command and the
# launch. A binary fetched by curl is not Gatekeeper-quarantined, so no
# right-click-to-open dance.
#
# The client has no such binary, so its bundle is placed here.
#
# EVERY COMMAND IS NAMED BY ABSOLUTE PATH. `curl | bash` runs with the invoking
# user's PATH, which routinely puts user-writable directories ahead of /usr/bin,
# and on a Mac several accounts share that is an account-to-account boundary
# (iss-2609081435387952). It is also a defence against ambiguity with no
# attacker in it at all: a release step once resolved a name to a tool that was
# not the tool meant, invisibly (iss-9). The rule is held by
# TestInstallerPinsEveryCommandItRuns, which refuses any command here that is
# not written as a path.
#
# NOTHING READS STANDARD INPUT. Under `curl … | bash` the remaining text of this
# script IS standard input, so a read would consume the rest of the installer.
# The one step that needs consent — the firewall grant — is asked for by the
# binary, through the system authorisation panel, which is also the only way a
# standard account can answer it at all.
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

[ "$(/usr/bin/uname -s)" = "Darwin" ] || die "Gropius is macOS only."

# Both bundles declare macOS 26 as their minimum, so Launch Services refuses
# them on anything older. Refuse here instead — before the download and before
# anything is written — so an unsupported Mac is turned away rather than
# half-installed. The major lives in this one variable; build/Info.plist is the
# value it must match.
MIN_MACOS_MAJOR=26
# `|| macos_version=""` is load-bearing: under `set -e` a bare assignment takes
# the command substitution's status, so a missing sw_vers would abort the script
# with no message at all instead of reaching the refusal below. An unreadable or
# non-numeric version leaves the major empty or unusable, and `[` refuses then
# too.
macos_version="$(/usr/bin/sw_vers -productVersion 2>/dev/null)" || macos_version=""
macos_major="${macos_version%%.*}"
[ "${macos_major:-0}" -ge "$MIN_MACOS_MAJOR" ] ||
	die "$APP requires macOS $MIN_MACOS_MAJOR (this Mac runs ${macos_version:-an unreadable version})."

# The server needs Apple Silicon (MLX runs on Metal). The client is universal.
# `uname -m` reports x86_64 in a Rosetta-translated shell (common with x86_64
# Homebrew), so also ask the kernel whether the hardware is Apple Silicon.
if [ "$mode" = "server" ] && [ "$(/usr/bin/uname -m)" != "arm64" ] &&
	[ "$(/usr/sbin/sysctl -n hw.optional.arm64 2>/dev/null)" != "1" ]; then
	die "the Gropius server needs Apple Silicon (this Mac is $(/usr/bin/uname -m)). The GropiusChat client is universal: rerun with 'client'."
fi

tmp="$(/usr/bin/mktemp -d)"
trap '/bin/rm -rf "$tmp"' EXIT
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

# fetch <asset-name> <dest>: download a release asset.
#
# The GitHub CLI fallback this function used to carry is gone. `gh` is a
# third-party tool with no fixed location — it cannot be named by absolute path
# on a machine nobody here controls — so keeping it meant one PATH-resolved
# binary in the middle of the only download path there is, for a fallback that
# helped a private fork and a transient error. Building from source and `make
# install` cover both, and neither is a script the README tells people to pipe
# into bash.
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
	/usr/bin/curl -q --proto =https --proto-redir =https -fsSL -o "$dest" "https://github.com/$REPO/releases/latest/download/$name" ||
		die "could not download $name from the latest release. Check your network and retry, or build from source (see the README)."
}

echo "Downloading ${APP}…"
fetch "$ASSET" "$zip"

# Verify the download is exactly what the release workflow built, BEFORE
# unpacking it, clearing its quarantine, or placing it. The checksums file comes
# from the same Release as the asset; --ignore-missing skips the other app's
# line.
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

# The client is placed by this script, because there is no GropiusChat binary
# that could place itself. Choose where it goes before unpacking, so the line
# that says where it is going is printed before the work starts.
#
# /Applications is root:admin and group-writable, so a standard (non-admin)
# account cannot write it — and on a Mac several people share, the account that
# most needs the chat client is exactly the one without admin rights.
# ~/Applications is the per-user location macOS already understands: Spotlight
# and Launchpad index it, and it needs no privileges.
#
# Chosen over elevating on purpose. A destination has a per-user equivalent, and
# elevating to write /Applications would install the app for every account when
# only one asked for it. The server's own copy of this rule lives in
# internal/lifecycle, which is what places the server bundle.
if [ "$mode" = "client" ]; then
	if [ -w /Applications ]; then
		DEST="/Applications"
	else
		DEST="$HOME/Applications"
		/bin/mkdir -p "$DEST" || die "no write access to /Applications, and $DEST could not be created."
		echo "No write access to /Applications (this account is not an administrator) — installing to $DEST instead."
	fi
	echo "Installing ${APP}.app to ${DEST}…"
else
	echo "Unpacking ${APP}…"
fi

/usr/bin/ditto -x -k "$zip" "$tmp/extract" || die "could not unpack $ASSET."
[ -d "$tmp/extract/$APP.app" ] || die "$ASSET did not contain $APP.app."
# Safe to clear the quarantine now: we have verified this .app is the exact
# artifact the release workflow built. (curl downloads are usually not
# quarantined anyway, but a proxy or prior run might have tagged it, which would
# otherwise block launch.)
/usr/bin/xattr -dr com.apple.quarantine "$tmp/extract/$APP.app" 2>/dev/null || true

if [ "$mode" = "server" ]; then
	# HAND OVER TO THE BINARY THIS SCRIPT VERIFIED — the one inside the bundle
	# in $tmp, never the one already installed. The bootstrap and the binary it
	# calls are then always the same build, which is what makes the two halves
	# of an install a single thing rather than a negotiation between a script
	# from one release and an application from another.
	VERIFIED_BIN="$tmp/extract/$APP.app/Contents/MacOS/gropius"
	[ -x "$VERIFIED_BIN" ] ||
		die "$ASSET carries no executable at $APP.app/Contents/MacOS/gropius — refusing to install it."

	handover=("$VERIFIED_BIN" install --bundle "$tmp/extract/$APP.app")
	if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
		# Say so, loudly, rather than passing silently. The release gate runs
		# this script to prove the installer works against the artefacts just
		# built; a runner has no console to answer an authentication panel and
		# no business downloading an MLX runtime, so the handover places the
		# bundle and stops there. A green run that does not say what it did not
		# look at is a false green.
		echo "warning: passing --place-only — CI has no console for an authentication panel and no business provisioning a runtime." >&2
		echo "warning: a green result from this run therefore says NOTHING about the firewall grant, the MLX provisioning or the launch. They are exercised only by a real install on a Mac." >&2
		handover+=(--place-only)
	fi

	# The exit status is read rather than left to `set -e`, because ONE of its
	# values means something specific: a bundle whose binary predates the
	# lifecycle verbs refuses an argument it does not know with exit 2, and that
	# refusal is a version mismatch rather than a failed install. In no case
	# does this script start a server — the binary does that, or nothing does.
	status=0
	"${handover[@]}" || status=$?
	if [ "$status" -eq 2 ]; then
		die "the downloaded $APP does not carry the lifecycle verbs: its binary refused \`install\` with exit 2, which is how a build older than this bootstrap refuses an argument it has never heard of. The script and the bundle are different builds. Nothing was launched."
	elif [ "$status" -ne 0 ]; then
		die "gropius install stopped (exit $status) — the message above says at which stage. Nothing was launched."
	fi
	exit 0
fi

# From here it is the client, which this script places itself.
#
# Quit a running copy first. LaunchServices' `open` activates an
# already-running process instead of launching the new binary, so an upgrade
# over a live app would report success while the old version keeps running.
if /usr/bin/pgrep -qf "$DEST/$APP.app/Contents/MacOS/" 2>/dev/null; then
	echo "Quitting the running ${APP}…"
	/usr/bin/osascript -e "quit app \"$APP\"" >/dev/null 2>&1 || true
	for _ in $(/usr/bin/seq 1 20); do
		/usr/bin/pgrep -qf "$DEST/$APP.app/Contents/MacOS/" || break
		/bin/sleep 0.5
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
#
# The staging name comes from mktemp, not from the pid. `.$APP.app.incoming.$$`
# is predictable, which hands anyone watching the directory a reliable signal
# for when to act on it.
#
# What this does NOT fix, stated plainly so nobody reads it as settled: `mv`
# nests into a destination that already exists as a directory and follows one
# that is a symlink, exiting 0 in both cases. It has no dependable "fail if the
# destination exists" mode, and any test-then-move is a race by construction.
# Closing that needs os.Rename semantics — Go, not shell — which is where the
# SERVER's swap now lives (internal/lifecycle/swap.go, with the behavioural
# tests beside it). The client has no binary of its own to do the same, so this
# path keeps the best a shell can do and says what that is worth.
staged="$(/usr/bin/mktemp -d "$DEST/.$APP.incoming.XXXXXXXX")" ||
	die "could not create a staging directory in $DEST."
/bin/cp -R "$tmp/extract/$APP.app" "$staged/$APP.app" || {
	/bin/rm -rf "$staged"
	die "could not write $APP.app to $DEST — the installed copy is untouched."
}

# Move the old bundle ASIDE rather than deleting it, put the new one in place,
# and only then delete what was set aside. The previous order deleted the
# installed bundle first and, on a failed rename, deleted the staged copy too —
# so an ordinary rename failure left NO application at all, which is precisely
# the outcome the paragraph above says staging exists to prevent. It needed no
# attacker and no unusual filesystem: one failing rename was enough. Every
# failure path below now ends with a working bundle at the destination.
retired=""
if [ -e "$DEST/$APP.app" ] || [ -L "$DEST/$APP.app" ]; then
	retired="$staged/$APP.app.retired"
	/bin/mv "$DEST/$APP.app" "$retired" || {
		/bin/rm -rf "$staged"
		die "could not set the installed $APP.app aside in $DEST — it is untouched."
	}
fi
/bin/mv "$staged/$APP.app" "$DEST/$APP.app" || {
	# Put the old bundle back before giving up, so a failure here is a no-op
	# rather than an uninstall.
	[ -n "$retired" ] && /bin/mv "$retired" "$DEST/$APP.app" 2>/dev/null
	/bin/rm -rf "$staged"
	die "could not move $APP.app into place in $DEST — the previous copy is left as it was."
}
/bin/rm -rf "$staged"
echo "placed $DEST/$APP.app."

# CI has no desktop to launch into, and a chat client left running on a runner
# outlives the job. Everywhere else this is the last thing the script does, so
# the line above is what proves a run reached the end.
if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
	echo "warning: not launching $DEST/$APP.app (CI) — this run does not exercise the launch." >&2
else
	/usr/bin/open "$DEST/$APP.app"
fi
/bin/cat <<'DONE'

Open GropiusChat, then point it at your Gropius server: the address from the
server's Connect tab without the trailing /v1 (GropiusChat adds the path
itself).
DONE
