# Why there is a memory budget

Apple Silicon has unified memory: the weights a model loads come out of the same
pool as the window server, the browser and everything else. Nothing in macOS
stops a process filling that pool, and a model server that does takes the
machine down with it. So Gropius decides for itself how much of the Mac it is
willing to fill, and holds every load to that figure. The
[models list reference](models-list.md) states the figures and the rules; this
page is about what they mean.

## Why the default is a share, worked out per machine

The default is a share of the memory this Mac has, rather than a number in the
settings file. That is deliberate: a settings file copied to a smaller Mac would
otherwise carry a figure chosen for a machine that no longer exists. Leave the
field blank and each Mac answers for itself.

The share is chosen to leave the rest of the machine usable, which is the right
default for a Mac someone also works on and a poor one for a Mac that does
nothing else. That is the whole reason the figure is a setting.

## Why it is bounded by the machine

A budget larger than the memory that exists cannot be honoured, so Gropius does
not pretend to: whatever the settings file says, models are held to what this
Mac has. The stored figure is left alone — it is yours, and it may have been
written for a bigger machine you will move the file back to — but nothing is
admitted on the strength of memory that is not there.

If this Mac's memory cannot be read at all, there is nothing to bound the figure
to. Gropius falls back to a conservative default, shows no percentage, and takes
any figure you type at its word.

## What the budget does not count

A loaded model is charged its weights and headroom — the reference gives the
figure. What that charge leaves out is the cache each request builds as it works
through its prompt. That grows with the length of the prompt, differs sharply
between model architectures (dense models pay the most for it), and the model
server keeps such caches between requests, so a Mac loaded exactly to its budget
can still run out of memory under long prompts served at the same time. It is a
known limit, open in the project's own issue ledger.

This is why a budget claiming most of the machine draws a warning rather than an
assurance, and why leaving room matters more the longer your prompts are.

## Why a change applies to the next load

Lowering the budget unloads nothing. A model in memory is one somebody is
probably using, and taking it away at the moment the operator pressed Save is
the one thing they were not asking for. The new figure governs the next load
instead, and the panel reports the machine as over its budget until the models
resident at the time go by the usual rules.

A swap does not lift the ceiling either. A model being replaced keeps its share
of the budget until its server process has actually gone, so the request that
needed the room waits out those seconds rather than starting a second server on
top of the first. That wait is bounded: a server that will not go — one the
system has stopped and then killed, and which is still there — leaves the
request refused rather than held, and the panel says how much memory is being
held that way and by how many servers. Until they go, the budget is that much
smaller than it looks.

## Choosing a figure

Alice's Mac Studio serves models and does nothing else, so she raises the budget
to most of the machine and keeps her writer and her reviewer in memory together.
Bob works on the laptop he serves from, so he leaves the default alone.

Add up the charged sizes of the models you want resident at the same time, leave
room for the ones your clients ask for occasionally, and leave more again if
those clients send long prompts. [Pinning](pinning-models.md) is the other half
of this decision: it decides which models keep their place in the budget when
something else needs the room.
