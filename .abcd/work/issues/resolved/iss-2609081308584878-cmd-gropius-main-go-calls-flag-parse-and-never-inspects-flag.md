---
schema_version: 1
id: "iss-2609081308584878"
slug: "cmd-gropius-main-go-calls-flag-parse-and-never-inspects-flag"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "manual-capture"
origin: researcher-authored
production_mode: hand-written
resolution: "main now reads flag.Args() and refuses any positional argument before anything else happens, exiting 2 with a message naming the offending word and pointing at an invocation that works. Verified on the built binary: 'gropius install' prints the refusal and exits 2 where it previously bound the configured address, generated and printed an API key, advertised over Bonjour and never returned; 'gropius -version' still prints and exits 0; a mistyped flag still gets the flag package's own usage. The rule is extracted as refuseUnknownArgs in cmd/gropius/args.go so it is testable away from main, following the shape settings_warning_test.go already uses, with cmd/gropius/args_test.go covering the empty, single-word, word-then-flag and remedy-text cases. The test was watched to fail before the change and pass after. Only the first offending argument is named, because flag stops parsing at the first non-flag and listing the trailing flags would suggest they were the problem. Full suite green under -race, gofmt clean, vet clean."
impact: fix
---

cmd/gropius/main.go calls flag.Parse() and never inspects flag.Args(), so any unrecognised positional argument is silently ignored and the process falls through to runServer. Verified live: 'gropius install' binds [::]:11535, generates and prints an API key, begins Bonjour advertising, starts provisioning and never exits. A typo, a stray shell argument, or a future subcommand name run against an older binary all start a LAN-exposed server instead of reporting a usage error. Reject unknown positional arguments with exit 2. Found during installer feasibility review; worth fixing independently of that work.

## Grounds

- pursued: we expect a refusal here to be strictly safer than the silent fall-through, because nothing in this repository invokes the binary with a positional argument — the bundle is launched by macOS with none, make run passes only -headless, and install.sh uses open — so the only callers who can hit the refusal are ones who were already getting behaviour they did not ask for. We are wrong if some launch agent, wrapper script or user habit in the wild passes a stray word deliberately and now gets exit 2 where it used to get a working server; that would show as a report of the app refusing to start after an update, and the remedy would be to ignore unrecognised words only when they cannot be confused with a subcommand, which is a weaker guarantee than this one.
