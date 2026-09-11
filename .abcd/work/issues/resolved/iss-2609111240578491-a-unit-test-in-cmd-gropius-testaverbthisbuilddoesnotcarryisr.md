---
schema_version: 1
id: "iss-2609111240578491"
slug: "a-unit-test-in-cmd-gropius-testaverbthisbuilddoesnotcarryisr"
severity: "major"
category: "observation"
source: "user-observation"
found_during: "merging lifecycle phases A and B, 2026-09-11"
origin: researcher-authored
production_mode: hand-written
found_at: "cmd/gropius/verbs.go"
resolution: "The cmd/gropius verb runner takes its environment from verbEnvFor, a variable; TestMain replaces it and the writing entries of lifecycleVerbs with recording fakes before any test runs; and internal/lifecycle refuses to build a live install or uninstall environment inside a test binary (testing.Testing()), with one named, single-test opt-in for the case that needs the live root resolution. A guard test asserts the defaults in the test binary are the fakes, and was watched to fail when TestMain's assignment was removed."
impact: internal
resolved_by:
  intent: "itd-2609081259532589"
  spec: "spc-2609111029315861"
  commit: "0ae8744"
---

A unit test in cmd/gropius (TestAVerbThisBuildDoesNotCarryIsRefusedByName, written against phase A) dispatched install and uninstall through the live verb runner once phase B wired them, and so ran a real install on the developer's Mac: it asked a running copy to quit and raised the macOS administrator panel twice, hanging the suite for ten minutes. Nothing was written for this account, but the hazard is structural: the cmd/gropius verb runner builds its environment from the live process (home, /Applications, osascript, the provisioner) with nothing a test can substitute, so any test that reaches runCommandVerb with a writing verb acts on the machine. The runner needs an injectable environment and a test asserting that no test path can reach the live one.

## Grounds

- pursued: a verb runner whose world is a variable, with the live builders refusing inside a test binary, stops the accident structurally rather than by convention; wrong if a test is later found acting on the machine despite both — which would mean the seam is in the wrong place rather than missing
