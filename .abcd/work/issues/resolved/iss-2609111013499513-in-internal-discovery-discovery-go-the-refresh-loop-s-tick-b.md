---
schema_version: 1
id: "iss-2609111013499513"
slug: "in-internal-discovery-discovery-go-the-refresh-loop-s-tick-b"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "fixing iss-2609091950213211, 2026-09-11"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/discovery/discovery.go"
resolution: "The refresh loop's tick branch now checks ad.stopped() — a non-blocking read of the responder's done channel — before claiming recovery. A responder whose death was already sitting unread in ad.done, having lost the random select toss to the tick, is no longer announced as a republished service and no longer costs a second outage report on the pass that reads it."
impact: fix
---

In internal/discovery/discovery.go the refresh loop's tick branch infers recovery from 'serving', which only means a serve was issued, not that the responder is up: when a retry's Respond has not yet failed by the next tick, or select takes tick.C while ad.done is also ready, the loop logs 'network advertisement republished', clears 'down', then logs 'has stopped' a second time when it sees the failure, and 'republished' again on the real recovery. With the 15 s production interval this is a misleading pair of log lines during an outage whenever dnssd's Respond takes longer than one interval to fail; the retry itself still happens. Found while fixing the flake in TestAResponderThatFailsIsServedAgainOnTheSameRegistration (iss-2609091950213211), which is the same window at 1 ms. A non-blocking read of ad.done before claiming recovery closes it.

## Grounds

- pursued: the recovery report now turns on the responder being up now, not on a serve having been issued an interval ago — TestAResponderThatHasAlreadyGivenUpIsNotReportedAsRecovered forces the coincidence by holding the loop in its own log call until the responder it just started has failed, and fails 5 of 5 runs before the change (3 to 7 outage reports across one continuous outage) and passes 20 of 20 after. What would show it wrong: a responder that is still inside Respond when the tick comes round is still reported as recovered, because the loop has no evidence of its death — that case is the interval's own semantics, not this defect.
