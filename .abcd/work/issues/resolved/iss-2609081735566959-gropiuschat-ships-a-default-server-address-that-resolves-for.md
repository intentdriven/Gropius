---
schema_version: 1
id: "iss-2609081735566959"
slug: "gropiuschat-ships-a-default-server-address-that-resolves-for"
severity: "major"
category: "bug"
source: "user-observation"
found_during: "non-admin account testing of the installer authentication panel"
origin: researcher-authored
production_mode: hand-written
found_at: "client/GropiusChat/GropiusChat.swift"
resolution: "The client opens on http://localhost:11535, the empty chat and the disabled composer both name Settings and offer a button that opens it, and Settings lists the Gropius servers advertising themselves on the network so an address never has to be guessed."
impact: fix
---

GropiusChat ships a default server address that resolves for nobody, and the composer is disabled until connected, so the client is unusable on first launch with no visible way out. client/GropiusChat/GropiusChat.swift:107 sets the AppStorage default serverURL to a documentation persona's hostname; the same string at :705 is legitimate placeholder text, but at :107 it is the real stored value. The composer (:520) and send button (:536) are both disabled(!model.connected), and the empty state (:601) reads "Connect to a Gropius server to start" without naming Settings or offering a control, so a first-run user sees a dead text field and a sentence telling them to do something the window gives them no way to do. Reported from a standard account: could not connect, could not type. The client also has no discovery at all: the server advertises _gropius._tcp with a TXT record carrying api, path, auth and models (internal/discovery/discovery.go), and nothing in client/GropiusChat browses for it, so the README's "discoverable over Bonjour" describes only the server half. Two fixes wanted together: a localhost default, which is right because the documented order installs the server then the client on the same Mac, and a Bonjour browse so a client on another Mac can find servers instead of being told to type an address it cannot guess.

## Grounds

- pursued: a first launch of GropiusChat now reaches a server on the same Mac and, when it does not, says where to go and gets there in one click; a client on another Mac picks its server from a list rather than typing an address. Shown wrong if a first-run user still meets a text field they cannot type in with nothing to click, or if Settings lists nothing while a server is advertising (check with dns-sd -B _gropius._tcp).
