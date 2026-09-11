---
schema_version: 1
id: "iss-2609111942567418"
slug: "the-control-plane-publishes-no-version-field-so-gropius-upda"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "implementing spc-2609111812370705, the update verb"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/gateway/control.go"
---

The control plane publishes no version field, so gropius update reports the serving version as "cannot be determined" on every Mac

## What is missing

`spc-2609111812370705` asks for a read-only `version` field on the state
snapshot the control panel already polls — the smallest addition that makes
`gropius update`'s report truthful, and one the panel gets for free because it
already reads that route. `internal/gateway` is another lane's territory, so
the field is a coordinated change and was not made here.

## What ships without it

The fallback the intent names, and it costs no criterion: the serving version
is reported as `cannot be determined — the running server does not publish its
version` wherever it cannot be read, and the version just installed is never
put in its place. The output is poorer rather than untrue.

## What closing it takes

1. A `version` field on `gateway.State`, read-only, inside the loopback-only
   surface, reaching nothing that writes.
2. Moving the decode out of `fetchServingVersion` in
   `internal/lifecycle/update.go` onto `ServerState` in `status.go`, and adding
   `version` to `statusReads` in `internal/lifecycle/statecontract_test.go` —
   the contract test that holds every field that struct declares to one the
   gateway publishes, and which is exactly why the decode sits apart today.
3. The control panel showing both versions, which belongs to
   itd-2609081259493890 rather than here.

Until then `TestTheServingVersionIsDecodedFromTheSnapshot` proves the decode
works against a snapshot that carries the field, so the day the field lands the
report starts telling the truth with no further change on this side.
