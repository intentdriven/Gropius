---
schema_version: 1
id: "iss-2609070157013643"
slug: "testpanicisloggedwithouttheclientaddress-is-flaky-it-asserts"
severity: "nitpick"
category: "bug"
source: "user-observation"
found_during: "eviction-grace branch, full suite run"
origin: researcher-authored
production_mode: hand-written
found_at: "cmd/gropius/logging_test.go"
resolution: "The panic-log leak assertion now looks for the client address only in forms a hex pointer cannot spell, and a new test pins that behaviour."
impact: internal
---

TestPanicIsLoggedWithoutTheClientAddress is flaky: it asserts the panic log does not contain the client's port, 54321, but the log carries a goroutine stack full of hex pointers, and a pointer such as 0x1054321c0 contains that digit sequence. Observed failing once and passing on every re-run. The assertion needs to look for the address in a form a hex pointer cannot spell — the host and port together, or the port with a delimiter.

## Grounds

- pursued: the test no longer reds when a goroutine stack hex pointer spells the port; a real leak in any logged form still fails it. Wrong if a leak reaches the log in a form none of the four checks cover.
