---
id: itd-2609091412177263
slug: an-api-client-is-told-what-gropius-cannot-do-never-why-the-w
spec_id: spc-2609091703459471
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2609081015545349]
severity: minor
impact: additive
origin: researcher-authored
production_mode: dictated-and-formatted
---

# An API client is told what Gropius cannot do, never why; the why goes to the server's own log. When Bob's client asks for a model Gropius cannot load or does not have, the answer says so plainly: 'cannot load model X', 'model X not available'. The reason stays on the server, in a log that lives in the account running Gropius and is never shared with another account. The log is sparse by default and Alice can switch it to detailed when she is diagnosing something. A client on this Mac, or one holding the API key, keeps today's informative refusal.

## Press Release

Bob's coding harness asks for a model this Mac cannot load. The answer is
plain: "cannot load model X". He asks for one that is not here: "model X not
available". Nothing in the answer says why, and nothing describes the machine.
Alice, who runs the server, opens its log and reads the reason: the model
needed more memory than the budget allows, or the download never finished. The
log is hers alone, kept in her own account, unreadable to anyone else who uses
the Mac. By default it is sparse, a line per event that mattered; when she is
chasing a problem she switches it to detailed and it says everything Gropius
knew, still never a prompt or an answer. A client on this Mac, or one holding
the API key, keeps the fuller refusal it gets today.

## Why This Matters

Today Gropius logs to standard error only, which a Finder-launched app
discards, so the reason behind a refusal a client saw is nowhere an operator
can read it; and a refusal to a client on the network can describe this Mac
(its budget in bytes, its in-flight count). The what belongs to the client, the
why to the operator, and the two must not be the same sentence. Builds on the
entitlement rule the models list and the pool refusals already share, and on
server state living in the serving account's own directory.

## Mechanism

We expect an app log file in the account's own directory, written at a sparse
level by default and switchable to detailed without a restart, to let Alice
find the why for any refusal a client saw, because every refusal already passes
through one place in the gateway. Detailed never records prompts or answers;
that stays with the separate per-model debug draft (itd-2609062346072707).
Shown wrong if the sparse level leaves out the line that explains a refusal, or
the detailed level fills the disk.

## Scope Conditions

- macOS; one log per serving account under its own directory, 0600, never under the shared root. <!-- cond: cond-2609091703454678 -->
- Sparse by default. <!-- cond: cond-2609091703454790 -->
- The model servers' own logs are unchanged. <!-- cond: cond-2609091703455271 -->
- Prompts, answers, keys and tokens are never logged at any level. <!-- cond: cond-2609091703457603 -->
- The log is size-rotated like the statistics store. <!-- cond: cond-2609091703452047 -->

## Acceptance Criteria

- Given an unentitled client, when Gropius refuses a request, then the answer says what (cannot load, not available, cannot serve now) and never why, and one sparse log line names the model and which refusal.
- Given an entitled client, when Gropius refuses, then the answer is today's informative one.
- Given the server runs, when it writes its log, then the file is in the account's own directory at 0600 and contains no prompt, answer, key or client address.
- Given the level is switched in config.json or the panel, when the next event happens, then it is logged at the new level without a restart, and a save never refuses an untouched field.
- Given detailed, when a refusal or a load happens, then the line carries the figures (in-flight, budget, wait, launch error) that sparse omits.
- Given the log grows, when it passes its size, then it rotates and old files are pruned.
- Given the docs, when a reader opens the logging page, then it says what each level writes and where the file is.

## Open Questions

