---
id: adr-2609111126115848
slug: a-deliberately-invoked-diagnostic-may-report-an-observed-sig
status: accepted
date: 2026-09-11
supersedes: adr-2609081118587999
superseded_by: null
related_intents: [itd-2609081259532589]
related_rfcs: []
related_adrs: [adr-2609081118587999, adr-2609061503319212]
---

# ADR-2609111126115848: A deliberately invoked diagnostic may report an observed signal it cannot verify, and never a verdict

**Supersedes:** [adr-2609081118587999](2609081118587999-detecting-a-private-network-daemon-may-inform-what-gropius-s.md),
on the diagnostic carve-out only.

## Context

itd-2609081259532589 promises a `gropius doctor` verb, and the failure its
press release opens on is the one people actually hit: a machine across the
room gets an empty response. On an ad-hoc-signed build the cause is almost
always one of two grants — the macOS Application Firewall entry, or the Local
Network Privacy grant — and both are state Gropius does not own.

Three facts about that state were measured rather than assumed
(`.abcd/development/research/notes/2026-09-08-native-installer-feasibility.md`):

- The firewall query answers "permitted" for a path that has no entry at all,
  and for a path that does not exist. A check built on it would confirm a
  healthy grant at exactly the moment an update invalidated one.
- An ad-hoc signature's designated requirement *is* its code-directory hash,
  and that hash changes on every build, so both grants are lost on every
  update. Whatever was observed may already be about a bundle that is gone.
- Local Network Privacy has no query interface of any kind, is not part of
  TCC, and cannot be reset or pre-seeded.

adr-2609081118587999 governs signals of exactly this class. Its rule 1 lets
presentation state only what was observed; its rule 2 closes every enforcement
decision **and every warning's firing condition** to them; and its Consequences
name "a firewall's state" among the signals that inherit the rule. So the
question is not whether that record has an opinion about doctor. It has one,
and shipping doctor without settling it would be routing around a ratified
record rather than amending it.

The tension is narrower than it first looks, and it is real on both sides.

**Why doctor is not a warning.** A warning fires on the app's own initiative,
on a surface the operator did not ask for, beside a control whose behaviour it
softens or hardens. That is why rule 2 closes its firing condition: the
operator reads a softened warning as a statement about their exposure, and the
inference behind it goes away — a logout, an expiry, a rebuild — without
anything in Gropius being told. A diagnostic is the opposite arrangement in
every one of those respects. Alice types it, at the moment she is already
debugging, and what comes back changes no behaviour, admits no request, and
gates nothing. It is a report of what was seen.

**Why the tension is nevertheless real.** A doctor line that carries a severity
in machine-readable output is warning-shaped, and the intent's own criteria ask
for severities there. "Observed" is a label a future line can acquire while its
sentence quietly hardens into a conclusion, which is how the inference gets back
in. And the reasoning the installer uses cannot be borrowed here: the
authorisation panel avoids the tension by never reading the state at all, which
a diagnostic cannot do and remain a diagnostic.

The record already has a precedent for this shape. Its own amendment of
2026-09-08 met a case rule 2 had not had in view — resolving a bind address for
a mode the operator explicitly chose — by amending narrowly, stating the
conditions, arming them, and saying plainly what the exception cost. The
maintainer settles this one the same way.

## Decision

We supersede adr-2609081118587999 on one point only: **rule 2's closure of "a
warning's firing condition" does not reach a diagnostic a person invokes
deliberately.** Every other rule in that record, and every other decision in
it, stands unchanged and is read from there.

**The carve-out.** A diagnostic verb that a person invokes — `gropius doctor`
is the case in hand — may report an observed signal about state Gropius does
not own, including the firewall entry and the Local Network Privacy grant,
under three conditions. All three hold together; a report that drops one is not
covered by this record.

**Condition 1 — the signal is labelled observed, and the report issues no
verdict.** The line says what the query returned and that it could not be
verified, in both directions: it may not say the grant is present, and it may
not say the grant is missing. "The firewall lists an entry for this path; that
does not establish the grant still covers this build" is admissible. "The
firewall grant is in place", "your firewall is blocking this" and any wording a
reader would act on as settled fact are not. Where nothing can be queried at
all, as with Local Network Privacy, the report says the state cannot be
determined from here rather than omitting the check or guessing it.

A severity in machine-readable output is permitted, and it carries the same
constraint as the prose: for a signal reported under this carve-out, the value
says the state could not be determined, never that a fault was found. The
severity of a check Gropius genuinely verified — the runtime, the root, the
configuration, the port's holder — is unaffected by this record.

