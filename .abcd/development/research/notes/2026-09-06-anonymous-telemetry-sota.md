# Anonymous telemetry for local LLM apps — state of the art, 2026-09-06

Research question: what does the field do about anonymous, privacy-preserving
telemetry in local-first open-source desktop apps and local LLM runtimes, and
should Gropius have any? Produced by an independent research pass (evaluator
outside the loop); the maintainer's decision is the gate. Evidence tiers are
marked: [CONSENSUS], [EVIDENCE — primary], [CONTESTED], [ANECDOTE].

## Bottom line

Gropius should ship **no telemetry now**, publish a one-page "what Gropius
sends over the network" statement, and invest instead in a redacted,
user-initiated diagnostics export that users attach to GitHub issues.

Today Gropius makes two kinds of outbound connection and no others:
HuggingFace for model downloads (`internal/hub/hub.go`) and GitHub for the
`uv` binary during runtime provisioning (`internal/runtime/provision.go`). A
search of the tree for telemetry, analytics, or update-check code found
nothing.

The consensus among privacy-positioned local-LLM tools is "none" or "opt-in
only". The one design that survived open-source scrutiny at scale, Go's
transparent telemetry, needs roughly 16,000 weekly reports to be
statistically useful, a population an experimental menu-bar app does not
have. If a concrete question later arises that GitHub issues and download
counts cannot answer, the pre-vetted design is recommendation 6.

## What comparable projects do

Verified against primary sources unless marked.

| Project | Model | What is sent | Identifier | Disclosure and opt-out | Reaction |
| --- | --- | --- | --- | --- | --- |
| Ollama | No usage telemetry; hourly update check, opt-out in settings | os, arch, version, timestamp, nonce; on macOS a persistent device id (UUIDv7 in the app's SQLite store), request signed with a device key | Yes, persistent, macOS only | Privacy policy (Mar 2026) mentions "limited device and usage metadata" but not the device id; macOS docs say nothing | Users spotted Cloudflare traffic and filed issue #2567; maintainer: "this is the only outgoing call". Comments still advise monitoring traffic rather than trusting statements |
| LM Studio (proprietary) | [CONTESTED] Official page (Jun 2026): no telemetry, update checks only. A Mar 2026 network audit reports an opt-out "Send anonymous usage data" toggle | Unknown | Settings, per secondary sources; no primary source found | Flagged in privacy checklists as opt-out by default and unverifiable |
| llama.cpp | None; `/metrics` is a local Prometheus endpoint | None | Not needed; absence verified by docs search, not code audit | Treated as the clean baseline |
| Jan | Opt-in prompt at first launch; PostHog EU; daily-active and retention counts only | Random user id | Settings toggle; policy says no data until explicit allow, 12-month retention | 2026 audit found no telemetry traffic and rated it excellent |
| vLLM | Opt-out, on by default | UUID, CPU/GPU type and count, memory, model architecture, dtype, quantisation, parallelism, env flags | UUID | Docs page; env var, `DO_NOT_TRACK=1`, or a marker file; aggregates published | Server-side tool, less backlash; notable for the file-marker opt-out |
| Open WebUI | None of its own | Its Dockerfile sets three env vars to silence dependency telemetry (ChromaDB to PostHog) | None | None | A dependency ignored the flag until a later release; dependency telemetry is your problem too |
| text-generation-webui | None; "zero telemetry" is marketed | None | Maintainer on issue #2167: "100% offline"; Gradio telemetry disabled | Positive |
| GPT4All | Opt-in dialog at first launch, usage and chat sharing asked separately | None | Audit calls the dialog one of the best | Positive |
| Homebrew | Opt-out; notice printed before first event | Formula names, options, version, arch, OS; command names with values stripped; no user id, no IP field; 365-day retention, EU-hosted; Google Analytics dropped 2023 | None | `brew analytics off` or env var; declined `DO_NOT_TRACK` in 2019 | Perennial opt-in requests closed as not planned: "opting-in would skew toward a very small group" |
| VS Code | Opt-out, default all | Usage, error, crash; folders identified by hash of git remotes | Hash of a NIC address | Telemetry level setting; extensions may ignore it | Accepted as a Microsoft norm; VSCodium exists largely because of it |
| Next.js | Opt-out | Command, version, CPU count, OS, CI flag, plugins, build duration, page count | Not documented | CLI disable, env var, a debug env var shows the payload | Long-running issues asking for default-off; vendor unmoved |
| Astro | Opt-out | Command, version, CPU/OS/CI, integrations, sanitised errors | Not documented | CLI disable, env var | Complaints centre on per-developer opt-out being unworkable for regulated teams |
| Nuxt | Opt-in prompt, versioned consent that re-prompts on policy change | None | Env var, config key | Quiet |
| Gatsby | Opt-out (2019) | Machine id, hardware, cwd, command | Machine id | Honoured `DO_NOT_TRACK` early | Issue "collects personal usage data by default"; the RFC itself predicted anger |
| .NET CLI | Opt-out for Microsoft builds; source-built distros default off | Command usage | None documented | Env var | 2016 to 2018 backlash; a Linux distribution patched it off |
| Angular CLI | Opt-in prompt | Usage | None documented | Env var | Quiet |
| Deno | No usage telemetry found; daily update check, env var to disable | None | None | Quiet |
| Bun | Crash reports only, opt-out; a short URL with a 64-bit feature bitmap, "zero personal information" | None | Config key; honours `DO_NOT_TRACK` | Quiet; a good minimal crash-report design |
| GitHub CLI (Apr 2026) | Opt-out, silent | Subcommands and flags, "pseudonymous" | Pseudonymous | Env vars, config | Large HN thread within a day; covered as a controversy by trade press |
| Go toolchain | Opt-in (`go telemetry on`); default records counters locally and uploads nothing | Counters on a public allow-list only | None | Weekly upload, random skip-sampling, all aggregates published | Opt-out proposal (Feb 2023) caused uproar; opt-in accepted Apr 2023. About 1,800 weekly participants after an IDE prompt |
| Syncthing (Go desktop precedent) | Opt-in dialog with a preview of the exact report; three detail levels; daily post | Random id created on enable, deleted on disable | Public dashboard | Users still asked for id-less storage; maintainer declined |
| Console Do Not Track | Convention: `DO_NOT_TRACK=1` disables telemetry | n/a | Adopted by Gatsby, vLLM, Bun, Scarf SDKs, GitHub CLI; declined by Homebrew | Increasingly the expected floor for CLIs |

## Consent UX and regulators

- [CONSENSUS] For privacy-positioned desktop apps the accepted pattern is a
  first-launch dialog, off by default, a preview of the exact payload, a
  persistent toggle in settings, and an env var or file marker. GPT4All, Jan,
  Syncthing, Nuxt, Angular and Go all do this. No opt-in app in this survey
  suffered backlash, with Audacity as the exception (see the counter-position).
- [EVIDENCE — primary] EDPB Guidelines 2/2023 (adopted Oct 2024) hold that
  ePrivacy Article 5(3) covers information, not only personal data; that
  software on the device which proactively calls an API endpoint is "gaining
  access"; and that locally used information is out of scope only while it
  does not leave the device. Applicability does not automatically mean
  consent is required; exemptions are case by case. Practical reading:
  "anonymous" does not take app telemetry outside ePrivacy in the EU, and the
  safe basis is consent.
- [EVIDENCE — primary] The UK ICO's storage-and-access guidance (updated Apr
  2026) keeps consent as the default for device access, treats "strictly
  necessary" narrowly, and introduces a statistical-purposes exception for
  low-risk first-party analytics. Whether a desktop-app install id qualifies
  is untested.
