---
id: spc-2609091236506400
slug: every-model-carries-a-category-and-a-client-picks-by-it-when
intent: itd-2609091129451578
origin: researcher-authored
production_mode: dictated-and-formatted
---
# every-model-carries-a-category-and-a-client-picks-by-it-when

## Summary

This spec records the model's category — HuggingFace's own `pipeline_tag` and
`tags` — at download time, keeps it on the registry entry, and publishes it on
each `GET /v1/models` entry as two top-level fields under those names, beside a
`chat` boolean the server derives from a rule the operator can change. Gropius
invents no vocabulary: it stores and republishes the Hub's words, and the only
judgement it makes is the rule, which is a setting with a default in
`config.json` and in the control panel's Settings pane. GropiusChat carries its
own copy of the same rule, changeable in its own Settings, and filters its
picker with it. Nothing filters the API: every model stays callable by name.

## Scope

In scope:

- `internal/hub` gains `RepoInfo`, one call to the Hub's repo endpoint that
  returns the same `hub.Model` summary a search result carries.
- `internal/registry`'s `Model` gains `PipelineTag` and `Tags`, persisted in
  `registry.json`, bounded on every path in.
- `internal/app`'s download path captures the category once a transfer has
  completed and hands it to the registry; `Rescan` fetches nothing and clears
  nothing.
- `internal/config` gains `ChatRule` — two lists of Hub words — with a default,
  a resolver, a bound and a repair.
- `internal/gateway` publishes `pipeline_tag`, `tags` and `chat` on every
  models-list entry, to every client.
- The control panel's search tab shows each result's pipeline tag, and its
  Settings pane holds the chat rule.
- `client/GropiusChat` decodes the two fields, applies its own rule, and holds
  that rule in its Settings.
- `docs/models-list.md`, a new how-to page, `client/README.md`, `README.md`.

Out of scope:

- Any API-side filter. `chat` is advisory; the completions path does not read
  it, and criterion 6's test is the standing guard on that.
- A Gropius taxonomy. There is no "chat/coding/other" enum anywhere: the
  intent's open question settled that a coding category is not derivable from
  the Hub (13 of 30 name-identified coder repositories in the 500-repo sample
  carry a code tag), so nothing here names one.
- A migration. Pre-1.0: an entry recorded by an older build simply has no
  category, and gains one when the model is downloaded again.
- Reading the category out of files on disk. The chat template was the other
  candidate signal in iss-2609081743175520 and is not used: the intent chose the
  Hub's metadata, and a second signal would be a second answer to one question.
- Any change to who may call the listing, to `withAuth`, or to the residency
  projection.

## Approach

**Capture, once, at download time.** `hub.Client.RepoInfo(ctx, repoID)` GETs
`/api/models/<repo>` and decodes it into the existing `hub.Model`, which already
declares `PipelineTag` and `Tags` for the search path. It is bounded by the same
`maxJSONBody` every other Hub decode is, and it is the only new network call.
`App.Download` calls it after the transfer completes and before the ready record
is written, beside the two other readings taken there (`dirSize`,
`ReadContextLength`) and outside `dlMu` for the reason those are: it is I/O, and
the pool waits on that lock. It is best-effort under a short timeout — the Hub
being unreachable at that moment must not fail a download of gigabytes that has
already succeeded — so a failure records no category, which is the same state as
a repository the Hub does not tag. A cancelled or failed attempt that restores
the previous ready record restores the category with it, from the prior record
rather than from the network.

**Persist, bounded.** `registry.Model` gains `PipelineTag string` and
`Tags []string`, both `omitempty`. `registry.json` lives in a group-writable
directory in shared-cache mode, so these are two more strings a hostile local
account could plant and the LAN would read: `sanitizeCategory` is the one
canonical primitive that bounds them — a tag longer than `MaxTagBytes` or
carrying a control character is dropped rather than truncated (truncating would
publish a word the Hub never said), and the list is capped at `MaxTags`. It runs
in `Put`, which is the only write path, and in `Open`, which is where a planted
file arrives, exactly as `plausibleContextLength` does. `Rescan` re-derives what
the disk can tell it and nothing else: the category is not on disk, so a
rescanned entry keeps the category it has rather than losing one on every
restart.

**The rule is a setting, and it is the server's.** `config.ChatRule` holds two
lists of Hub words:

```json
"chat_rule": {
  "pipeline_tags": ["text-generation", "image-text-to-text"],
  "required_tags": ["conversational"]
}
```

