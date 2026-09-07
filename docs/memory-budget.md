# Set how much memory models may use

Gropius holds as many models in memory as its memory budget allows, and unloads
the one used longest ago when a request needs the room. The budget is a share of
this Mac's memory, and it is a setting: a Mac that does nothing but serve models
can give them most of itself, while a laptop someone also works on wants less.

## Set the budget

1. Open the control panel and go to **Settings**.
2. Type a figure in gigabytes into **Memory for loaded models**. Leave the field
   blank for the default.
3. Read the line under the field. It says what the budget is, what share of this
   Mac's memory that is, and what the models in memory are using of it.
4. **Save settings**.

The budget applies at once — no restart. The next model to load is measured
against the new figure, and the **Search** tab immediately hides the models that
no longer fit and shows the ones that now do.

## What the default is

With the field blank, the budget is 60% of this Mac's memory, which leaves the
rest for macOS and everything else on a machine whose GPU and CPU share one pool
of memory. Nothing is stored in that case: the figure is worked out on the Mac
that is running, so a settings file copied to a smaller Mac gets that Mac's
default rather than the first one's number.

If this Mac's memory cannot be read at all, the budget is a conservative 8 GB,
the field shows no percentage, and any figure you type is taken at its word.

## What counts against it

Each model in memory is charged its size on disk plus a fifth — the weights it
loads, with headroom. That is the same figure the eviction rule uses, so the
number under the field and the number a refusal quotes are one number. Two
models of 45 GB and 29 GB are charged about 89 GB together, and they sit in
memory together on a Mac whose budget is above that.

Gropius charges what a model loads, not what a conversation adds to it. Each
request in flight also holds a cache of the prompt it is working through, which
grows with the length of that prompt and differs sharply between architectures —
dense models pay the most for it — and the model server keeps such caches
between requests. None of that is counted. It is a known limit, open in the
project's issue ledger as `iss-3`, and it is the reason a budget claiming most
of the Mac draws a warning rather than an assurance: a machine
fully committed on paper can still run out of memory under long prompts served
concurrently.

## What a change does not do

- **Lowering the budget unloads nothing.** The models in memory stay, and the
  panel says the machine is over its budget until they go by the usual rules —
  a request for something else, the idle timeout, or your own **Unload**. A
  model is never taken away at the moment you press Save.
- **The budget is not a hard ceiling during a swap.** A model being replaced is
  credited back its memory as the replacement starts, so the two overlap for the
  seconds it takes the first to exit.
- **It does not reserve anything.** A budget of 100 GB on a Mac running other
  work is a promise Gropius cannot keep for it; the figure only decides what
  Gropius is willing to load.

## What Gropius refuses

- A budget larger than this Mac's memory. The save is refused, naming what the
  Mac has, and nothing is written.
- A budget below what the [pinned models](pinning-models.md) cost together. A
  pinned model is never unloaded, so a budget under their sum leaves no room for
  anything else and every other request is refused. The save is refused, naming
  the sum.

A pinned set that arrives already over budget — a settings file carried from a
Mac with more memory — is reported rather than refused, so it never stands
between you and saving an unrelated setting. Raising the budget is always
allowed; lowering it under such a set is not.

## Choose a figure

Alice's Mac Studio serves models and does nothing else, so she raises the budget
to most of the machine and keeps her writer and her reviewer in memory together.
Bob works on the laptop he serves from, so he leaves the default alone: 60% is
chosen to keep the rest of the machine usable.

Add up the charged sizes of the models you want resident at the same time, leave
room for the ones your clients ask for occasionally, and leave more room again
if those clients send long prompts. The eviction rules the budget drives are set
out in the [models list reference](models-list.md).
