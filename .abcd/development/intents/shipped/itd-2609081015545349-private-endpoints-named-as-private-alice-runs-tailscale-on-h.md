---
id: itd-2609081015545349
slug: private-endpoints-named-as-private-alice-runs-tailscale-on-h
spec_id: spc-2609081222104376
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Private endpoints marked as private: Alice sees which address on the list is the one that does not cross the café's Wi-Fi, and the list stops offering addresses her Mac never answers on

## Press Release

Alice serves models from her Mac and reaches them from her laptop. She runs a
mesh VPN across both, so a private, encrypted path between them already exists —
but Gropius has never said so. Its panel lists every address the server answers
on, and because the VPN's interface is simply another interface, its address is
quietly in that list, looking exactly like the address that carries her prompts
across whatever Wi-Fi she is sitting on. Alice picks between them by reading the
numbers.

Gropius now marks them. The address on the private network is labelled as such,
and the addresses that are the local network keep saying that they are the local
network. When there is no private network, the list looks exactly as it always
has.

The mark says what Gropius observed, not what it guarantees. It reads "private
network" and names no product, because Gropius cannot see whether that network
has since been shared, published to the internet, or logged out from — and a
label that says "encrypted" survives every one of those and then lies. What it
can honestly say is which address belongs to which network, which is the fact
Alice is missing.

The same change stops the list offering addresses the server never answers on.
Gropius lists every address the machine holds, but when Alice has told it to
listen on one specific address, the rest are dead — and a mark that promotes a
dead address to the recommended one would be worse than no mark at all.

Nothing about who may reach the server changes: the same bind, the same API key
rule, the same warnings.

## Why This Matters

The gateway serves plain HTTP with no TLS. Over the local network every prompt,
every completion and the API key itself cross the wire in the clear; over a mesh
VPN the same traffic is inside an encrypted tunnel and reachable off the local
network entirely. Those are very different propositions, and the panel presents
them as one undifferentiated list of addresses.

The cost of getting it wrong runs one way. An operator who assumes the private
path is in use, because nothing told her otherwise, hands out the café's address
and her key with it. Marking the list is the cheapest thing that removes the
guess.

The dead addresses matter for the same reason. A specific-address bind is the
one way an operator can genuinely narrow who reaches the server, and the panel
answers it by listing every address on the machine — most of which refuse the
connection. Marking one of those as the private one would turn a confusing list
into a confidently wrong one.

## Mechanism

We expect that when someone has a private path available, they will use it, and
that the only reason they do not today is that they cannot tell which address is
which. This is wrong if the marked list makes no difference — if people go on
pasting the local-network address once the mark exists, then the missing
information was not what was stopping them, and marking is not the fix.

## Scope Conditions

- A Mac running a mesh VPN installed the ordinary way, where the private <!-- cond: cond-2609081222119118 -->
  address has the shape those products normally give it. A self-hosted or
  unusually configured network may go unmarked, and unmarked is the designed
  failure: the claim is that Gropius never marks wrongly, not that it always
  marks.

## Acceptance Criteria

- Given a Mac on a mesh VPN, When Alice opens the control panel, Then the
  endpoint on the private network is marked and the local-network endpoints are
  not.
- Given that mark, When Alice reads it, Then it says the address is on a private
  network, names no vendor, and claims nothing about encryption, reachability or
  who else can connect.
- Given a Mac with no private network, When Alice opens the control panel, Then
  nothing about the endpoint list changes.
- Given Gropius is bound to one specific address, When Alice opens the control
  panel, Then only addresses the server actually answers on are listed, and the
  mark never appears on an address it does not answer on.
- Given any request arrives at the gateway, When it is handled, Then
  authentication, admission and every other enforcement path behave exactly as
  they do on a build with no private-network detection at all.
- Given the private network appears, disappears or changes address while Gropius
  is running, When Alice reloads the control panel, Then the list reflects the
  current state and Gropius keeps serving throughout — no fact about the private
  network is cached for the life of the process.

