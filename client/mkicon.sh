#!/usr/bin/env bash
# Regenerate icon/AppIcon.icns from icon/icon.svg using Apple's own tooling.
# Needs rsvg-convert (brew install librsvg). The committed AppIcon.icns means
# this is only necessary when the source art changes.
set -euo pipefail
cd "$(dirname "$0")/icon"

rsvg-convert -w 1024 -h 1024 icon.svg -o icon-1024.png

rm -rf AppIcon.iconset && mkdir AppIcon.iconset
for sz in 16 32 64 128 256 512; do
  sips -z "$sz" "$sz" icon-1024.png --out "AppIcon.iconset/icon_${sz}x${sz}.png" >/dev/null
  sips -z "$((sz*2))" "$((sz*2))" icon-1024.png --out "AppIcon.iconset/icon_${sz}x${sz}@2x.png" >/dev/null
done
cp icon-1024.png AppIcon.iconset/icon_512x512@2x.png
iconutil -c icns AppIcon.iconset -o AppIcon.icns
rm -rf AppIcon.iconset
echo "icon/AppIcon.icns rebuilt"
