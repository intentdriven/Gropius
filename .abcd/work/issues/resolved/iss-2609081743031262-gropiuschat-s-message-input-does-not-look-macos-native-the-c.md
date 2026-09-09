---
schema_version: 1
id: "iss-2609081743031262"
slug: "gropiuschat-s-message-input-does-not-look-macos-native-the-c"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "maintainer walkthrough of the chat client on a standard account"
origin: researcher-authored
production_mode: hand-written
found_at: "client/GropiusChat/GropiusChat.swift"
resolution: "The composer is an NSTextView-backed control: bordered, growing to a ceiling then scrolling, with visible focused and disabled states, Return to send and Shift-Return for a newline."
impact: fix
---

GropiusChat's message input does not look macOS-native. The composer (client/GropiusChat/GropiusChat.swift:513) is a plain TextField with textFieldStyle(.plain) on a rounded quaternary rectangle, which reads as a generic box rather than a native control. Wanted: a composer that looks at home on the platform. Related to the bubble treatment but a separate control with its own states — focused, disabled, multi-line growth — and the disabled state matters here because the composer is disabled whenever the client is not connected.

## Grounds

- pursued: the composer reads as a native macOS control in both appearances, keeps the 'Connect to a server' hint while disabled, and never holds the keyboard when it cannot take a keystroke; shown wrong by a report that Return inserts a newline instead of sending, or that the field stops growing before its ceiling.
