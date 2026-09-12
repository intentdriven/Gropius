---
schema_version: 1
id: "iss-2609120438396694"
slug: "gropius-install-prints-five-error-texts-unredacted-install-g"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
---

gropius install prints five error texts unredacted: install.go lines 108, 174, 191, 212 and 243 write err.Error() straight to the terminal, and 243 redacts the destination on the same line while the error beside it is not, which defeats the redaction. The update verb's quit warning was fixed on fix/update-verify-scope; the install verb's siblings were not, and there is no test holding any lifecycle warning line to the redaction rule. Found by the security review of that branch.
