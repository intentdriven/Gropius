---
schema_version: 1
id: "iss-2609081259591183"
slug: "the-control-panel-is-served-over-a-loopback-tcp-port-with-no"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
wontfix_reason: "Refuted against the tree. The claim of no Origin/Host allowlist is false: internal/gateway/control.go:116-130 refuses a non-loopback RemoteAddr, a non-loopback Host header (the DNS-rebinding guard, named as such in the comment at 110-115) and a cross-origin Origin. The genuine residual — a blind cross-origin GET, which carries no Origin header and so passes the third guard — is already recorded accurately and more narrowly as iss-11, with the mechanism, the file and two candidate fixes. This record duplicated iss-11 while overstating it, and would have left a false claim in the ledger. It was filed on an unverified reading of a research report during installer feasibility work. The only new material is two 2025 CVEs as prior art and a per-launch token as a third candidate fix, which belong as a scope note on iss-11 rather than as a record of their own."
---

The control panel is served over a loopback TCP port with no Origin/Host allowlist and no per-launch token, so any web page the operator visits can reach it and drive its mutating endpoints; DNS rebinding defeats naive origin checks. CVE-2025-49596 and CVE-2025-66414 are this exact class in shipped 2025 software, both fixed with an origin allowlist plus a session token. Found during installer feasibility research; unrelated to installation.

## Grounds

- declined: Refuted against the tree. The claim of no Origin/Host allowlist is false: internal/gateway/control.go:116-130 refuses a non-loopback RemoteAddr, a non-loopback Host header (the DNS-rebinding guard, named as such in the comment at 110-115) and a cross-origin Origin. The genuine residual — a blind cross-origin GET, which carries no Origin header and so passes the third guard — is already recorded accurately and more narrowly as iss-11, with the mechanism, the file and two candidate fixes. This record duplicated iss-11 while overstating it, and would have left a false claim in the ledger. It was filed on an unverified reading of a research report during installer feasibility work. The only new material is two 2025 CVEs as prior art and a per-launch token as a third candidate fix, which belong as a scope note on iss-11 rather than as a record of their own.
