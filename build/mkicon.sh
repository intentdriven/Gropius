#!/bin/bash
# Rebuild AppIcon.icns from icon.svg using Apple's own tooling.
#
# Run BY HAND when the art changes (`make icon`). Nothing in the build or the
# release path depends on it: `make app` copies the committed AppIcon.icns, so a
# clean checkout — and the release runner, which has no librsvg — builds the app
# without ever reaching this script.
#
# It refuses rather than skipping when rsvg-convert is absent. A skip would leave
# an edited icon.svg and a stale .icns and say nothing; the recorded source hash
# beside the .icns is what makes that staleness visible afterwards.
set -e
cd "$(dirname "$0")"

if ! command -v rsvg-convert >/dev/null 2>&1; then
  echo "mkicon: rsvg-convert is not installed (brew install librsvg); AppIcon.icns is committed art and is not rebuilt without it" >&2
  exit 1
fi

rsvg-convert -w 1024 -h 1024 icon.svg -o icon-1024.png
rm -rf AppIcon.iconset && mkdir AppIcon.iconset
for sz in 16 32 64 128 256 512; do
  sips -z $sz $sz icon-1024.png --out AppIcon.iconset/icon_${sz}x${sz}.png >/dev/null
  sips -z $((sz*2)) $((sz*2)) icon-1024.png --out AppIcon.iconset/icon_${sz}x${sz}@2x.png >/dev/null
done
cp icon-1024.png AppIcon.iconset/icon_512x512@2x.png
iconutil -c icns AppIcon.iconset -o AppIcon.icns
rm -rf AppIcon.iconset
# The art this .icns was rasterized from. internal/archtest compares it with
# icon.svg's current hash, so editing the SVG without rerunning `make icon`
# fails the suite instead of shipping an icon nobody drew.
shasum -a 256 icon.svg | awk '{print $1}' > AppIcon.icns.source-sha256
echo "AppIcon.icns built"
