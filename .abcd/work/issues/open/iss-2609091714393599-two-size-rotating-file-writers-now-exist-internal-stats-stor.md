---
schema_version: 1
id: "iss-2609091714393599"
slug: "two-size-rotating-file-writers-now-exist-internal-stats-stor"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "implementing spc-2609091703459471 (the server's own log)"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/applog/applog.go"
---

Two size-rotating file writers now exist: internal/stats/store.go's storeWriter and internal/applog's rotator. Both append lines to a 0600 file opened O_CREATE|O_WRONLY|O_APPEND|O_NONBLOCK|O_NOFOLLOW through an os.Root on their own directory, both start a new file when the next line would take the current one past a byte cap, and both remove the oldest file to stay under a bound. They were not unified when the second arrived because the store's rotation is not a size-rotating file writer with a store on top: its file names carry a UTC day and a counter, its pruning enforces two bounds (a months horizon and a byte ceiling), and what it drops is folded into a summary file that itself counts toward the ceiling. Extracting the shared primitive means separating the rotation from the day-numbering, the fold and the retention arithmetic inside the package that owns this repository's durable data format, which is a larger and riskier change than the log file that prompted it. The canonical home, when it is extracted, is the smaller of the two: internal/applog's rotator is the plain primitive and the store's writer is the specialisation. What would show this wrong is the two drifting on the discipline they share — one gaining a check on the opened handle, or a mode, or a symlink refusal that the other does not.
