# Set how much memory models may use

Gropius holds as many models in memory as its memory budget allows. The budget
is a share of this Mac's memory by default, and it is a setting: a Mac that does
nothing but serve models can give them most of itself, while a laptop someone
also works on wants less.

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

To go back to the default, clear the field and save. The line under it then
shows the default figure for this Mac.

## What the save tells you

A save is refused, with nothing written, when it makes matters worse:

- **It raises the budget past this Mac's memory.** The message names what the
  Mac has.
- **It lowers the budget under what the [pinned models](pinning-models.md) need,
  when the budget in force can still hold them.** A pinned model is never
  unloaded, so a budget under their sum leaves no room for anything else. The
  message names the sum.

Otherwise the save goes through, and the panel says so when the figure is worth
a word:

- **A budget that claims most of the Mac** is saved with a warning. macOS and
  everything else running share this memory, and a model is charged what it
  loads rather than what a long conversation adds to it, so the machine can
  still run out.
- **A budget too small to hold the smallest model on this Mac** is saved with a
  warning too, naming what that model needs. Every request is refused until the
  budget is raised.

A figure that arrives already over this Mac — a settings file carried from a
bigger one, or written during a start where the machine's memory could not be
read — is applied rather than refused, so it never stands between you and saving
an unrelated setting. Models are still held to the memory this Mac has, and the
panel and the start-up log show the figure being enforced. Change the field to
put your own figure back in charge.

## What a change does not do

Lowering the budget unloads nothing. The models in memory stay, and the panel
says the machine is over its budget until they go by the usual rules — a request
for something else, the idle timeout, or your own **Unload**. A model is never
taken away at the moment you press Save.

## Where the rules are

The [models list reference](models-list.md) states what a loaded model is
charged, what the default share is, and the rules by which models are unloaded.
[Why there is a budget at all](memory-budget-explained.md) explains what those
figures do and do not account for, and how to pick one.
