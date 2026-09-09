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
---

GropiusChat's message bubbles do not read as speech bubbles. The chat transcript renders messages as plain rows (MessageRow in client/GropiusChat/GropiusChat.swift), so a conversation reads as a list rather than as an exchange, and the eye has no quick cue for which side spoke. Wanted: bubble treatment that distinguishes the person from the model at a glance — shape, alignment and background, in the platform's idiom rather than an imitation of another chat app.
