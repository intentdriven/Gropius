---
schema_version: 1
id: "iss-2609081743033285"
slug: "gropiuschat-s-message-bubbles-do-not-read-as-speech-bubbles"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "maintainer walkthrough of the chat client on a standard account"
origin: researcher-authored
production_mode: hand-written
found_at: "client/GropiusChat/GropiusChat.swift"
resolution: "Messages are bubbles again: the person's right-aligned in an accent tint, the model's left-aligned in a neutral fill, both from the platform's own colours."
impact: fix
---

GropiusChat's message bubbles do not read as speech bubbles. The chat transcript renders messages as plain rows (MessageRow in client/GropiusChat/GropiusChat.swift), so a conversation reads as a list rather than as an exchange, and the eye has no quick cue for which side spoke. Wanted: bubble treatment that distinguishes the person from the model at a glance — shape, alignment and background, in the platform's idiom rather than an imitation of another chat app.

## Grounds

- pursued: a glance says which side spoke, in light and dark, under any accent and under Increase Contrast, with no fixed colour value anywhere in the row; shown wrong by an appearance in which either bubble is indistinguishable from the transcript behind it, which is how the first build of this was caught.
