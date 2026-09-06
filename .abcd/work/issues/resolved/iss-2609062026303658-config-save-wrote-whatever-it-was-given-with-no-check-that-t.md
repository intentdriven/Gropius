---
schema_version: 1
id: "iss-2609062026303658"
slug: "config-save-wrote-whatever-it-was-given-with-no-check-that-t"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "2026-09-06 sampling defaults build"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/config/config.go"
resolution: "config.Save now refuses a config whose marshalled bytes would exceed MaxConfigBytes, so it can never write a file config.Load will not read back; config.ValidRepoID also bounds each half of a repo id at 96 characters, which turns the 256-override ceiling into a bound on the file's size rather than only on its entry count. Covered by TestSaveRefusesAConfigTooLargeToLoadBack, TestValidRepoIDBoundsLength, TestAFullOverrideMapStillFitsTheConfigFile and, end to end through the settings endpoint, TestASaveCannotWriteAConfigTheNextStartRefuses."
impact: fix
resolved_by:
  intent: "itd-2609061429508050"
  spec: "spc-2609061822378193"
---

config.Save wrote whatever it was given, with no check that the result fits under the limit config.Load reads back. A settings save large enough to exceed it wrote a config.json the next start refuses, and main then falls back to loopback-only with the shipping defaults, so the bind address and API key a user set are silently unused until someone edits the file by hand.

## Grounds

- pursued: we expect no accepted settings save to be able to write a config.json the next start refuses to read, since that reverts the bind address and API key to the shipping defaults with no visible cause; we are wrong if any field reachable from the endpoint can still push the marshalled file over the loader's limit
