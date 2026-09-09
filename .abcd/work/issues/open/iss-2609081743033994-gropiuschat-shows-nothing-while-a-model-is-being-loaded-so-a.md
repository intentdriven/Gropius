---
schema_version: 1
id: "iss-2609081743033994"
slug: "gropiuschat-shows-nothing-while-a-model-is-being-loaded-so-a"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "maintainer walkthrough of the chat client on a standard account"
origin: researcher-authored
production_mode: hand-written
found_at: "client/GropiusChat/GropiusChat.swift"
---

GropiusChat shows nothing while a model is being loaded, so a first message to a cold model looks like a hang. Loading a model into memory takes appreciable time, and the client's only states are connected and sending; there is no signal that the delay is a load rather than a slow answer. Wanted: a visible loading state for the model, distinct from ordinary generation latency, so the user knows the wait is provisioning and roughly where it is. The gateway already distinguishes a load from a completion internally, so the question is what it should tell a client and how the client should show it.
