# Gropius ADRs

Architecture Decision Records: settled decisions, their context, the
alternatives rejected, and their consequences. Written after the decision is
made, so that a future reader who was not in the room can see why.

An ADR is filed only when all three hold:

1. **Hard to reverse.** Changing it later costs something real.
2. **Surprising without context.** A future reader would ask "why this way?"
3. **The result of a real trade-off.** There were genuine alternatives.

Otherwise the rationale lives inline, or as a dated line in
`.abcd/work/DECISIONS.md`. User-facing capability is an intent, not an ADR.

Records are minted with `abcd decide "<title>"` from v0.8 of the tool
onward, which allocates the id (`adr-<yymmddHHMMSS><rrrr>`), the date and the
filename, and writes the four sections empty. An older binary has no `decide`
command; a record minted by hand follows the shape of the file beside it —
the same filename form, the same frontmatter keys, the same four sections.
The author writes the sections and sets `status: accepted` in the same change
that puts the decision in force. Appending the row to the index below is a
hand edit.

## Index

| ID | Title | Status | Date |
|---|---|---|---|
| [adr-2609061503319212](2609061503319212-no-public-telemetry-local-telemetry-only-as-a-strict-opt-in.md) | No public telemetry; local telemetry only, as a strict opt-in | accepted | 2026-09-06 |
| [adr-2609061610102325](2609061610102325-the-gateway-may-rewrite-prompt-content-only-to-merge-system.md) | The gateway may rewrite prompt content only to merge system messages, per model, opt-in, and never logs, retains or counts what it reads | accepted | 2026-09-06 |
| [adr-2609061610107154](2609061610107154-statistics-store-format-json-lines-size-rotated-per-account.md) | Statistics store format: JSON Lines, size-rotated, per account, with months and size caps in Settings | superseded by [adr-2609090716413337](2609090716413337-the-statistics-store-s-record-kinds-are-request-load-removed.md) on the record kinds | 2026-09-06 |
| [adr-2609070004056820](2609070004056820-the-app-s-three-locks-have-one-order-the-pool-s-mutex-then-t.md) | The app's three locks have one order: the pool's mutex, then the configuration's, and a settings save serialises above both | superseded by [adr-2609091239058072](2609091239058072-the-app-s-four-locks-have-one-order-the-settings-handler-s-a.md) | 2026-09-07 |
| [adr-2609081118587999](2609081118587999-detecting-a-private-network-daemon-may-inform-what-gropius-s.md) | Detecting a private-network daemon may inform what Gropius says, never what it enforces | superseded by [adr-2609111126115848](2609111126115848-a-deliberately-invoked-diagnostic-may-report-an-observed-sig.md) on the diagnostic carve-out | 2026-09-08 |
| [adr-2609090716413337](2609090716413337-the-statistics-store-s-record-kinds-are-request-load-removed.md) | The statistics store's record kinds are a request line, a load, a removal carrying one of seven reasons, and a settings record | accepted | 2026-09-09 |
| [adr-2609091123526871](2609091123526871-gropius-binds-loopback-alongside-every-other-address-with-a-p.md) | Gropius binds loopback alongside every other address, with a private-network mode that fails closed to loopback | accepted | 2026-09-09 |
| [adr-2609091239058072](2609091239058072-the-app-s-four-locks-have-one-order-the-settings-handler-s-a.md) | The app's four locks have one order: the settings handler's, the save lock, then the pool's mutex and the configuration's | accepted | 2026-09-09 |
| [adr-2609111126115848](2609111126115848-a-deliberately-invoked-diagnostic-may-report-an-observed-sig.md) | A deliberately invoked diagnostic may report an observed signal it cannot verify, and never a verdict | accepted | 2026-09-11 |
