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
---

A unit test in cmd/gropius (TestAVerbThisBuildDoesNotCarryIsRefusedByName, written against phase A) dispatched install and uninstall through the live verb runner once phase B wired them, and so ran a real install on the developer's Mac: it asked a running copy to quit and raised the macOS administrator panel twice, hanging the suite for ten minutes. Nothing was written for this account, but the hazard is structural: the cmd/gropius verb runner builds its environment from the live process (home, /Applications, osascript, the provisioner) with nothing a test can substitute, so any test that reaches runCommandVerb with a writing verb acts on the machine. The runner needs an injectable environment and a test asserting that no test path can reach the live one.
