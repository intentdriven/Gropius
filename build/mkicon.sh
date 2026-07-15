#!/bin/bash
# Build AppIcon.icns from the 1024px source using Apple's own tooling.
set -e
cd "$(dirname "$0")"
[ -f AppIcon.icns ] && [ AppIcon.icns -nt icon-1024.png ] && exit 0
rm -rf AppIcon.iconset && mkdir AppIcon.iconset
for sz in 16 32 64 128 256 512; do
  sips -z $sz $sz icon-1024.png --out AppIcon.iconset/icon_${sz}x${sz}.png >/dev/null
  sips -z $((sz*2)) $((sz*2)) icon-1024.png --out AppIcon.iconset/icon_${sz}x${sz}@2x.png >/dev/null
done
cp icon-1024.png AppIcon.iconset/icon_512x512@2x.png
iconutil -c icns AppIcon.iconset -o AppIcon.icns
rm -rf AppIcon.iconset
echo "AppIcon.icns built"
