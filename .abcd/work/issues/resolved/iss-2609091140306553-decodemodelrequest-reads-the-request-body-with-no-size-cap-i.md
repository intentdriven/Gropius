---
schema_version: 1
id: "iss-2609091140306553"
slug: "decodemodelrequest-reads-the-request-body-with-no-size-cap-i"
severity: "nitpick"
category: "bug"
source: "user-observation"
found_during: "independent security review of fix/gateway-bounds-and-settings-serialization"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/gateway/control.go"
resolution: "decodeModelRequest now reads the body through http.MaxBytesReader at config.MaxConfigBytes, the settings save's cap, in the one helper the five model-action endpoints share; an over-cap body is refused 400 'request body is too large'."
impact: fix
---

decodeModelRequest reads the request body with no size cap: internal/gateway/control.go decodes r.Body straight into the model-name struct for /api/models/load, /api/models/unload, /api/models/download, /api/models/cancel and /api/models/delete, with no http.MaxBytesReader in front of it. The two handlers beside it are both capped — the settings save at config.MaxConfigBytes and the completions handler at maxRequestBody — so this is the odd one out rather than a considered exception. Loopback-only, so it is a local caller or a hostile browser tab, and the decoder stops at the end of the first JSON value; the cost is the buffering json.Decoder does on the way there. Noticed while adding the per-model load dedup, whose map now keys on the model name this decodes.

## Grounds

- pursued: every model-action endpoint refuses a body over config.MaxConfigBytes with a 400 that names it, while an ordinary model-name body is answered exactly as before; TestModelActionsRefuseAnOversizedBody failing on either half would show it wrong, as would a new model-action handler decoding r.Body directly instead of through the helper.
