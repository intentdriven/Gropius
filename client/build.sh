#!/usr/bin/env bash
# Build GropiusChat.app from a single Swift source, no Xcode project.
#
# Produces dist/GropiusChat.app, ad-hoc signed. For handing to another Mac see
# README.md (Gatekeeper / quarantine notes).
set -euo pipefail

cd "$(dirname "$0")"

APP="GropiusChat"
BUNDLE="dist/$APP.app"
MACOS="$BUNDLE/Contents/MacOS"
RES="$BUNDLE/Contents/Resources"

echo "Compiling $APP..."
rm -rf "$BUNDLE"
mkdir -p "$MACOS" "$RES"

# Universal binary (arm64 + x86_64) so it runs on any Mac, Apple Silicon or Intel.
swiftc -O -parse-as-library \
    -target arm64-apple-macos14.0 \
    -o "$MACOS/$APP-arm64" \
    GropiusChat/GropiusChat.swift

if swiftc -target x86_64-apple-macos14.0 -O -parse-as-library -o "$MACOS/$APP-x86_64" \
        GropiusChat/GropiusChat.swift 2>/dev/null; then
    lipo -create -output "$MACOS/$APP" "$MACOS/$APP-arm64" "$MACOS/$APP-x86_64"
    rm -f "$MACOS/$APP-arm64" "$MACOS/$APP-x86_64"
    echo "Built a universal (arm64 + x86_64) binary."
else
    mv "$MACOS/$APP-arm64" "$MACOS/$APP"
    echo "Built an arm64-only binary (x86_64 SDK not available)."
fi

cp Info.plist "$BUNDLE/Contents/Info.plist"

# Stamp the bundle version (VERSION without a leading v) so Finder's Get Info
# distinguishes client releases, same as the server bundle. Unset keeps the
# plist's default for local dev builds; the release workflow passes the tag.
if [ -n "${VERSION:-}" ]; then
    /usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString ${VERSION#v}" \
        "$BUNDLE/Contents/Info.plist"
fi

# App icon. Regenerate the .icns from the source art if it is missing.
if [ ! -f icon/AppIcon.icns ]; then ./mkicon.sh; fi
cp icon/AppIcon.icns "$RES/AppIcon.icns"

# A stable ad-hoc identity keeps Local Network Privacy from re-prompting on every
# build. This is NOT a Developer-ID signature — see README for distribution.
codesign --force --identifier dev.gropius.chat --sign - "$BUNDLE"

echo "Built $BUNDLE"
echo "Run it with:  open $BUNDLE"
