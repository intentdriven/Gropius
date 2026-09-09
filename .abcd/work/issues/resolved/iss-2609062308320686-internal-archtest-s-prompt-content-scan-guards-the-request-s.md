---
schema_version: 1
id: "iss-2609062308320686"
slug: "internal-archtest-s-prompt-content-scan-guards-the-request-s"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "adversarial security review of feat/local-statistics"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/archtest/prompt_content_test.go"
resolution: "The prompt-content scan now covers both sides of a conversation: completionFields ('choices', 'delta', choicesField) is scanned against generatedContentReaders, with internal/gateway/gateway.go and the mlx fake as the only listed files."
impact: internal
---

internal/archtest's prompt-content scan guards the request side of a conversation (messages, role, content) but not the response side. The relay now names choices and decodes that array to tell a chunk of an answer from the counts-only event; nothing reads inside a choice today, and nothing would fail if a future change did. Adding the response-side field names to the same scan, with internal/gateway/gateway.go as the one listed reader, would make a second reader of generated content as visible as a second reader of a prompt already is. Raised by the adversarial security review of feat/local-statistics; it belongs to adr-2609061610102325's boundary rather than to the statistics intent.

## Grounds

- pursued: a second reader of a generated answer is now as visible as a second reader of a prompt — it has to be added to a named list in a diff someone reviews; we are wrong if a reader reaches the answer through a field name the list does not carry (a type declared in a listed file, a key built by concatenation, or the counts path, which is deliberately not scanned).
