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

## What a model is charged, and why the cache is most of it

A loaded model is charged three things: its weights, a fifth of them again for
the working set a running model needs whatever the prompt is, and the attention
cache the window it is served at costs — once for every sequence its server may
decode at once.

The cache is the term that decides which models can share this Mac. It grows
with the prompt, and what it costs per token is a property of the architecture
rather than of the model's size: measured on a 128 GB Mac, four models between
18 GB and 45 GB of weights ranged from 12 KB to 353 KB per token, a thirtyfold
spread, and the largest of them was not the most expensive. The model server
also keeps a prompt's cache after the answer is sent, and caches stack across
requests, so the charge is what the model may come to hold rather than what it
holds the moment it loads.

The figure per token is read from the model's own configuration: how many of
its layers attend over the whole prompt, and what one layer's entry costs. That
arithmetic is a floor rather than an estimate — every model measured held more
than it, because a server keeps more than the raw cache — so Gropius multiplies
it: five times for a model that caches keys and values per head, seven for one
that caches a compressed latent, each the top of what its own kind was measured
holding. A model whose configuration cannot be read is charged the flat figure
instead: its weights plus a fifth.

## The window is the operator's, and it is the same window twice

The window in that charge is the one **Settings → Served context** holds for
the model, and the window the model's own configuration declares when that
field is blank. It is one figure doing two jobs, and that is what makes it
trustworthy: the memory budget charges the cache this window costs, and the
gateway refuses a request estimated to be larger than it. Gropius never budgets
for one window and then serves another.

That is what to reach for when a model will not fit. A model declaring 262,144
tokens can cost more than a whole Mac at that window; served at 32,768 it costs
a fraction of it, and it is a model this Mac can hold beside another rather
than one it cannot hold at all. Lowering **Batched requests (decode
concurrency)** does the same thing, in proportion: each sequence that may run
at once holds its own cache.

A model whose charge does not fit the budget is refused rather than loaded on a
smaller figure. The refusal names the window and the concurrency it was charged
at and the largest of each that would fit, so what to change is in the message.
Gropius will not quietly charge a model less than it costs: a budget that lies
by a factor is worse than one that says no.

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
held that way and by how many servers.

Nothing waits on that memory any more. Gropius has already asked the server to
stop and then killed it; there is nothing further it can do, so it stops
counting on the memory coming back and works around the loss instead — models
go on loading and being swapped inside what is left, rather than every request
for room being refused from then on. If the system does let go of the server
later, the memory is credited then, and the panel stops reporting it. If it
never does, restarting Gropius is the remedy; the log names the server on the
way out, so the next start can finish the job.

## Choosing a figure

Alice's Mac Studio serves models and does nothing else, so she raises the budget
to most of the machine and keeps her writer and her reviewer in memory together.
Bob works on the laptop he serves from, so he leaves the default alone.

Add up the charged sizes of the models you want resident at the same time and
leave room for the ones your clients ask for occasionally. The charge already
counts the long prompts, so a set that fits is one this Mac can serve at those
lengths rather than one it can merely load. [Pinning](pinning-models.md) is the
other half of this decision: it decides which models keep their place in the
budget when something else needs the room.
