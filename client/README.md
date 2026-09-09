# Gropius Chat

A small native macOS app for chatting with the MLX models a
[Gropius](../README.md) server exposes on your network. Point it at the Mac
running Gropius, pick a model, and talk to it — streaming replies, no browser.

It's a plain OpenAI-compatible client, so it needs nothing installed on the other
machine: just this one app. **Requires macOS 26**, the same floor as the server.

## Build

Needs the Xcode command-line tools (`xcode-select --install`) on the Mac you
build on, with the **macOS 26 SDK**: the app's Liquid Glass button styles exist
only there, so the build fails against an older SDK. No Xcode project — one
Swift file, one script:

```sh
./build.sh
open dist/GropiusChat.app
```

`build.sh` produces a **universal** (Apple Silicon + Intel) `dist/GropiusChat.app`.

## Use

1. Launch it. On first run it tries `http://localhost:11535` — the Gropius
   server on this same Mac, which is where the documented install puts one.
2. If the server is on another Mac, open **Settings** (the **gear** in the
   toolbar, or the button on the empty chat). Settings lists every Gropius
   server it can find on your network: each row names the server, says whether
   it needs an API key, and how many models it can serve. Click one and its
   address fills the field — nothing connects until you say so, so the choice
   of which machine receives your API key stays yours.

   Nothing found, or the server is somewhere Bonjour does not reach? Type the
   address instead: the Mac's `.local` name or its LAN IP with port `11535` and
   no path — for example `http://your-mac.local:11535`. The Gropius **Connect**
   tab and menu bar show that address with `/v1` on the end (the form OpenAI
   clients want); GropiusChat adds `/v1` itself, so drop the suffix when
   pasting. Add an API key only if that server requires one.

   macOS asks for permission to search the local network the first time
   Settings opens. Without it the list stays empty, and typing the address
   still works.
3. Pick a model from the top-right menu and start typing. Enter sends; the arrow
   button too. The stop button interrupts a reply in progress.

Thinking models (Qwen3, etc.) stream their reasoning; a grey **Thoughts** row
above the answer expands to show it, so a reply that spends its whole budget
reasoning is never blank.

### Chats

The left **sidebar** holds your conversations. The **pencil** button in the
toolbar starts a new chat; the toolbar's sidebar button hides or shows the
list. Delete a chat by swiping or right-clicking it. Everything is
saved to `~/Library/Application Support/GropiusChat/conversations.json` and
restored on next launch — history lives on the machine running the client, not on
the server.

## Distributing it to another Mac

The app is **ad-hoc signed**, not signed with an Apple Developer ID. That's fine
for your own network but macOS Gatekeeper will quarantine it after it's copied or
downloaded. On the receiving Mac, either:

- **Right-click the app → Open** the first time, then confirm the dialog, or
- clear the quarantine flag from a terminal:

  ```sh
  xattr -dr com.apple.quarantine /path/to/GropiusChat.app
  ```

For friction-free distribution to Macs you don't control, you'd sign and
**notarize** the app with an Apple Developer ID — out of scope here.

The app talks plain HTTP to a LAN address; its `Info.plist` allows that
(`NSAllowsLocalNetworking`) and declares Local Network access, which macOS may
prompt the user to approve on first connect.

## What it is under the hood

- `GropiusChat/GropiusChat.swift` — the whole app (SwiftUI). `GET /v1/models` to
  list, `POST /v1/chat/completions` with `stream: true` to chat, parsed as SSE.
- `Info.plist` — bundle metadata, local-network entitlements, and the Bonjour
  service type the app may browse for (`_gropius._tcp`, the one the server
  advertises).
- `build.sh` — compiles with `swiftc` and assembles the `.app`.

Settings persist across launches: the server URL and chosen model in
`UserDefaults`, the API key in the macOS Keychain.
