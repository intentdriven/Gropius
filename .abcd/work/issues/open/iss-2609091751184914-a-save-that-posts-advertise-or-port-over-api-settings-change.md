---
schema_version: 1
id: "iss-2609091751184914"
slug: "a-save-that-posts-advertise-or-port-over-api-settings-change"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "security review of the posture page, 2026-09-09"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/gateway/control.go"
---

A save that posts advertise or port over /api/settings changes Config() at once, while the Bonjour advert and the listeners are started once at launch: port is flagged as needing a restart, advertise is not, and neither has a control in the Settings pane (advertise is preserved server-side by the form). A script posting advertise:false is told 'saved' and nothing says the advert runs on until the next start. The posture page reads the decision made at start (BindState.Advertising, BindState.Port) rather than the stored values, so it stays truthful; the gap is the save path's silence and the missing control, which the three-surfaces intent itd-2609081259493890 and the restart list in applySettings should close together.