- [EVIDENCE — primary] A random install id is pseudonymous personal data under
  GDPR (EDPB Guidelines 01/2025; ICO Mar 2025 guidance). The EDPB's draft
  Guidelines 02/2026 on anonymisation use a three-part test: no singling out,
  no linkage, no inference. With a tiny user base even id-less events fail
  "no singling out": a lone user with a rare chip, RAM and OS tuple is
  identifiable. Not legal advice; a reason to prefer consent.
- Gropius-specific: it is launched from Finder or launchd, so a shell
  `DO_NOT_TRACK` would not be inherited. Honour it anyway for the headless
  `make run` path, and add a config-key or file marker the way vLLM does.

## Minimisation and anonymisation

- [CONSENSUS] The strongest designs use no identifier at all plus a fixed
  cadence so each install contributes at most one sample per period: Go
  (weekly, no id, random skip-sampling), Homebrew (no user id, no IP field),
  KDE's Telemetry Policy (forbids unique identification), Bun (feature bitmap
  only).
- [EVIDENCE] Differential privacy (Apple's local DP, Google RAPPOR) and
  Prio/DAP via Divvi Up plus Oblivious HTTP (Firefox, 2023) all need
  populations in the hundreds of thousands and, for Prio, two non-colluding
  operators. At Gropius's scale the noise needed for meaningful epsilon would
  swamp any signal from a few hundred installs.
- [EVIDENCE] Sample size: Go's designer estimates about 16,000 weekly reports
  for 1% accuracy at 99% confidence. Go's prompt produced about 1,800 weekly
  participants and still found real bugs, but those were stack counters
  (crash fingerprints), which are useful at any n, unlike distributions.
- Retention norms: Homebrew 365 days; Jan 12 months; TelemetryDeck free tier
  90 days.

## Backends for a solo maintainer

| Backend | Cost | Self-host | Raw events or IP visible to vendor | Fit |
| --- | --- | --- | --- | --- |
| PostHog Cloud EU | 1M events/month free | Yes, but needs Postgres, ClickHouse, Redis and Kafka | Yes | Over-featured; product-analytics posture |
| Plausible | Cloud from about 9 EUR/month; community edition free | Yes | Receives IP and UA to compute a daily salted hash; nothing stored raw | Web-oriented "visitor" model |
| Umami | 100k events/month free; MIT self-host | Yes | Receives IP; salted hash rotates | Same caveat |
| Scarf | Free tier | No | Uses IP for company enrichment, then purges | Reject: resolving who-is-the-company from IP is the network-identity linkage Gropius forbids |
| Aptabase | 20k events/month free; EU or US; open source | Yes | Claims no device ids or fingerprinting | Reasonable managed option if a vendor is wanted |
| TelemetryDeck | Free tier 50k signals/month from Jul 2026, 90-day retention; EU | No | Client-side salted hash re-hashed server-side; still a per-user pseudonym | Apple-native; no Go SDK, plain HTTP works |
| Sentry | 5k errors/month free; self-host is full-featured | Yes, heavy | Yes; PII off by default; scrubbing hooks | Crash-only; Go panic strings here can embed absolute paths (the class bug-hunt round 8 fixed in the gateway), so scrubbing is mandatory |
| Cloudflare Workers Analytics Engine | Included in the Workers free tier; aggregate SQL only; adaptive sampling | n/a | Cloudflare sees the request IP transiently; nothing stored if the Worker writes buckets only | Best fit: the repo already plans a Cloudflare Worker for release-driven landing-page updates, so the marginal cost is one route |

## What is worth collecting, if anything

- Justified by precedent and by decisions a maintainer can act on: app
  version; macOS major version; chip family bucket (M1 to M5, not model or
  core count); unified-memory bucket; a small enum of failure classes
  (model-load OOM, backend exited before ready, provisioning download failed,
  venv missing, port squat); counts bucketed by order of magnitude. Homebrew,
  Go and vLLM all publish that hardware and OS split is what they use.
- Must never: prompts, completions, request bodies, model repo ids (they
  reveal interests; Jan excludes model choice, so publish at most
  architecture family and quantisation bits), hostnames, mDNS names, LAN
  addresses, bind configuration, HuggingFace tokens, absolute paths, install
  ids, fine timestamps, and any crash text that has not passed the same
  redaction as the gateway's launch-error path.
- Crash reports are singletons, not aggregates, so anonymity by aggregation
  does not apply. Treat each as a per-event consent, as macOS and Sparkle do.

## The counter-position

- [EVIDENCE — case] Audacity 2021: a strictly opt-in, disabled-by-default
  proposal was withdrawn after community revolt. The backends were Google and
  Yandex and the announcement was botched; vendor choice and communication
  matter as much as the default.
- [EVIDENCE — case] Go 2023, Fedora 2023, GitHub CLI 2026: every opt-out
  proposal in a developer-facing OSS project in this survey produced a public
  backlash; Zed's opt-out telemetry spawned a fork.
- [CONSENSUS among local-LLM users] The category's selling point is "nothing
  leaves the machine". The Ollama issue shows users treating even an update
  check as suspect, and the 2026 audit ranks tools by absence of telemetry.
  For Gropius, whose README promise and decision log are built on this,
  telemetry is brand-negative even when technically clean.
- The credible pro-telemetry case: Go found bugs users would not bother to
  report; Homebrew argues opt-in skews toward a small group. Both arguments
  concern populations of millions. [ANECDOTE] The widely repeated line that
  the vast majority opt out of Homebrew analytics traces to podcast remarks;
  no methodology was found.
- Alternatives that ship no telemetry: GitHub release-asset download counts
  (per asset and version, a free install proxy); a user-initiated, redacted
  diagnostics bundle (Tailscale's bug-report pattern); issue templates that
  ask for chip, RAM and macOS; a periodic short survey linked from release
  notes (Go and Rust both run these).

## Ranked recommendations

1. **Ship nothing; publish a network-activity statement.** One docs page
   (explanation type) listing every outbound connection Gropius makes and
   why, with the User-Agent string and a promise of no telemetry. Zero code,
   high trust, and it closes the question users will otherwise ask in an
   issue. [CONSENSUS]
2. **Add a user-initiated, redacted "Export diagnostics" bundle**: app
   version, macOS major, chip family, RAM bucket, runtime provisioning
   state, the last lines of model logs with the existing path and hostname
   redaction applied. Users attach it to GitHub issues; an issue template
   asks for it. This gets the failure-class data at n=1 with explicit
   consent per report. [CONSENSUS]
3. **Use GitHub release download counts as the install proxy.** No code;
   already available. [EVIDENCE — platform feature]
4. **Audit and pin dependency telemetry**: the `uv` binary, the mlx-lm
   server, and anything Python-side that might phone home. The HuggingFace
   hub library has its own telemetry headers; set its disable variable and
   `DO_NOT_TRACK=1` on child processes alongside the existing
   `HF_HUB_OFFLINE=1`. Verify the exact variable name against current
   huggingface_hub docs; that page was not fetched. [EVIDENCE — Open WebUI's
   dependency leak]
5. **Honour `DO_NOT_TRACK` and a config marker now, even with nothing to
   disable**, so the promise is mechanically enforced if telemetry ever lands.
   A test asserting no outbound host other than HuggingFace and GitHub would
   fit the architecture test package. [CONSENSUS]
6. **If a concrete question ever justifies telemetry, use this design and
   nothing weaker**: opt-in only via a first-run dialog with a "show what
   would be sent" preview; no identifier; coarse buckets only; at most one
   report per install per week with client-side random skipping; sent to the
   Cloudflare Worker already planned, writing only bucketed dimensions to
   Analytics Engine; aggregates published on the landing page; versioned
   consent that re-prompts when the schema changes; a toggle in the menu and
   in config.json; documented retention. Expect it to remain statistically
   weak below several thousand installs.
7. **Crash reports, if ever**: per-crash consent dialog, Bun-style minimal
   payload (version, chip bucket, panic site in Gropius code only, no message
   text), never automatic.

## Not worth adopting

- Opt-out telemetry of any kind: every OSS case here produced backlash, and
  for a privacy-positioned local-LLM app the cost is reputational and
  immediate.
- Random install ids: pseudonymous personal data; weekly cadence dedups
  without one.
- Scarf: its value proposition is resolving IPs to companies, the opposite
  of Gropius's network-identity rule.
- Hosted PostHog, Plausible or Umami: a third party holds raw events and sees
  IPs; self-hosting is unreasonable load for one maintainer.
- Differential privacy, Prio, OHTTP: correct at Firefox or Apple scale,
  meaningless noise at hundreds of installs.
- Automatic Sentry crash capture: Go panic strings from this codebase can
  carry absolute paths; automatic upload without per-event review contradicts
  the repo's privacy convention.
- Model repo ids in any payload.

## Unverified or conflicting

- LM Studio: official policy says no telemetry; two 2026 secondary sources
  describe an opt-out toggle. No primary source for the toggle was found.
- Ollama's macOS device id in the update check is verified in source but not
  disclosed on its macOS docs page.
- Homebrew "vast majority opt out": no methodology found.
- Deno and llama.cpp: "no telemetry" is established by absence in docs and
  search, not by code audit.

## Sources

Ollama issue #2567, updater and store source files, privacy policy · LM
Studio app-privacy page · local-llm.net privacy audit (Mar 2026) · Jan privacy
and privacy-policy pages · vLLM usage-stats docs · Open WebUI Dockerfile and
issue #15613 · text-generation-webui issue #2167 · Homebrew Analytics docs,
issues #142 and #18479, PR #6745 · VS Code telemetry docs · Next.js telemetry
page, issues #23183 and #59686 · Astro telemetry page, issue #11820 · Nuxt
telemetry README · Gatsby RFC 0009, issue #12922, PR #19528 · .NET CLI
telemetry docs · Angular CLI analytics page · Deno upgrade docs · bun.report
announcement · GitHub CLI changelog (2026-04-22) and trade-press coverage ·
go.dev/doc/telemetry, the Go telemetry blog post, and Russ Cox's opt-in essay ·
Syncthing usage-report source and forum thread · consoledonottrack.com · EDPB
Guidelines 2/2023, 01/2025, and draft 02/2026 · ICO pseudonymisation guidance
and storage-and-access myth-busting · Apple local differential privacy paper ·
RAPPOR (arXiv 1407.6981) · Mozilla OHTTP and Prio announcement; LWN on Divvi
Up · KDE Telemetry Policy · Sparkle system profiling docs · PostHog, Plausible,
Umami, Scarf, Aptabase, TelemetryDeck, Sentry and Cloudflare Analytics Engine
pricing and data-policy pages · Audacity coverage (TechRadar, Hackaday) ·
Fedora telemetry coverage (The Register) · Zedless fork discussion · Tailscale
bug-report docs.
