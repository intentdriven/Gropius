# Choose which models are offered for chat

Not every model can hold a conversation. A speech-recognition model, an OCR
model or a base model without a chat template is worth downloading and worth
calling, but it has no business in a chat application's model menu — the first
message sent to one is wasted.

Gropius records what HuggingFace says each model is when you download it, and
publishes those words on the models list. A rule decides which of them count as
able to chat. There are two places to change it: the server, which is what every
client is told, and the GropiusChat client, which applies its own.

## On the server

1. Open the control panel and go to **Settings → Which models can chat**.
2. **Pipeline tags that count** is a comma-separated list of HuggingFace
   pipeline tags. As shipped: `text-generation, image-text-to-text`.
3. **Tags a model must carry** is a second comma-separated list, and a model has
   to carry all of them. As shipped: `conversational`.
4. **Save**. The change applies at once — no restart, and nothing is unloaded.

Clear a field to stop testing that half. Clear both and every model is marked as
able to chat, which is the setting to reach for if the rule is hiding models you
want.

The same rule lives in `config.json`, which is hand-editable:

```json
"chat_rule": {
  "pipeline_tags": ["text-generation", "image-text-to-text"],
  "required_tags": ["conversational"]
}
```

Delete the `chat_rule` key to go back to the rule Gropius ships.

Each list holds at most 64 words, of at most 128 bytes each. A save that goes
beyond that is refused and names the field; a `config.json` that does is
repaired on the next start and the panel says which setting was changed, rather
than the server refusing to start.

## In the chat client

GropiusChat applies its own rule to the same words, so the models it offers are
its user's decision rather than the server's. Open **Settings** — the gear in
the toolbar — and edit the two fields under **Models to offer**. They ship with
the server's own default.

## What the rule does and does not do

- **It filters nothing.** Every model stays callable over the API by the name it
  is listed under, whatever the rule says of it. The rule decides one field on
  the models list, `chat`, which a client is free to act on or ignore. See
  [the models list reference](models-list.md).
- **The words are HuggingFace's.** Gropius invents no categories: it republishes
  the repository's pipeline tag and tags as they are written on the Hub, and the
  search tab shows each result's pipeline tag — or "no tag" — before you
  download anything.
- **A model with no tags is marked as unable to chat** under the shipped rule,
  because the rule asks for a pipeline tag it does not have. Models downloaded
  before Gropius recorded these words carry none: nothing on disk says what kind
  of model it is, so download such a model again to give it its words, or clear
  the pipeline-tag field to stop testing that half.
