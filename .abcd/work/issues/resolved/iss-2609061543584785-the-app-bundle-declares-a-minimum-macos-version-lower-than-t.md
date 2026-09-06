---
schema_version: 1
id: "iss-2609061543584785"
slug: "the-app-bundle-declares-a-minimum-macos-version-lower-than-t"
severity: "minor"
category: "bug"
source: "user-observation"
found_during: "2026-09-06 landing-page planning interview"
origin: researcher-authored
production_mode: hand-written
found_at: "build/Info.plist"
resolution: "build/Info.plist is the single declaration of the floor and now says 26.0. Held to it: client/Info.plist, both swiftc deployment targets in client/build.sh, a new sw_vers gate in install.sh that refuses an older Mac before any download, and the phrase \"Requires macOS 26\" in README.md, docs/getting-started.md and client/README.md. internal/archtest reads the plist value and derives every one of those expectations from it, so no surface carries a second copy of the number to drift from."
impact: fix
---

The app bundle declares a minimum macOS version lower than the one the product requires. The maintainer set the supported floor to macOS 26 on 2026-09-06 (the release runner is macOS 26, the chat client uses macOS 26 button styles, the README says tested on 26), while build/Info.plist still declares an older minimum, so an older Mac is allowed to install and fails later instead of being refused at launch. Raise the declared minimum to match the stated requirement and say the requirement in the README and the landing page in the same words.

## Grounds

- pursued: an older Mac is refused at launch instead of failing later, and every surface states the same requirement; we are wrong if a supported Mac is refused or the surfaces drift again.