## Open Questions

- Which signals identify a private-network address, given the mark must never be
  wrong: the address range alone is a heuristic with real false positives, since
  the same range is handed out by some ISPs. The conjunction of that range and a
  tunnel interface is the candidate the spec should settle.
- Whether the dead-address fix belongs in the same commit as the mark or lands
  first as its own. It is the larger half and it is a fix, not an addition.
- Whether `iss-7` (a specific-address bind leaves the control panel unreachable)
  must be resolved before criterion 4 can be satisfied, or whether criterion 4
  can be met without touching what iss-7 defers.

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-8d69ca307ea9 -->
Fidelity review — receipt rcp-8d69ca307ea9 (verifier intent-auditor claude-opus-5[1m]).

Provenance: intent-auditor@claude-opus-5[1m] · rubric_hash sha256:7c98d21c13c4bb85d4f2a210e74c7c63a89eaed580266e11bb8a3f8a177ccde0 · prompt_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5
Input attestations: diff:76d1d30^1..76d1d30 (PR #27, feat/private-network-endpoints, merge 76d1d30bf5da34c801b0fc7679ebef03ed5fa7d3 onto de6d13cd745e2f051f470e5ba74d625804af7f2e)@sha256:2d51ddbb99c313c3542d9c8b5a30410eea6fe98f23474d4dfe09cbbaabd41df1; worktree:HEAD of .claude/worktrees/posture — criteria re-checked against the current tree, where the spec closed in 8fad0ba and later intents have moved this code@-;

Acceptance rollup: MET 3 · MET_WITH_CONCERNS 3 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: the classifier marks a tunnel address in 100.64.0.0/10 and nothing else, and a table test over an injected interface list asserts the mesh address is marked while the Wi-Fi, .local and loopback rows are not
  evidence: internal/netshape/netshape.go:166 — "func networkOf(ifaceName string, ip net.IP) string {"
  evidence: internal/gateway/endpoints_test.go:72 — "func TestPrivateEndpointIsMarkedAndLANEndpointsAreNot(t *testing.T) {"
  evidence: internal/gateway/endpoints_test.go:106 — "func TestNoMarkOnHalfASignal(t *testing.T) {"
  evidence: internal/ui/static/app.js:421 — "const mark = ep.network ? `<span class="pill">${escapeHtml(ep.network)}</span>` : '';"
- ac-2 — MET: the mark's whole vocabulary is one constant, "private network", and three independent scans hold the server string, the panel's own strings and the prose surfaces to it — vendor names and the words encrypt/secure/safe/only/vpn are failures, not review notes
  evidence: internal/netshape/netshape.go:30 — "const PrivateNetwork = "private network""
  evidence: internal/gateway/endpoints_test.go:141 — "func TestTheMarkNamesNoVendorAndPromisesNothing(t *testing.T) {"
  evidence: internal/ui/endpoints_test.go:96 — "func TestTheConnectPageNamesNoVendorAndPromisesNothing(t *testing.T) {"
  evidence: internal/archtest/honest_marking_test.go:224 — "func TestTheDocumentationAndThePanelClaimNothingAboutAPrivateNetwork(t *testing.T) {"
  evidence: internal/netshape/honesty_test.go:27 — "func TestNoStringInThisPackageNamesAVendorOrPromisesAnything(t *testing.T) {"
- ac-3 — MET_WITH_CONCERNS: a test asserts the whole list byte-for-byte against an interface list with no tunnel, so no mark and no reordering reach a Mac with no private network; the named concern is that the list is not literally unchanged for such a Mac — the enumeration moved from net.InterfaceAddrs() to per-interface inspection that skips interfaces that are down, and the .local name is now conditional on the machine holding a local-network address, so an address on a down interface that the old build offered is now dropped
  evidence: internal/gateway/endpoints_test.go:172 — "func TestWithNoPrivateNetworkTheListIsUnchanged(t *testing.T) {"
  evidence: internal/netshape/netshape.go:126 — "if iface.Flags&net.FlagUp == 0 {"
  evidence: internal/gateway/control.go:952 — "func hasLocalNetworkAddr(addrs []netshape.Addr) bool {"
- ac-4 — MET_WITH_CONCERNS: a specific bind now lists that address alone (dropped entirely once the Mac stops holding it) and the mark is read only for the bound address, so it can never land outside the bind; the concern is a divergence the delivery signed off in its own test — as merged, loopback was still listed under a specific non-loopback bind, an address that build did not answer on, held in place by iss-7 and left unmarked so it was a dead address and never a marked one. At HEAD that is closed: every bind acquires loopback, and the test was rewritten to say so
  evidence: internal/gateway/control.go:838 — "func Endpoints(cfg config.Config, plan bind.Plan) []Endpoint {"
  evidence: internal/gateway/control.go:897 — "func stillHeld(addrs []netshape.Addr, bound string) bool {"
  evidence: internal/gateway/endpoints_test.go:198 — "func TestASpecificBindListsOnlyWhatItAnswersOn(t *testing.T) {"
  evidence: internal/gateway/endpoints_test.go:406 — "func TestLoopbackIsListedUnderEveryBindBecauseEveryBindAnswersOnIt(t *testing.T) {"
- ac-5 — MET_WITH_CONCERNS: no enforcement package depends on the classifier — a go list -deps rule over a hand-maintained enforcement path enforces it, every module package is forced onto one side of the rule, and withAuth and the rest of gateway.go are untouched by the delivered diff (only control.go and tests moved); three concerns are named rather than waved past: the same PR did change enforcement code for reasons unrelated to the detection (ExposedToLAN rewritten to read bracketed IPv6 and case-folded localhost, Validate newly refusing bind hosts it used to accept), which the spec had put out of scope; the guarantee is review-carried by the ADR's and the test file's own admission, since any code can re-derive the classification from net.Interfaces(); and at HEAD a later intent's private-network bind mode lets the resolver read the classifier under the ADR's 2026-09-08 carve-out, so the closure is no longer total
  evidence: internal/archtest/enforcement_detection_test.go:171 — "func TestTheEnforcementPathCannotSeeThePrivateNetworkDetection(t *testing.T) {"
  evidence: internal/archtest/enforcement_detection_test.go:192 — "func TestEveryPackageIsOnOneSideOfTheRule(t *testing.T) {"
  evidence: internal/gateway/gateway.go:161 — "func (g *Gateway) withAuth(next http.Handler) http.Handler {"
  evidence: internal/config/config.go:1630 — "func (c Config) ExposedToLAN() bool {"
  evidence: internal/archtest/enforcement_detection_test.go:117 — "resolverPkg = "github.com/intentdriven/Gropius/internal/bind/private""
- ac-6 — MET: the classifier reads the interfaces on every call and holds nothing between them — no package-level cache exists — and two tests change the injected list between calls and assert the second answer reflects a tunnel that came up and then went away
  evidence: internal/netshape/netshape.go:111 — "func Addrs() []Addr {"
  evidence: internal/gateway/endpoints_test.go:347 — "func TestEndpointsReflectTheCurrentInterfaceList(t *testing.T) {"
  evidence: internal/netshape/netshape_test.go:167 — "func TestAddrsReflectsTheCurrentInterfaceList(t *testing.T) {"
  evidence: internal/gateway/control.go:385 — "Endpoints: Endpoints(cfg, c.App.Bind()),"

Gap audit:
- honoured:
  - Gropius now marks them: the address on the private network is labelled as such
    evidence: internal/netshape/netshape.go:174 — "return PrivateNetwork"
    evidence: internal/gateway/control.go:734 — "type Endpoint struct {"
  - the mark says what Gropius observed, not what it guarantees: it reads "private network" and names no product
    evidence: internal/netshape/netshape.go:30 — "const PrivateNetwork = "private network""
    evidence: internal/archtest/honest_marking_test.go:224 — "func TestTheDocumentationAndThePanelClaimNothingAboutAPrivateNetwork(t *testing.T) {"
  - the same change stops the list offering addresses the server never answers on
    evidence: internal/gateway/control.go:820 — "// Endpoints lists the base URLs clients can point at."
    evidence: internal/gateway/endpoints_test.go:455 — "func TestAnAcquiredAddressThatWentAwayIsNoLongerOffered(t *testing.T) {"
  - the mark is presentation and not behaviour — the menu bar still hands out the first entry, and what is copied is the URL alone
    evidence: cmd/gropius/menubar.go:60 — "endpoint.SetTitle(eps[0].URL)"
    evidence: internal/ui/endpoints_test.go:68 — "func TestTheClipboardAndTheExamplesTakeTheURLAlone(t *testing.T) {"
  - the meaning of the mark is written where an operator reads it
    evidence: docs/getting-started.md:108 — "An address in that list that sits on a private network carries a mark saying so"
- diverged:
  - when there is no private network, the list looks exactly as it always has
    evidence: internal/netshape/netshape.go:126 — "if iface.Flags&net.FlagUp == 0 {"
    evidence: internal/gateway/control.go:952 — "func hasLocalNetworkAddr(addrs []netshape.Addr) bool {"
  - nothing about who may reach the server changes: the same bind, the same API key rule, the same warnings
    evidence: internal/config/config.go:1630 — "func (c Config) ExposedToLAN() bool {"
    evidence: cmd/gropius/main.go:258 — "func loadStartupConfig(path string) startupConfig {"
  - the list stops offering addresses the server never answers on — as merged, loopback was still offered under a specific non-loopback bind
    evidence: internal/gateway/endpoints_test.go:398 — "loopback was listed under a bind that refused it — as a known divergence"
- missing:
  - the addresses that are the local network keep saying that they are the local network — no positive local-network label was delivered; a LAN row simply carries no mark
    evidence: internal/netshape/netshape.go:102 — "type Addr struct {"
    evidence: internal/ui/static/app.js:421 — "const mark = ep.network ? `<span class="pill">${escapeHtml(ep.network)}</span>` : '';"
  - what the mark means, and how to serve over a mesh VPN, in docs/ — filed as a deferred issue in this delivery rather than written in it; docs/mesh-vpn.md arrives only later, at 8a9753b
    evidence: .abcd/work/issues/resolved/iss-2609081221484689-docs-do-not-say-how-to-serve-gropius-over-a-mesh-vpn-or-what.md:18 — "Docs do not say how to serve Gropius over a mesh VPN, or what marking an endpoint means."

Scope-condition dispositions:
- cond-2609081222119118 — survived: the classifier is exactly the conjunction the condition assumed — a utun interface carrying a 100.64.0.0/10 address, the shape an ordinarily installed mesh VPN gives it — and a half-signal in either direction is left unmarked, so an unusual or self-hosted network goes unmarked, which is the designed failure the condition names
  evidence: internal/netshape/netshape.go:166 — "func networkOf(ifaceName string, ip net.IP) string {"
  evidence: internal/netshape/netshape.go:180 — "return strings.HasPrefix(name, "utun")"
  evidence: internal/gateway/endpoints_test.go:106 — "func TestNoMarkOnHalfASignal(t *testing.T) {"
  evidence: internal/netshape/netshape_test.go:42 — "func TestPrivateNetworkNeedsBothTheRangeAndATunnel(t *testing.T) {"
## Grounds

- pursued: the footgun is already live. The private-network address is in the endpoint list today with nothing said about it, so an operator running a mesh VPN is already choosing between addresses on no information, and the cheapest wrong choice hands out the cafe's address and the API key with it. This is closing a hole that shipped rather than adding a feature. Shown wrong if nobody is in fact serving Gropius across a mesh VPN, in which case the mark is decoration nobody reads; shown wrong a second way if people keep pasting the local-network address once the mark exists, which would mean the missing information was never what stopped them.