A model matches when its pipeline tag is one of the first list, and its tags
include every word of the second; comparison is case-insensitive and trimmed,
because Hub casing is not a promise. An empty list means "do not test this
half", which is how an operator says "offer everything". Absent means the
default — the shape `EvictionGraceSec` already uses — and the distinction
survives the file because the struct declares `IsZero` and is written with
`omitzero`, while the two lists inside it are written even when empty. So a
deleted key reads as the default and a cleared pair of fields reads as "no
constraint", and neither can be mistaken for the other. `Load` repairs an
over-long or over-full rule and names it in `Notices.Repaired`, because a
refused `config.json` takes the whole install down to loopback and a tag list is
not worth that. `Clone` copies both lists, so a posted rule cannot land in the
live configuration before `Validate` has seen it.

**Publication.** `handleListModels` adds, per entry: `pipeline_tag` when the
model has one, `tags` when it has any, and `chat` always — the rule's verdict on
that model, read once per request from the configuration in force. All three go
to every client, keyed or not, loopback or LAN: they are properties of the
model, like `context_length`, not facts about what this Mac is doing, which is
the line the residency projection draws. The two exact-key-set tests that guard
that line are updated to expect `chat`, deliberately and in the same change.

**The panel.** The search tab shows each result's pipeline tag as a pill beside
the quantization, or "no tag" — the search payload already carries the field, so
this is presentation only. The Settings pane gains two comma-separated fields
for the rule. `GET /api/settings` answers with the rule *in force*, resolving
the default when nothing is stored, so what the panel shows and posts back is
what the server is using; a save therefore writes the rule explicitly, and
deleting the key by hand is how an operator returns to the shipped default. The
save posts only the fields the form owns, as it does today, so an untouched
setting is untouched: the rule joins the body as one more field, and every
other field's behaviour on a save is unchanged.

**The client.** `GropiusChat` decodes `pipeline_tag` and `tags` beside the
`chat` flag it already reads, and holds its own rule in two `@AppStorage`
strings seeded with the same default the server ships, editable in Settings. Its
verdict per model: when the entry carries either field, the client's own rule
decides; when it carries neither, the server has said nothing the rule can read
and the server's own `chat` flag decides, defaulting to yes when that is absent
too. Against any server that implements this intent an untagged model is not
offered — such a server always publishes `chat`, and the default rule makes it
false without a pipeline tag — while against a server that predates the whole
feature the picker still offers everything, which is the promise already shipped
in `client/README.md`.

**The three surfaces stay in sync by tests, not by habit.** Two pins:
`internal/ui`'s settings test reads the field names off `config.ChatRule`'s JSON
tags and the default off `config.DefaultChatRule()`, and asserts the panel posts
the first and shows the second; `internal/archtest` reads the Swift client's
default rule out of its source and asserts it is the server's default, and that
the picker is filtered through the rule rather than from the served list.

## How each acceptance criterion is satisfied

1. _Given Alice searches for models to download, when results are listed, then
   each shows HuggingFace's pipeline tag, or "no tag" when the Hub reports
   none._ `renderSearch` draws a `pipelineLabel(m)` pill. Tests, in
   `internal/ui`: `pipelineLabel` evaluated against a result with a tag, one
   with an empty tag and one with no field at all, returning the tag and "no
   tag"; plus the wiring assertion that `renderSearch` calls it, which is the
   line a value test cannot reach without a DOM.
2. _Given a download completes, when the registry records the model, then the
   pipeline tag and tags are stored with it; a download the Hub did not tag
   stores none._ `App.Download` calls `RepoInfo` and passes both onto the ready
   record. Tests, in `internal/app` against the existing fake Hub server: a
   download of a tagged repo records both fields; a download of a repo the
   endpoint answers untagged (and one where the endpoint fails) records neither
   and still lands ready. A `internal/hub` test covers `RepoInfo` itself against
   the package's fake Hub, including a 404. A `internal/registry` test covers
   the round trip through `registry.json` and the bound: a planted file with a
   500-entry tag list, an over-long tag and a control character loads with those
   dropped, and `Rescan` leaves a stored category alone.
3. _Given a client requests the models list, when an entry is returned, then it
   carries the pipeline tag and tags as top-level fields under common names,
   omitted when absent, plus a `chat` flag derived from the server's chat rule._
   Tests, in `internal/gateway`: a tagged model's entry carries `pipeline_tag`,
   `tags` and `chat: true`; an untagged model's entry carries neither field,
   carries `chat: false`, and is still listed; and both key-set assertions are
   updated to the field set that now ships.
