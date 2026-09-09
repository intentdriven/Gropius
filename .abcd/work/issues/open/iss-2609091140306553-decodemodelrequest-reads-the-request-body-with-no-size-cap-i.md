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
---

decodeModelRequest reads the request body with no size cap: internal/gateway/control.go decodes r.Body straight into the model-name struct for /api/models/load, /api/models/unload, /api/models/download, /api/models/cancel and /api/models/delete, with no http.MaxBytesReader in front of it. The two handlers beside it are both capped — the settings save at config.MaxConfigBytes and the completions handler at maxRequestBody — so this is the odd one out rather than a considered exception. Loopback-only, so it is a local caller or a hostile browser tab, and the decoder stops at the end of the first JSON value; the cost is the buffering json.Decoder does on the way there. Noticed while adding the per-model load dedup, whose map now keys on the model name this decodes.