- Resolved 2026-09-09 at interview: entitled clients keep the informative refusal (today's decision on iss-2609062210238684 stands); the per-model debug draft itd-2609062346072707 stays a separate intent because a shared level control would put the level within the runtime's reach and fail the statistics-switch guard.

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-1f33f9504a0e -->
Fidelity review — receipt rcp-1f33f9504a0e (verifier intent-auditor claude-opus-5[1m]).

Provenance: intent-auditor@claude-opus-5[1m] · rubric_hash sha256:497613f9e065fa6511d7b3081b6b03076bf53a8aee432ecf812c6777d995a304 · prompt_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5
Input attestations: diff:3b34d66^1..3b34d66 (merge of feat/server-log-with-levels; spec closed in 6fe5914), verified against the worktree at HEAD dd6463d@sha256:55ffe17c8a51701dea573afd4dd8bd6eb437d34568387236269a62137f4101eb;

Acceptance rollup: MET 5 · MET_WITH_CONCERNS 2 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET_WITH_CONCERNS: The pool-refusal path is delivered whole — the unentitled client gets only genericRefusal and one Info line names the model and the refusal class — but the 404 refusal the criterion itself names ('not available') writes no log line at any level, so one of the three refusals the criterion enumerates leaves the operator nothing to read
  evidence: internal/gateway/gateway.go:676 — "g.log.Info("refused a request, and told the client only that it could not be served", "model", model, "class", class, "client", "unentitled")"
  evidence: internal/gateway/gateway.go:681 — "writeError(w, http.StatusServiceUnavailable, genericRefusal)"
  evidence: internal/gateway/gateway.go:1319 — "const genericRefusal = "cannot serve this model right now""
  evidence: internal/gateway/gateway.go:589 — "writeError(w, http.StatusNotFound, notServedText(err, g.entitled(r))) — no g.log call on this branch; the only g.log calls in the package are at gateway.go:637,638,676,678,719,777,787,807"
  evidence: internal/gateway/loglevel_test.go:175 — "func TestTheLogFileAfterAnUnentitledRefusalNamesTheModelAndWhichRefusal"
- ac-2 — MET: An entitled client (keyed-admitted or from this Mac) is still answered with the pool's own err.Error(), and the passing test asserts the informative text for that client while the unentitled one gets the generic sentence
  evidence: internal/gateway/gateway.go:647 — "if g.entitled(r) { writeError(w, http.StatusServiceUnavailable, err.Error()); return }"
  evidence: internal/gateway/gateway.go:298 — "func (g *Gateway) entitled(r *http.Request) bool { return g.admittedKeyed(r) || fromThisMachine(r) }"
  evidence: internal/gateway/refusal_text_test.go:219 — "func TestAPoolRefusalStaysInformativeForAnEntitledClient"
  evidence: internal/gateway/refusal_text_test.go:474 — "func TestANotReadyModelIsStillNamedForAnEntitledClient"
- ac-3 — MET: The file is opened 0600 through an os.Root on config.Paths.Logs — which hangs off the account directory, not the shared root — and a refusal driven end to end through the real gateway into a real file is asserted to carry no prompt marker, bearer token, HF token or client address at either level
  evidence: internal/applog/applog.go:180 — "const logPerm os.FileMode = 0o600"
  evidence: internal/applog/applog.go:229 — "f, err := r.root.OpenFile(r.name, fileFlags, r.perm)"
  evidence: internal/config/config.go:212 — "Logs: filepath.Join(acct, "logs")"
  evidence: internal/applog/applog_test.go:36 — "func TestTheLogFileIsOwnerOnlyAndRegular"
  evidence: cmd/gropius/logfile_test.go:106 — "func TestTheLogLivesInTheAccountsOwnLogDirectory"
  evidence: internal/gateway/loglevel_test.go:240 — "func assertNothingPrivateInTheLog(t *testing.T, got string, req *http.Request) — called from the Info and the Debug test alike; the prompt marker is really in the posted body and the key really in the Authorization header (loglevel_test.go:136-143)"
- ac-4 — MET_WITH_CONCERNS: A save from the panel moves the shared slog.LevelVar on the next line with no restart, and a body omitting log_level provably keeps the level in force; but the 'switched in config.json' half is not live — config.Load runs only at start-up (cmd/gropius/main.go:259, reached once from main) and nothing watches the file, so a hand edit of config.json takes effect at the next start, not at the next event
  evidence: internal/app/app.go:484 — "func (a *App) applyLogLevel(c config.Config)"
  evidence: internal/app/app.go:461 — "a.applyLogLevel(c) — called from SetConfig as well as from New (app.go:285)"
  evidence: internal/gateway/control_loglevel_test.go:30 — "func TestASaveThatNamesNoLogLevelKeepsTheOneInForce"
  evidence: internal/gateway/control_loglevel_test.go:49 — "func TestSavingALogLevelTakesEffectWithoutARestart — asserts out.Restart is false"
  evidence: cmd/gropius/main.go:259 — "cfg, notices, err := config.Load(path) — the only config.Load in cmd/ or internal/; no fsnotify or reload path exists"
- ac-5 — MET: Each sparse line has a Debug twin carrying exactly the figures the criterion names — in-flight count and budget via the wrapped pool error, the queue depth/limit/waited in the pool, the load duration in the observer, and the wrapped launch error in the gateway — and tests assert absence at Info and presence at Debug
  evidence: internal/gateway/gateway.go:678 — "g.log.Debug("refused a request...", "model", model, "class", class, "client", "unentitled", "err", err)"
  evidence: internal/gateway/gateway.go:638 — "g.log.Debug("model launch failed", "model", model, "err", err)"
  evidence: internal/runtime/pool.go:811 — "p.opts.Log.Debug("refused a model load: no model in memory could be freed", "model", repoID, "protected", noRoom.Protected, "limit", HumanBytes(noRoom.Limit), "waited", noRoom.Waited)"
  evidence: internal/app/app.go:836 — "o.log.Debug("model loaded", "model", repoID, "took", took)"
  evidence: internal/gateway/loglevel_test.go:221 — "func TestTheInFlightCountIsDetailedOnly"
  evidence: internal/runtime/loglevel_test.go:73 — "func TestADetailedRefusalCarriesTheFigures"
- ac-6 — MET: The rotator shifts gropius.log to gropius.1.log and removes what falls past Keep when the next line would pass RotateBytes, and the test writes 400 lines past a 2 KB cap and asserts the file count, the total bytes, the surviving previous file and 0600 on every kept file
  evidence: internal/applog/applog.go:259 — "if r.n > 0 && r.n+int64(len(b)) > r.max { if err := r.rotate(); err != nil {"
  evidence: internal/applog/applog.go:294 — "if err := r.root.Remove(to); err != nil && !errors.Is(err, os.ErrNotExist) {"
  evidence: internal/applog/applog_test.go:198 — "func TestTheLogRotatesAtItsSizeAndPrunesOldFiles"
  evidence: internal/applog/applog_test.go:250 — "func TestRotationIsSafeUnderConcurrentWriters"
- ac-7 — MET: docs/logging.md is a reference page carrying a per-level table, the file's location and mode, and the rotation figures, and an archtest holds the printed figures and level names against applog's and config's constants and asserts the page is linked from README and getting-started
  evidence: docs/logging.md:28 — "| Level | What it writes |"
  evidence: docs/logging.md:8 — "In the `logs` folder of your own Gropius data folder, as `gropius.log`"
  evidence: docs/logging.md:74 — "`gropius.log` grows to 5 MB and is then rolled over"
  evidence: internal/archtest/logging_docs_test.go:17 — "func TestTheLoggingPagePrintsTheFiguresTheCodeUses"
  evidence: internal/archtest/logging_docs_test.go:100 — "func TestTheLoggingPageIsLinked"

Gap audit:
- honoured:
  - The why goes to the server's own log: a file in the account running Gropius, never shared with another account
    evidence: internal/applog/applog.go:180 — "const logPerm os.FileMode = 0o600"
    evidence: internal/config/config.go:212 — "Logs: filepath.Join(acct, "logs")"
  - Sparse by default, switchable to detailed without a restart, from the panel
    evidence: internal/config/config.go:1221 — "func (c Config) EffectiveLogLevel() string { if c.LogLevel == "" { return LogLevelSparse } }"
    evidence: internal/ui/static/index.html:402 — "< legend>Server log< /legend>"
    evidence: internal/app/app.go:484 — "func (a *App) applyLogLevel(c config.Config)"
  - Detailed says everything Gropius knew and still never a prompt or an answer
    evidence: internal/gateway/loglevel_test.go:202 — "func TestTheDetailedLogFileCarriesTheRefusalsFigures — then calls assertNothingPrivateInTheLog"
  - A client on this Mac, or one holding the API key, keeps the fuller refusal it gets today
    evidence: internal/gateway/gateway.go:647 — "if g.entitled(r) { writeError(w, http.StatusServiceUnavailable, err.Error())"
  - A Finder-launched app is no longer mute: the reason behind a refusal lands somewhere an operator can read it
    evidence: cmd/gropius/main.go:99 — "appLog, logErr := applog.Open(applog.Options{Dir: paths.Logs})"
    evidence: cmd/gropius/main.go:109 — "log.Info("gropius starting", "version", version, "log", appLog.Path)"
  - The model servers' own log level is untouched; log_level never reaches internal/runtime's launcher
    evidence: internal/archtest/statistics_switch_test.go:44 — "func TestTheStatisticsSwitchDoesNotReachTheRuntime — unchanged by 3b34d66^1..3b34d66"
    evidence: internal/runtime/pool.go:803 — "the only runtime change is a log call beside an existing one; no LogLevel or log_level identifier exists anywhere in internal/runtime"
- diverged:
  - The press release's plain answers 'cannot load model X' and 'model X not available' — delivered as 'the model could not be started' and 'model %q is not available', and the launch-failure text is generic for every client, entitled or not, rather than only for the network
    evidence: internal/gateway/gateway.go:639 — "writeError(w, http.StatusServiceUnavailable, "the model could not be started") — written before the entitled branch at gateway.go:647"
    evidence: internal/gateway/gateway.go:1350 — "return fmt.Sprintf("model %q is not available", notServed.requested)"
    evidence: internal/gateway/refusal_text_test.go:263 — "func TestALaunchFailureStaysGenericForEveryClient"
  - 'Size-rotated like the statistics store' — delivered as a second, deliberately different writer in internal/applog (numbered names, one byte bound, no months horizon, no summary fold), with the duplication filed rather than extracted
    evidence: internal/applog/applog.go:144 — "internal/stats/store.go rotates by size too, and this is deliberately not that writer — see iss-2609091714393599"
    evidence: .abcd/work/issues/open/iss-2609091714393599-two-size-rotating-file-writers-now-exist-internal-stats-stor.md:1 — "two size-rotating file writers now exist"
- missing:
  - 'one sparse log line names the model and which refusal' for the not-available/not-ready refusal: the 404 branch answers the client and writes nothing to the log at any level
    evidence: internal/gateway/gateway.go:589 — "writeError(w, http.StatusNotFound, notServedText(err, g.entitled(r))) — no log call on this path"
  - 'Given the level is switched in config.json ... then it is logged at the new level without a restart' for a hand edit: config.json is read once at start-up and nothing re-reads or watches it
    evidence: cmd/gropius/main.go:259 — "cfg, notices, err := config.Load(path)"
    evidence: cmd/gropius/main.go:138 — "appLog.Level.Set(cfg.SlogLevel()) — the start-up application of the stored level; no later re-read exists"

Scope-condition dispositions:
- cond-2609091703454678 — survived: The log is opened under config.Paths.Logs, which is joined to the account directory rather than the shared root, at mode 0600, and both halves are asserted by tests
  evidence: internal/config/config.go:212 — "Logs: filepath.Join(acct, "logs")"
  evidence: internal/applog/applog.go:180 — "const logPerm os.FileMode = 0o600"
  evidence: cmd/gropius/logfile_test.go:106 — "func TestTheLogLivesInTheAccountsOwnLogDirectory"
  evidence: internal/applog/applog_test.go:52 — "if perm := fi.Mode().Perm(); perm != 0o600 {"
- cond-2609091703454790 — survived: Empty log_level resolves to sparse in one place, the field is omitempty so nothing is written for the default, and applog's zero Level is slog.LevelInfo — the fresh-install level a test pins to config.Default().SlogLevel()
  evidence: internal/config/config.go:1221 — "func (c Config) EffectiveLogLevel() string { if c.LogLevel == "" { return LogLevelSparse } }"
  evidence: internal/config/config.go:623 — "LogLevel string `json:"log_level,omitempty"`"
  evidence: cmd/gropius/logfile_test.go:86 — "func TestTheLogStartsAtTheLevelTheSettingsName"
  evidence: internal/config/loglevel_test.go:27 — "func TestAFreshInstallLogsSparsely"
- cond-2609091703455271 — survived: No model-server log path is touched: internal/runtime/launcher.go is not in the delivered diff, no LogLevel/log_level identifier exists anywhere in internal/runtime, and the statistics-switch guard that keeps the level out of the runtime is unchanged by the range
  evidence: internal/archtest/statistics_switch_test.go:44 — "func TestTheStatisticsSwitchDoesNotReachTheRuntime — git diff 3b34d66^1 3b34d66 lists no change to this file or to internal/runtime/launcher.go"
  evidence: docs/logging.md:64 — "The model servers keep their own logs, one per model, in the same folder. They are always run at their INFO level."
  evidence: internal/archtest/logging_docs_test.go:61 — "func TestTheLoggingPageStatesWhatIsNeverWritten"
- cond-2609091703457603 — survived: Every added line takes a registry-resolved repo id, a closed-set class name or a pool figure, and a real refusal driven through the gateway at Info and at Debug alike is asserted to hold no prompt marker, no bearer token, no HF token and no form of the client address
  evidence: internal/gateway/loglevel_test.go:240 — "func assertNothingPrivateInTheLog(t *testing.T, got string, req *http.Request)"
  evidence: internal/gateway/loglevel_test.go:137 — "req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"org/warm","messages":[{"role":"user","content":"`+promptMarker+`"}]}`))"
  evidence: cmd/gropius/logfile_test.go:26 — "func TestTheLogFileAfterARefusalNamesNoClientNoPromptAndNoSecret"
  evidence: internal/app/app.go:470 — "a.Log.Info("settings changed", "log_level", c.EffectiveLogLevel()) — only the level is named, never the key or the token"
- cond-2609091703452047 — narrowed: The log is size-rotated and pruned, but not by the statistics store's mechanism: a second, smaller writer was written into internal/applog and the duplication was filed rather than resolved
  narrowing: Holds as 'size-rotated with a bounded file count' only — 5 MB a file and five files kept, by internal/applog's own numbered-name rotator. It does not hold as 'like the statistics store' in any stronger sense: no code is shared, the names are numbered rather than UTC-dated, there is no months retention horizon and no summary fold of what is dropped, and the two are now two writers to keep in step (iss-2609091714393599, DECISIONS.md 2026-09-09)
  evidence: internal/applog/applog.go:144 — "internal/stats/store.go rotates by size too, and this is deliberately not that writer — see iss-2609091714393599 and the spec"
  evidence: internal/applog/applog.go:51 — "DefaultRotateBytes = 5 << 20"
  evidence: internal/applog/applog_test.go:198 — "func TestTheLogRotatesAtItsSizeAndPrunesOldFiles"
  evidence: .abcd/work/DECISIONS.md:181 — "The server's own log (spc-2609091703459471) gets a second size-rotating file writer, in internal/applog, rather than an extraction from internal/stats/store.go."
## Grounds

- pursued: we expect a client's plain refusal plus a server-side reason to end the guessing when a model will not load, without telling the network anything about this Mac; shown wrong if operators keep asking what a refusal meant because the sparse line did not say, or if they turn detailed on and leave it on.
