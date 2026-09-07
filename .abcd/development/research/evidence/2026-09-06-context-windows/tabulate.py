"""Render the campaign's result JSONs as the markdown tables for the note."""
import glob
import json
import os
import sys

GB = 2 ** 30
ORDER = ["Qwen3-Coder-Next-4bit", "NVIDIA-Nemotron-3.5-Lightning-30B-A3B-4bit",
         "GLM-4.7-Flash-8bit", "Qwen3.8-27B-8bit"]
SHORT = {"Qwen3-Coder-Next-4bit": "Qwen3-Coder-Next 4bit",
         "NVIDIA-Nemotron-3.5-Lightning-30B-A3B-4bit": "Nemotron-3.5-Lightning 4bit",
         "GLM-4.7-Flash-8bit": "GLM-4.7-Flash 8bit",
         "Qwen3.8-27B-8bit": "Qwen3.8-27B 8bit"}


def load(d):
    recs = {}
    for f in glob.glob(os.path.join(d, "*.json")):
        r = json.load(open(f))
        recs[os.path.basename(f)[:-5]] = r
    return recs


def gb(b):
    return "n/a" if b is None else f"{b / GB:.1f}"


def main():
    recs = load(sys.argv[1] if len(sys.argv) > 1 else "results")
    print("## Verified window\n")
    print("| Model | Largest verified prompt | Target | Outcome |\n| --- | --- | --- | --- |")
    for k in ORDER:
        r = recs.get(k)
        if not r or "verified" not in r:
            continue
        v = r["verified"]
        out = "no failure at any target sent" if v["first_failing_target"] is None else \
            f"failed at {v['first_failing_target']:,} target: {(v['failure_error'] or '')[:60]}"
        if r.get("sweep_note"):
            out += f"; {r['sweep_note']}"
        if v.get("stopped_on_time_budget"):
            out = "stopped on the time budget"
        print(f"| {SHORT[k]} | {v['max_verified_prompt_tokens']:,} | {v['max_ok_target']:,} | {out} |")

    print("\n## Prefill\n")
    print("| Model | Prompt tokens | Prefill s | Tokens per s | Load avg |\n| --- | --- | --- | --- | --- |")
    for k in ORDER:
        r = recs.get(k)
        if not r:
            continue
        for p in r.get("sweep", []):
            if p.get("skipped"):
                print(f"| {SHORT[k]} | target {p['target']:,} | skipped | | {p.get('error', '')[:70]} |")
            elif p["ok"]:
                print(f"| {SHORT[k]} | {p['prompt_tokens']:,} | {p['total_s']} | {p['tokens_per_s']:,} | {p['load_average']} |")
            else:
                print(f"| {SHORT[k]} | target {p['target']:,} | {p['total_s']} | fail | {p.get('error', '')[:60]} |")

    print("\n## Memory\n")
    print("| Model | Prompt tokens | Process before GB | Peak GB | After GB | Growth GB | GB per 1K tokens | System free drop GB | Swap grew |")
    print("| --- | --- | --- | --- | --- | --- | --- | --- | --- |")
    for k in ORDER:
        r = recs.get(k)
        if not r:
            continue
        for p in r.get("sweep", []):
            if not p.get("ok"):
                continue
            m = p["memory"]
            if m.get("top_peak") is None or m.get("top_before") is None:
                continue
            growth = m["top_peak"] - m["top_before"]
            per_k = growth / p["prompt_tokens"] * 1000 / GB
            drop = (m["sys_free_before"] - m["sys_free_min"]) / GB if m.get("sys_free_min") else None
            print(f"| {SHORT[k]} | {p['prompt_tokens']:,} | {gb(m['top_before'])} | {gb(m['top_peak'])} | "
                  f"{gb(m['top_after'])} | {growth / GB:.1f} | {per_k:.3f} | "
                  f"{'n/a' if drop is None else f'{drop:.1f}'} | {'yes' if m.get('swap_grew') else 'no'} |")

    print("\n## Needle\n")
    print("| Model | Prompt tokens | Depth | Recall | Finish | Seconds |\n| --- | --- | --- | --- | --- | --- |")
    for k in ORDER:
        r = recs.get(k)
        if not r:
            continue
        for n in r.get("needle", []):
            rec = "hit" if n["hit"] else ("in reasoning only" if n["hit_in_reasoning"] else "miss")
            if not n["ok"]:
                rec = f"error: {(n.get('error') or '')[:50]}"
            print(f"| {SHORT[k]} | {n.get('prompt_tokens') or n['target']:,} | {int(n['depth'] * 100)}% | {rec} | "
                  f"{n.get('finish_reason')} | {n['total_s']} |")

    print("\n## Concurrency\n")
    for k in ORDER:
        r = recs.get(k)
        if not r or "concurrency" not in r:
            continue
        c = r["concurrency"]
        s = c["single"]
        print(f"{SHORT[k]} single streaming: {s.get('prompt_tokens'):,} tokens, first token {s.get('first_delta_s')} s, "
              f"total {s['total_s']} s, process {gb(s['memory']['top_before'])} -> {gb(s['memory']['top_peak'])} GB")
        for ctl in r.get("stream_control", []):
            print(f"  control {ctl['mode']}: {ctl.get('prompt_tokens'):,} tokens, first token {ctl.get('first_delta_s')} s, "
                  f"total {ctl['total_s']} s, load {ctl['load_average']}")
        cc = c["concurrent"]
        for q in cc["requests"]:
            print(f"  concurrent x{cc['n']}: {q.get('prompt_tokens'):,} tokens, first token {q.get('first_delta_s')} s, "
                  f"total {q['total_s']} s, error {q.get('error')}")
        m = cc["memory"]
        print(f"  wall {cc['wall_s']} s, process {gb(m['top_before'])} -> {gb(m['top_peak'])} GB (after {gb(m['top_after'])}), "
              f"system free drop {(m['sys_free_before'] - m['sys_free_min']) / GB:.1f} GB, "
              f"swapouts {m['swapouts_after'] - m['swapouts_before']}, load {cc['load_average']}")


if __name__ == "__main__":
    main()


def fits(d="results"):
    """Least-squares growth line per model over clean probes (no note field)."""
    recs = load(d)
    print("\n## Memory fit (clean probes only)\n")
    print("| Model | Points | Intercept GB | GB per 1K tokens | KB per token |\n| --- | --- | --- | --- | --- |")
    for k in ORDER:
        r = recs.get(k)
        if not r:
            continue
        pts = [(p["prompt_tokens"], p["memory"]["top_peak"] - p["memory"]["top_before"])
               for p in r.get("sweep", []) if p.get("ok") and not p.get("note")
               and (p.get("memory") or {}).get("top_peak") and p["memory"].get("top_before")]
        if len(pts) < 2:
            continue
        n = len(pts)
        mx = sum(x for x, _ in pts) / n
        my = sum(y for _, y in pts) / n
        sxx = sum((x - mx) ** 2 for x, _ in pts)
        slope = sum((x - mx) * (y - my) for x, y in pts) / sxx
        icpt = my - slope * mx
        print(f"| {SHORT[k]} | {n} | {icpt / GB:.1f} | {slope * 1000 / GB:.3f} | {slope / 1024:.0f} |")


if __name__ == "__main__" and "--fit" in sys.argv:
    fits(sys.argv[1])
