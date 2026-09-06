# Set default sampling parameters

Most tools that speak the OpenAI API never send a temperature. Whatever the
model server picks then applies to every one of their requests. This page is
how to decide that yourself, once, for the whole machine.

Every parameter, its range and what a blank field means are in
[Reference: sampling parameters](sampling-reference.md). Why the defaults work
the way they do is in [How sampling defaults work](sampling-explained.md).

## Set a machine-wide default

1. Open the control panel and go to **Settings → Sampling defaults**.
2. Fill in the parameters you want, and leave the rest blank.
3. **Save settings**.

The message under the button names any model that is already loaded and needs
loading again — see [When a change takes effect](#when-a-change-takes-effect).

A default fills a gap and nothing more: a request that sets `temperature`
itself is still served with its own value.

## Give one model its own figures

1. Go to **Settings → Per-model sampling**.
2. Choose the model. The fields fill in with whatever it already has.
3. Set the ones that should differ from the machine-wide defaults. A field
   left blank keeps the machine-wide value, so an override can be a
   temperature alone.
4. **Save settings**.

**Set override** is for building up several models' overrides before saving;
the fields are part of the settings form, so **Save settings** takes whatever
is in them either way.

To remove an override, clear every one of its fields, or use **Remove** beside
it in the list, then **Save settings**.

## When a change takes effect

A change reaches a model the next time that model loads. One that is already
loaded goes on serving with the values it started with until it is unloaded —
from **My Models → Unload**, or by an idle timeout — and loaded again. The
saved-settings message names the loaded models this applies to.

To apply a change at once, unload the models it names and let the next request
load them again.

## If a value is refused

The panel refuses a value outside a parameter's range, names the field, and
changes nothing else. The ranges are in
[Reference: sampling parameters](sampling-reference.md).

## Make generation repeatable

Set a temperature of 0 and leave it there. At 0 the same prompt gives the same
answer every time. There is no seed to set — see
[How sampling defaults work](sampling-explained.md#why-there-is-no-seed).