**Condition 2 — the commands that would settle it are always named.** Every
line reported under this carve-out prints, beside it, what a person would run
to establish or to restore the state. This is not a courtesy. An observation a
reader cannot act on collapses into a verdict in the reading, because the only
thing left to do with it is believe it; naming the remedy is what keeps the
line a report.

**Condition 3 — it gates nothing, and that is armed rather than asserted.**
Two tests carry the condition, and they are a prerequisite of the carve-out
rather than a follow-up to it:

- A **dependency-closure test**: no package on the enforcement path imports the
  diagnostic package. The shape already exists in
  `internal/archtest/enforcement_detection_test.go` — `go list -deps` over each
  enforcement package, plus a companion list that puts every package in the
  module on one side of the rule or the other, so a package added later fails
  loudly instead of being silently unjudged.
- A **wording test**: no line reporting a signal under this carve-out is
  phrased as a conclusion. It is a scan over the diagnostic's own report
  strings, and it is the one that has to exist, because condition 1 is a
  property of sentences and sentences drift.

Neither test can stop code that means to re-derive the signal for itself — the
superseded record says so about its own guards, in a paragraph that stays
true — and neither is claimed to. They catch the accident, which is what a
reviewer skimming a diff is most likely to wave through.

**What is not covered.** The carve-out reaches the verb a person types. It does
not reach `gropius status`, which the menu bar polls; it does not reach a panel
page that renders the same signal to somebody who did not ask for it; and it
does not reach any warning, anywhere, whatever its wording. Those remain closed
by rule 2 as written.

## Alternatives Considered

1. **Amend with the three-condition carve-out (chosen).** The record that names
   firewall state as governed also says what a diagnostic may do with it, in
   the place a future reader looks, with two tests holding the conditions.
2. **No amendment; record a dated ledger line saying a user-invoked diagnostic
   is presentation under rule 1.** Cheapest, and the reading is defensible.
   Rejected: the exemption would live in an append-only ledger while the
   ratified record went on naming a firewall's state as governed, so a reader
   who was not in the room could not tell which was in force — which is the
   whole job of the record.
3. **Omit the firewall check from doctor.** No tension, no amendment.
   Rejected: the empty response from a machine across the room *is* the
   firewall grant, and it is the failure the intent's press release opens on. A
   doctor that cannot speak to it is not the doctor the intent promises, and
   the person debugging would go and read the same query themselves with none
   of the labelling this record requires.
4. **Report the signal, but print it only when it looks like a fault.**
   Attractive, because most runs would stay quiet. Rejected outright: deciding
   whether to print is a firing condition on inferred state, which is precisely
   what rule 2 closes, and the decision would rest on a query measured to
   answer "permitted" for a path with no entry. Doctor prints the line every
   time.
5. **Supersede the whole of adr-2609081118587999 and restate it.** Rejected:
   everything that record decided about enforcement, bind addresses and
   presentation is in force and unchanged. Restating it would put one decision
   in two documents and hide which sentence actually moved.

## Consequences

- adr-2609081118587999 is marked superseded in part by this record. Its status
  fields change and its content does not; everything it decided other than the
  reach of rule 2 over a deliberately invoked diagnostic remains in force and
  is read from there, not from here.
- itd-2609081259532589's doctor criteria are admissible as drafted, and its
  spec carries the two tests condition 3 names. The wording test is what makes
  "observed, never a verdict" a property of the build rather than of the
  reviewer who read the first version of the strings.
- **What this costs, stated plainly.** A rule that was absolute now carries two
  exceptions rather than one, and an exception is a thing future work reasons
  from — the more so when there are two, because two look like a pattern where
  one looked like a case. The boundary is deliberately narrow: one class of
  verb, invoked by a person, reporting what it saw, gating nothing, under three
  conditions that are armed. Anything wider is a new decision, not an extension
  of this one.
- The honest limit of the diagnostic is now a ratified expectation rather than
  a disappointment: for two of the states an operator most wants settled,
  doctor names what it saw and what to run, and stops. Documentation written
  against this record says that, and never that doctor checks the firewall.
- If a Developer ID is ever bought, the grants stop being invalidated on every
  build and the firewall query stops being the only route to them. That does
  not repeal this record — the query's answer for a path with no entry is
  unchanged by notarisation — but it is the point at which whether doctor can
  verify rather than observe is worth asking again, as a new decision.
