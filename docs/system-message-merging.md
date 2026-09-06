# Merge system messages for a template-strict model

Some models refuse a conversation whose instructions are not all at the top.
That breaks any assistant which repeats its instructions as the conversation
goes on: the model answers the first message and returns a template error on
the next one. This page is how to switch merging on for such a model, and what
switching it on agrees to.

Merging is per model and is off until you switch it on. It is the one setting
that has Gropius read part of a request rather than pass it straight on, which
is why it is granted model by model and why the rule it runs under is written
down as
[an architecture decision](../.abcd/development/decisions/adrs/2609061610102325-the-gateway-may-rewrite-prompt-content-only-to-merge-system.md).

## Switch it on

1. Open the control panel and go to **Settings → Merge system messages**.
2. Tick the box beside the model whose template refuses the pattern.
3. **Save settings**.

It applies from the next request. Clear the box and save to switch it off
again; the request after that goes to the model exactly as it arrives.

## What it does to a request

For requests to that model, Gropius gathers the instruction (`system`)
messages into the first one, in the order they were sent and separated by a
blank line, and passes every other message and field on untouched. An
instruction that is empty adds nothing, so the prompt does not begin with a
blank line.

The merged message is the conversation's own first instruction message with
its text extended, so anything else that message carries — a `name`, a cache
directive — stays with it. Nothing is invented: its text starts the merged
one and the later instructions are added to it. A lone instruction message
that is merely in the wrong place is moved with everything it carries.

## What it reads, and what it keeps

For a model you switch it on for, Gropius reads the instruction messages of
each request and nothing else: the role of every message, and the text of the
instruction ones. It keeps none of what it reads. Nothing of a request's
contents reaches the log, the model server's command line, or any file on
disk, at any log level.

Every other model is untouched, and so is every request to `/v1/completions`,
which carries a prompt rather than a conversation.

## What it passes on unrewritten

Merging never rewrites a request it cannot rebuild exactly. Such a request
reaches the model as it arrived, and the log records that it did — naming the
model, and nothing from the request itself. Those cases are:

- an instruction whose text arrives as a list of content parts rather than as
  plain text, as `null`, or not at all;
- an instruction whose text is bytes that are not valid text, which could only
  be passed on by substituting characters the client never sent;
- an instruction message after the first one carrying anything beyond its role
  and its text — its words are about to be added to the message above, and a
  `name` on it has nowhere honest to go;
- a `messages` value that is not a list of messages at all.

A conversation that already has its instructions in one message at the front
needs no rewrite and gets none.
