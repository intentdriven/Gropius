"""Probe per-request temperature/seed behaviour for each Gropius model.

Gropius passes the request body to mlx_lm.server untouched (only the "model"
field is rewritten), and no sampler flags are set at launch, so whatever we
see here is per-model mlx-lm behaviour, not Gropius policy.

Per model, 5 small requests:
  A1, A2 : temperature 0           -> deterministic?
  B1, B2 : temperature 0.7 seed 42 -> seed honoured (identical outputs)?
  B3     : temperature 0.7 seed 999-> differs from B1 (temperature reaches the model)?

Usage: python3 sampling_probe.py [--models id,id,...]
"""
import argparse, json, os, sys, time, urllib.request, urllib.error

GROPIUS = "http://127.0.0.1:11535/v1/chat/completions"
PROMPT = "List eight arbitrary English words, one per line. No numbering, no commentary."
MODELS = [
    "mlx-community/Qwen3-Coder-Next-4bit",
    "mlx-community/NVIDIA-Nemotron-3.5-Lightning-30B-A3B-4bit",
    "mlx-community/GLM-4.7-Flash-8bit",
    "mlx-community/Qwen3.8-27B-8bit",
]


def call(model, temperature, seed, max_tokens=64):
    body = {"model": model, "messages": [{"role": "user", "content": PROMPT}],
            "max_tokens": max_tokens, "temperature": temperature, "stream": False}
    if seed is not None:
        body["seed"] = seed
    req = urllib.request.Request(GROPIUS, data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json"})
    start = time.time()
    try:
        with urllib.request.urlopen(req, timeout=600) as resp:
            data = json.loads(resp.read())
    except urllib.error.HTTPError as e:
        return {"error": f"HTTP {e.code}: {e.read().decode('utf-8', 'replace')[:200]}", "total_s": round(time.time() - start, 1)}
    except Exception as e:
        return {"error": f"{type(e).__name__}: {e}", "total_s": round(time.time() - start, 1)}
    msg = data["choices"][0].get("message", {})
    text = msg.get("content") or msg.get("reasoning_content") or msg.get("reasoning") or ""
    usage = data.get("usage", {})
    return {"text": text.strip(), "ttft_total_s": round(time.time() - start, 1),
            "completion_tokens": usage.get("completion_tokens"),
            "finish": data["choices"][0].get("finish_reason"),
            "msg_keys": list(msg.keys())}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--models", default=",".join(MODELS))
    args = ap.parse_args()
    models = [m.strip() for m in args.models.split(",") if m.strip()]

    results = {}
    for m in models:
        print(f"\n=== {m} ===", flush=True)
        r = {}
        r["A1_temp0"] = call(m, 0.0, None)
        r["A2_temp0"] = call(m, 0.0, None)
        r["B1_seed42"] = call(m, 0.7, 42)
        r["B2_seed42"] = call(m, 0.7, 42)
        r["B3_seed999"] = call(m, 0.7, 999)
        r["B4_temp15"] = call(m, 1.5, None)

        errs = [k for k, v in r.items() if "error" in v]
        if errs:
            print(f"  errors: {errs} -> {r[errs[0]].get('error', '')[:120]}")
            results[m] = {"errors": errs, "detail": r}
            continue

        det = r["A1_temp0"]["text"] == r["A2_temp0"]["text"]
        seed_ok = r["B1_seed42"]["text"] == r["B2_seed42"]["text"]
        seed_diff = r["B3_seed999"]["text"] != r["B1_seed42"]["text"]
        temp_eff = r["B4_temp15"]["text"] != r["B1_seed42"]["text"]
        print(f"  load+first call : {r['A1_temp0']['ttft_total_s']}s")
        print(f"  temp=0 twice identical      : {det}")
        print(f"  temp=0.7 twice identical    : {seed_ok}")
        print(f"  seed=999 differs from seed42: {seed_diff}"
              f"  -> seed parameter {'honoured' if (seed_ok and seed_diff) else 'IGNORED' if seed_ok else 'inconclusive'}")
        print(f"  temp=1.5 differs from 0.7   : {temp_eff}  (temperature reaches the model: {temp_eff})")
        print(f"  B1 head: {r['B1_seed42']['text'][:60]!r}")
        print(f"  B4 head: {r['B4_temp15']['text'][:60]!r}")
        results[m] = {"temp0_deterministic": det, "seed_honoured": seed_ok,
                      "seed_differentiates": seed_diff, "temperature_effective": temp_eff,
                      "load_first_call_s": r["A1_temp0"]["ttft_total_s"],
                      "detail": r}
        json.dump(results, open(os.path.join(os.path.dirname(__file__), "results", "sampling-probe.json"), "w"), indent=1)

    print("\n=== summary ===")
    for m, v in results.items():
        if "errors" in v:
            print(f"{m}: ERRORS {v['errors']}")
        else:
            print(f"{m}: temp0_det={v['temp0_deterministic']} seed_honoured={v['seed_honoured']} seed_diff={v['seed_differentiates']} temp_effective={v['temperature_effective']}")


if __name__ == "__main__":
    main()
