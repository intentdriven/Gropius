---
schema_version: 1
id: "iss-2609080855033159"
slug: "installing-from-a-non-admin-account-fails-and-macos-moved-th"
severity: "major"
category: "bug"
source: "user-observation"
found_during: "release testing on macOS 26 across two accounts, 2026-09-08"
origin: researcher-authored
production_mode: hand-written
found_at: "install.sh"
---

Installing from a non-admin account fails, and macOS moved the installed server app to the Trash. Two symptoms from one test on macOS 26. First, install.sh copies the bundle straight to /Applications with no writability check, no fallback and no error handling on the copy, so a standard (non-admin) account gets a bare 'Permission denied'; /Applications is root:admin drwxrwxr-x, writable only by the admin group. This bites hardest on the chat client, which is exactly the app a secondary non-admin user on a shared Mac needs, while the README advertises multi-account use and the shared model cache supports it. Second, the server app that Spotlight found was relocated to the Trash by the operating system rather than merely refused, which is current macOS behaviour for a quarantined bundle that is ad-hoc signed and not notarized (spctl rejects it; no notarization ticket is stapled). The maintainer reports both apps worked on macOS 26 before the product rename, so at least the second symptom looks like a regression. Hypothesis, not yet confirmed: the rename changed the bundle identifier to [redacted-user].gropius.app, and every macOS trust decision already granted to the previous identifier (the Gatekeeper approval a user had clicked through, the firewall entry, the Local Network Privacy grant) is keyed to the old identifier and does not carry over, so the renamed app meets the system as an app it has never seen. The Makefile already records that this class of breakage exists, warning that an unstable signing identifier makes every build look like a different app to the firewall and to Local Network Privacy. What would show the hypothesis wrong: a freshly downloaded build of the previous name, quarantined the same way on the same macOS version, being trashed too. Note the first symptom is probably not a regression at all, since installing from a non-admin account is a path that was likely never exercised before; the old app was almost certainly installed once by an admin and merely launched by the other account. Separately and independently of both: install.sh removes the installed bundle before it copies the replacement, and unlike every other fallible step in that script the copy carries no failure handler, so on an admin account any copy failure leaves the user with no app at all and turns an upgrade into a destroyed install.
