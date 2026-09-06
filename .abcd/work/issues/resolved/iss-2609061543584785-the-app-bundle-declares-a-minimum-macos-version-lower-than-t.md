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
resolution: "build/Info.plist declares the floor and now says 26.0. Every other declared minimum is held to it by internal/archtest, which reads the plist value and derives what it expects of each: client/Info.plist, both swiftc deployment targets in client/build.sh, the MIN_MACOS_MAJOR gate added to install.sh so an older Mac is refused before any download, and the phrase \"Requires macOS 26\" in README.md, docs/getting-started.md and client/README.md. Prose that names the version outside that phrase (the README's tested-on line, the client README's SDK requirement) is not read by the test and still needs a human edit when the floor moves. The landing page's statement of the requirement is not part of this fix: it arrives with spc-2609061822370424, whose criterion 5 adds internal/sitetest to hold the page to this same plist value."
impact: breaking
---

The app bundle declares a minimum macOS version lower than the one the product requires. The maintainer set the supported floor to macOS 26 on 2026-09-06 (the release runner is macOS 26, the chat client uses macOS 26 button styles, the README says tested on 26), while build/Info.plist still declares an older minimum, so an older Mac is allowed to install and fails later instead of being refused at launch. Raise the declared minimum to match the stated requirement and say the requirement in the README and the landing page in the same words.

## Grounds

- pursued: an older Mac is refused at launch instead of failing later, and every surface states the same requirement; we are wrong if a supported Mac is refused or the surfaces drift again.
