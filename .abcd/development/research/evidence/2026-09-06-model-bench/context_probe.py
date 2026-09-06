"""Context-window bisection probe for Gropius models.

For each model, finds the largest prompt the server accepts without error, by
bisecting prompt length between a known-good floor and a cap (default 128K
tokens). The real token count is read from the response's usage.prompt_tokens,
so tokenizer and chat-template overhead are accounted for exactly.

Failure modes recorded: HTTP 4xx/5xx, connection reset (child crash / OOM),
timeout, or a 200 without usage. Run at low system load: memory pressure can
trigger model eviction, which both distorts timings and stalls other clients.
Requests share prefixes, so mlx-lm prompt caching (if any) reduces prefill
work on later bisection steps.

Usage: python3 context_probe.py [--cap 131072] [--models id,id,...] [--floor 1024]
"""
import argparse, json, math, os, sys, time, urllib.request, urllib.error

GROPIUS = "http://127.0.0.1:11535/v1/chat/completions"

# Varied filler: distinct paragraphs so the tokenizer sees natural text.
_BLOCK = ("The lighthouse keeper counted the waves as they broke against the seawall, "
          "marking each seventh one in a small leather notebook. {} "
          "Morning fog delayed the ferry, and the gulls circled the harbour in slow, "
          "patient spirals while the fishermen mended their nets on the pier. ")
_MARKERS = ["Nearby,", "Meanwhile,", "Later,", "Somehow,", "Quietly,", "Often,", "Yesterday,",
            "Today,", "Besides,", "However,", "Beyond,", "Above,", "Below,", "Perhaps,",
            "Certainly,", "Suddenly,", "Gradually,", "Finally,"]


def filler_tokens(target_tokens, chars_per_token):
    """Build filler text of roughly target_tokens tokens."""
    n = max(1, int(target_tokens * chars_per_token / len(_BLOCK)))
    parts = []
    for i in range(n):
        parts.append(_BLOCK.format(_MARKERS[i % len(_MARKERS)]))
    return "".join(parts)


def probe(model, text, timeout):
    """One request. Returns dict with ok, prompt_tokens, prefill_s, error."""
    body = json.dumps({"model": model,
                       "messages": [
                           {"role": "system", "content": "You are a test harness. Reply with exactly: OK"},
                           {"role": "user", "content": text + "\n\nReply with exactly: OK"}],
                       "max_tokens": 1, "temperature": 0, "stream": False}).encode()
    req = urllib.request.Request(GROPIUS, data=body, headers={"Content-Type": "application/json"})
    start = time.time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            data = json.loads(resp.read())
    except urllib.error.HTTPError as e:
        return {"ok": False, "error": f"HTTP {e.code}: {e.read().decode('utf-8', 'replace')[:200]}",
                "total_s": round(time.time() - start, 1)}
    except Exception as e:
        return {"ok": False, "error": f"{type(e).__name__}: {str(e)[:200]}",
                "total_s": round(time.time() - start, 1)}
    total = round(time.time() - start, 1)
    usage = data.get("usage") or {}
    pt = usage.get("prompt_tokens")
    if not pt:
        return {"ok": False, "error": f"200 without usage.prompt_tokens (keys={list(usage.keys())})",
                "total_s": total}
    return {"ok": True, "prompt_tokens": pt, "prefill_s": total, "total_s": total}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--cap", type=int, default=131072)
    ap.add_argument("--floor", type=int, default=1024)
    ap.add_argument("--timeout", type=int, default=900)
    ap.add_argument("--models", default="")
    args = ap.parse_args()

    models = ([m.strip() for m in args.models.split(",") if m.strip()]
              or ["mlx-community/Qwen3-Coder-Next-4bit",
                  "mlx-community/NVIDIA-Nemotron-3.5-Lightning-30B-A3B-4bit",
                  "mlx-community/GLM-4.7-Flash-8bit",
                  "mlx-community/Qwen3.8-27B-8bit"])

    results = {}
    for m in models:
        short = m.split("/")[-1]
        print(f"\n=== {short} ===", flush=True)
        rec = {"probes": []}

        # calibration: small probe to measure chars-per-token
        base = probe(m, filler_tokens(args.floor, 4.0), args.timeout)
        if not base["ok"]:
            print(f"  floor probe FAILED: {base.get('error')}")
            results[m] = {"error": base.get("error"), "probes": [base]}
            continue
        chars_per_token = len(filler_tokens(args.floor, 4.0)) / base["prompt_tokens"]
        rec["load_first_call_s"] = base["total_s"]
        print(f"  floor {base['prompt_tokens']} tokens OK ({base['total_s']}s, load included), "
              f"chars/token≈{chars_per_token:.2f}", flush=True)

        lo, lo_pt = args.floor, base["prompt_tokens"]
        hi, hi_err = args.cap, None
        while hi - lo > max(1024, lo // 8):
            mid = (lo + hi) // 2
            r = probe(m, filler_tokens(mid, chars_per_token), args.timeout)
            r["target"] = mid
            rec["probes"].append(r)
            if r["ok"]:
                lo, lo_pt = mid, r["prompt_tokens"]
                print(f"  ~{mid//1024}K -> OK, actual {r['prompt_tokens']} tokens, prefill {r['total_s']}s", flush=True)
            else:
                hi, hi_err = mid, r.get("error")
                print(f"  ~{mid//1024}K -> FAIL ({r.get('error', '')[:80]}), {r['total_s']}s", flush=True)
                # a crashed child needs a moment; next probe reloads the model
                time.sleep(3)

        rec["max_verified_prompt_tokens"] = lo_pt if lo_pt > args.floor else None
        rec["max_target_tokens"] = lo
        rec["failure_at_target"] = hi
        rec["failure_error"] = hi_err
        results[m] = rec
        print(f"  => max verified: {lo_pt} prompt tokens (~{lo//1024}K target); "
              f"failure at ~{hi//1024}K: {hi_err or 'cap reached'}", flush=True)
        json.dump(results, open(os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                             "results", "context-probe.json"), "w"), indent=1)
        time.sleep(3)

    print("\n=== summary (max verified prompt tokens incl. template) ===")
    for m, v in results.items():
        if "error" in v:
            print(f"{m.split('/')[-1]}: floor probe failed: {v['error'][:80]}")
        else:
            print(f"{m.split('/')[-1]}: {v['max_verified_prompt_tokens']} tokens "
                  f"(cap {v.get('failure_at_target', 'not reached')})")


if __name__ == "__main__":
    main()
