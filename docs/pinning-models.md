# Pin a model so it stays in memory

Gropius keeps as many models in memory as its budget allows, and unloads the
one used longest ago when a request needs the room. On a Mac serving more than
one client that is usually right — but not for the one or two models you rely
on all day. Pin those, and nothing anyone else asks for can push them out.

## Pin a model

1. Open the control panel and go to **Settings**.
2. Under **Pinned models**, tick each model you want kept in memory.
3. Read the line under the boxes. It says what the ticked models cost against
   the memory budget and what that leaves for everything else.
4. **Save settings**.

The pins apply at once — no restart, and a model that is already in memory is
protected from that moment. Each pinned model is marked on its card under **My
Models**.

## What a pin does

- The model is never unloaded to make room for another one.
- The idle timeout does not touch it, however long it goes unused.
- A request for a different model that would need its memory is refused
  straight away, rather than served by unloading it.

The client that is refused is told only that there is not enough memory. It is
never told which models are protected, or what this Mac is running.

## What a pin does not do

- **It does not load the model.** Pinning protects a model; it does not put it
  in memory. Until something loads it — a request, **My Models → Load**, or
  **Preload** — the card reads `pinned, not loaded`. Name a model in both
  **Preload** and **Pinned models** to have it loaded at start-up and protected
  from then on.
- **It does not survive everything.** Your own **Unload** works on a pinned
  model, and so does quitting Gropius or a model server crashing. The pin stays
  in Settings either way, so the model is protected again the next time it
  loads.
- **It does not go away when the model does.** Deleting a pinned model leaves
  its pin in Settings, listed as `(not on this Mac)`. It protects nothing —
  there is no model to protect — and downloading the model again brings the
  protection back. Untick it to be rid of it.
- **It does not reserve memory.** Memory is charged when a model is loaded, so
  a pinned model that nothing has loaded holds none of the budget and does not
  stop other models from filling it. (The check below is a different sum: it
  asks what the pinned models *would* cost together, so that a set which could
  never be held is refused before it is saved.)

## Choose how much to pin

Every model in memory is charged its size plus a fifth, and the budget is 60%
of this Mac's physical RAM. The figure beside the boxes is that sum for the
models you have ticked. A model that is still downloading is charged the size
it declares, so pinning one before it lands is measured on the same terms as
pinning one already on disk.

Pin close to the whole budget and Gropius has nothing left to serve anything
else with: every request for an unpinned model is refused rather than served by
a swap. A settings save whose pinned models cannot all be in memory at once is
refused outright, naming both figures, and nothing is changed. Leave room for
the models your clients ask for occasionally.

## When a pinned set stops fitting

The check runs when Settings is saved, and it refuses a save that makes the set
worse — one that pins another model. A set that arrives already too large is
applied and reported rather than refused, so that a `config.json` carried from
a Mac with more memory never stands between you and saving an API key.

A set can also stop fitting with no save at all:

- A pinned model that was deleted is charged nothing until you download it
  again, and the download brings the whole charge back.
- Downloading a model again at a larger quantization grows what it costs.

Nothing refuses either — there is no save to refuse — so Gropius says so on the
control panel and in its log instead. The symptom to recognise, if the warning
is missed, is every unpinned model being refused for memory on a Mac that
plainly has some.

## Unpin a model

Clear its box in **Settings → Pinned models** and save. The model stays in
memory and goes back to being evictable like any other. Every pin has a box,
including one naming a model this Mac no longer has, so nothing is stuck
pinned.

## See also

- [Reference: the models list](models-list.md) — the `pinned` field a client
  reads, and the eviction rules a pin changes.
