---
schema_version: 1
id: "iss-2609120417422598"
slug: "install-sh-s-checksum-check-exits-0-without-verifying-the-bu"
severity: "critical"
category: "observation"
source: "user-observation"
found_during: "adversarial security review of spc-2609111812370705, the update verb"
origin: researcher-authored
production_mode: hand-written
found_at: "install.sh"
resolution: "install.sh now narrows SHA256SUMS.txt to the archive's own line before /usr/bin/shasum sees it, drops --ignore-missing with the narrowing, refuses a line naming a path, and reads the pass line by line — the same three moves the update verb took in d7e31ab (internal/lifecycle/updatefetch.go). Held by TestTheInstallerVerifiesTheArchiveItDownloadedIsInTheChecksums, five rows against the real script and a real /usr/bin/shasum: the capture's own /dev/null reproduction and a line naming the archive by a path both exited 0 with the download unpacked before the change, and both are refused after it; the honest bare-name line still passes and a wrong digest still fails as a mismatch. Test cb21caf, fix 9ecd1ad; TestTheInstallerVerifiesBeforeItHandsOver re-anchored off the removed flag."
impact: fix
resolved_by:
  commit: "9ecd1ad"
---

install.sh's checksum check exits 0 without verifying the bundle when SHA256SUMS.txt names any other readable file

## Why this is critical

`install.sh` is the command the README tells people to pipe into bash, and this
line is the only integrity control in the whole install path:

```sh
/usr/bin/shasum -a 256 -c --ignore-missing SHA256SUMS.txt
```

`shasum -c` answers for the files the checksums file NAMES. `--ignore-missing`
skips names that are absent; names that are present and irrelevant are verified
and reported as a pass. Nothing asserts that `Gropius.app.zip` was among them.

## Reproduction

Measured against this Mac's own `/usr/bin/shasum`:

```
$ cat SHA256SUMS.txt
e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  /dev/null
$ cat Gropius.app.zip
HOSTILE ARCHIVE CONTENT, NOT THE RELEASE
$ /usr/bin/shasum -a 256 -c --ignore-missing SHA256SUMS.txt
/dev/null: OK
EXIT=0
```

The script then prints `Checksum OK.`, clears the quarantine attribute, and
hands over to the binary inside that archive — which places it, grants it
through the firewall as root, and launches it.

## What has to be true for it to bite

Control of the response to the `SHA256SUMS.txt` request. HTTPS is pinned across
redirects, so this is not a passive network position; it is a compromised or
substituted response for that one asset. But the whole point of the checksum
step is to be the control that survives a bad archive, and it does not survive
a bad checksums FILE — which is fetched over exactly the same channel, from
exactly the same origin, by the same function.

The comment above the line says it "fails closed on all four" of the inputs it
was verified against. It does; this is a fifth input nobody tried.

## The fix, as taken in the Go verb

`internal/lifecycle/updatefetch.go` (branch `feat/update-verb`,
spc-2609111812370705) now narrows the checksums file to the line naming exactly
`Gropius.app.zip` before shasum sees it, drops `--ignore-missing` with the
narrowing, refuses a line whose name carries a path separator, and reads the
pass line by line rather than as a substring. The same three moves apply to the
shell, and `TestInstallerPinsEveryCommandItRuns` and the release gate both need
looking at with the change.

Not fixed here: the update verb's lane is the Go verb, and changing the
bootstrap touches the release gate that runs it. It is written down as its own
change so it is not assumed closed by a verb that fixed the same bug elsewhere.

## Grounds

- pursued: the bootstrap and the update verb now make the same assertion about scope, so a checksums file that does not cover the download refuses in both; it would be shown wrong by any path that reaches shasum without the narrowing, or by a release whose SHA256SUMS.txt names its assets other than by bare name