4. _Given the server's chat rule is at its default, when a model is
   text-generation or image-text-to-text and tagged conversational, then its
   flag is true, otherwise false; the rule is a setting in config.json and the
   control panel, and a save never refuses a field the operator did not touch._
   Tests: `internal/config` table-tests `DefaultChatRule().Matches` over the two
   accepted pipeline tags, a rejected one, a missing one, a missing
   `conversational` tag, mixed case, and both empty lists; a round trip of the
   file proving absent reads as the default and a cleared pair reads as no
   constraint; `internal/gateway`'s control test posts a body naming only the
   rule and asserts every other setting survives, and posts a body naming
   everything but the rule and asserts the rule survives; `internal/ui` holds
   the panel's field names and shown default to the Go declaration.
5. _Given GropiusChat's picker, when it lists models, then it offers those
   matching its own rule, shipped with the same default and changeable in its
   Settings; a model with no tags is not offered._ The client's rule and its
   verdict, as above. Held by `internal/archtest`: the client's two default
   strings equal `config.DefaultChatRule()`, the picker filters through the
   verdict, the client decodes the two field names the gateway publishes, and
   the Settings sheet binds both rule fields. The load-state pins already in
   that file are kept true — the `chat ?? true` fallback survives as the
   no-fields-published branch — and the one that named the old expression is
   updated to name the new one.
6. _Given an API caller, when it names a model outside the chat rule, then the
   request is served._ Nothing on the completions path reads the category. Test,
   in `internal/gateway`: a model published with `chat: false` is posted a chat
   completion by name and is relayed and answered, in the same test that asserts
   its flag is false, so the two can never drift apart.
7. _Given the fields ship, when a reader opens the models-list reference page,
   then it documents them._ `docs/models-list.md` gains the three rows in the
   field table and a section on the category: what the words are, whose they
   are, when each field is absent, what `chat` means, and that it filters
   nothing. A new how-to page carries the setting on both surfaces;
   `client/README.md` and `README.md` are brought into line.

## Trust-boundary review notes

`internal/hub`, `internal/registry` (via `internal/config`'s reader),
`internal/config` and `internal/gateway` are all touched.

- **New remote input.** `RepoInfo` reads attacker-influencable strings — a repo
  owner writes their own tags — and they are persisted and later served to the
  LAN. They are bounded at the registry boundary rather than at the hub one, so
  the same bound covers a planted `registry.json`, and they are published as
  JSON strings by `encoding/json`, which escapes them; the panel escapes them
  again with `escapeHtml` before they reach the DOM.
- **No new authorisation path.** The three fields are published unconditionally,
  which is a deliberate widening of the listing and is argued above: they say
  what a model is, never what this Mac is doing. `withAuth` and the residency
  projection are untouched.
- **No new refusal.** The rule decides one boolean in a listing. It is not
  consulted on the completions path, so a mis-set rule cannot make a model
  unservable — the failure mode is a picker that offers too much or too little.
- **One extra request per download**, to the host the download is already
  talking to, under a timeout, best-effort.
- The change ships with the adversarial security review the conventions require
  for these packages.

## Docs to change

- `docs/models-list.md` (reference): the three new fields in the table, and a
  section on the category and the chat flag.
- A new how-to page: how to change which models are offered for chat, on the
  server and in the client.
- `client/README.md`: the picker now runs the client's own rule.
- `README.md`: one bullet in the feature list, pointing at the how-to.
- `CHANGELOG.md`: a bullet under `[Unreleased] / Added`.

## Dependencies and sequencing

- Builds on itd-2609061431463108 (the context length), whose spec fixed the
  models-list extension shape this follows: top-level fields, common names,
  absent rather than defaulted.
- Supersedes the earlier same-day decision to publish a chat capability derived
  from the chat template; the flag keeps its name and its client-side default,
  and only its derivation changes.
- Resolves iss-2609081743175520.

## Open design points

- Whether an operator should be able to return the panel's rule to the shipped
  default without editing the file. The panel writes the rule explicitly once a
  save happens, so a later change to the default will not reach an install that
  has saved settings. Recorded rather than solved: a "use the default" control
  is a settings-pane pattern this panel does not have yet, and inventing one for
  one field would be the wrong place to start.
- Whether a model's category should be shown on its card in **My Models**. The
  registry carries it, so this is presentation only.
