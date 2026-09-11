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
resolution: "Both halves closed. advertise joins the restart list in applySettings, so a save that changes it is answered with restart=true — the advert is started once at launch, and a script posting advertise:false was previously told 'saved' while the server went on announcing itself. And the Settings pane gains a control for it, under a fieldset of its own with the hint that a change takes effect at the next start, so the setting is no longer reachable only by hand-editing config.json. The posture page is unchanged and stays truthful: it reads the decision taken at start, never the stored value."
impact: fix
resolved_by:
  intent: "itd-2609081259493890"
  spec: "spc-2609111941481833"
  commit: "fe723bd2543eef0f6cb8ce2aa197b1325df6a5c1"
---

A save that posts advertise or port over /api/settings changes Config() at once, while the Bonjour advert and the listeners are started once at launch: port is flagged as needing a restart, advertise is not, and neither has a control in the Settings pane (advertise is preserved server-side by the form). A script posting advertise:false is told 'saved' and nothing says the advert runs on until the next start. The posture page reads the decision made at start (BindState.Advertising, BindState.Port) rather than the stored values, so it stays truthful; the gap is the save path's silence and the missing control, which the three-surfaces intent itd-2609081259493890 and the restart list in applySettings should close together.

## Grounds

- pursued: we expect a restart notice plus a control to close the silence, because the gap was a stored value changing at once while the thing it decides is read only at launch; shown wrong if operators read 'restart needed' as 'the save did not work' and stop trusting the notice on the fields that have always carried it
