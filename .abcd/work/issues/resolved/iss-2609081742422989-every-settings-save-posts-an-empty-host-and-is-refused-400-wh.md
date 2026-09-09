---
schema_version: 1
id: "iss-2609081742422989"
slug: "every-settings-save-posts-an-empty-host-and-is-refused-400-wh"
severity: "major"
category: "bug"
source: "agent-finding"
found_during: "2026-09-08 design review of itd-2609081718534201"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/ui/static/index.html"
resolution: "The settings pane renders the stored bind host as an extra <option> before assigning it, so a host the select never offered — localhost, IPv6 loopback, one specific address — round-trips instead of posting the empty string an unoffered HTMLSelectElement value yields, and every save on such a bind is accepted again."
impact: fix
---

Every settings save posts an empty host and is refused with 400, whenever the bind host is not one of the two the select offers.

The bind-address control is a <select> with exactly two hard-coded options, 0.0.0.0 and 127.0.0.1 (internal/ui/static/index.html:192-195). renderSettings assigns the stored host into it (internal/ui/static/app.js:515, `$('setHost').value = c.host`), and the submit handler posts that back verbatim (internal/ui/static/app.js:975, `host: $('setHost').value`). HTMLSelectElement.value has no notion of a value the element does not offer: assigning one sets selectedIndex to -1 and leaves .value as the empty string. So for any stored host outside those two literals, the form posts host:"" on EVERY save — including saves that change nothing about the bind. config.Validate refuses it ("host must not be empty", internal/config/config.go:910-911), App.SetConfig returns that error (internal/app/app.go:285-287) and handleSetSettings turns it into 400 (internal/gateway/control.go:1014-1017). The operator sees a save fail, naming a field the panel never showed them as wrong, and no setting on the pane can be saved until they edit config.json by hand.

Verified, 2026-09-08:
- DOM half, in a headless Chromium against the shipped markup for the select, replaying app.js:515 then app.js:975: "0.0.0.0" posts "0.0.0.0"; "127.0.0.1" posts "127.0.0.1"; "localhost", "127.0.0.2", "[::1]" and "192.0.2.5" each post "" with selectedIndex -1.
- Server half, a real POST /api/settings through the control mux with host:"": 400, body {"error":{"code":400,"message":"host must not be empty","type":"invalid_request_error"}}.
- config.Validate accepts "localhost", "[::1]", "127.0.0.2" and "192.0.2.5" and refuses only "". So each of those is a bind an operator can write and Gropius will start on, and each of them breaks the pane.

Which of those an operator can actually hit today is narrower than it first looks, and the difference matters for the fix:
- "localhost" and "[::1]" are the live cases. Both bind, both leave the control plane fully reachable — loopbackOnly accepts "localhost", "127.0.0.1" and "::1" as Host headers (internal/gateway/control.go:140-148) — so the panel opens, renders, and then refuses every save.
- "127.0.0.2" binds and is loopback by RemoteAddr, but its Host header is not on that three-name allow-list, so the panel answers 403 first. That is iss-7's fault, not this one.
- A specific non-loopback address is unreachable today for the reason iss-7 records. It stops being unreachable under the decision taken on iss-7 on 2026-09-08 — loopback is always in the bind — at which point every specific-address bind reaches the panel and lands here. So resolving iss-7 widens this bug from two spellings of loopback to the whole configuration iss-7 exists to make usable.

Relation to the recorded advice: iss-7 records that the settings UI offers only the wildcard and loopback, so a narrowed bind needs a hand-edited config.json, and that editing the same file back is the recovery. adr-2609081118587999 rule 3 makes that hand-edited bind the ONLY admissible way to narrow exposure. This bug is the second half of the same sharp edge: the operator who follows that route keeps a working /v1 API and loses the whole settings pane, with an error message that points at nothing they typed.

Fixes are cheap and are a choice rather than a discovery, which is why this is captured rather than fixed in passing: render the stored host as an extra <option> when it matches neither literal (smallest, keeps the control a select and makes the value round-trip); or make the control a datalist-backed text input (matches what Validate actually accepts, and puts a free-text bind address in the UI, which is a bind-surface decision); or have the form omit host entirely when it is unchanged, since handleSetSettings already preserves fields the body does not name. The third is the smallest change that stops the data loss but leaves the panel displaying a bind that is not the one in force.

## Grounds

- pursued: we expect the pane to show and save the bind in force for every host config.Validate accepts, because the select is now given an option for whatever host is stored before the value is assigned, and the option added last time is dropped first so a bind that changes does not accumulate dead addresses. We are wrong if a host reaches the panel that the select can hold but the server then refuses — the round trip is only as wide as config.Validate — or if some later code assigns setHost.value without calling renderBindOptions first, which would silently return the empty-string bug; the seam test on renderSettings is what would catch the second. Pursued now rather than deferred because resolving iss-7 widens the bug from two spellings of loopback to every specific-address bind.
