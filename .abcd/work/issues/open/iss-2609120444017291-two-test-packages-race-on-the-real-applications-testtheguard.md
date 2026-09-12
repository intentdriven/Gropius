---
schema_version: 1
id: "iss-2609120444017291"
slug: "two-test-packages-race-on-the-real-applications-testtheguard"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
---

Two test packages race on the real /Applications: TestTheGuardCanBeOpenedForOneTest (internal/lifecycle/liveguard_test.go) builds the live install environment, whose installDest asks writableDir of /Applications by creating and removing a .doctor-*.tmp file there, while the installer-gate tripwire (internal/archtest/installer_gate_test.go) snapshots /Applications before and after each install.sh run. go test runs the packages concurrently, and on a Mac where /Applications is writable (every CI runner) the probe's file can be in one snapshot and not the other. Seen once on PR 50 in the merge queue. The tripwire now leaves that one name out of its listing; the underlying fact stands that a unit test creates a file in the machine's /Applications, which is the class iss-2609111240578491 exists to stop, and the destination probe has no seam a test can point elsewhere.
