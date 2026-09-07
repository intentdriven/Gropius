"""Context-window measurement campaign for Gropius models (2026-09-06).

Every request goes through the Gropius gateway on its default port, so the
numbers are what a client sees, including the gateway's fixed ten-minute
upstream response-header timeout (a request that dies at 600 s died there).

Subcommands (one model at a time; run at low system load):

  load     -- ask the control panel to load the model, wait until resident
  sweep    -- fixed prompt sizes, then the nominal cap, then a bisection if
              the cap fails; one output token, temperature 0; prompt size
              read from usage.prompt_tokens; per-probe memory samples
  needle   -- needle-in-a-haystack recall at three depths and the given sizes
  stream-control -- one streaming, one plain and one streaming request of a
              size, for first-token time against non-streaming wall time
  concurrency -- one streaming request, then N concurrent, first-token time
  unload   -- ask the control panel to unload the model

Prompts are synthetic and generated here from the filler and needle strings
below; the results carry no prompt text and not the needle's value, only
whether the answer contained it.
Every prompt begins with a unique nonce and uses a per-request marker order,
so no two requests share a prefix and the model server's prompt cache cannot
shorten a later prefill.

Memory is sampled about every three to four seconds, two ways, because the model server runs under a different
account and only `top` can read its footprint: `top` reports the process's
memory (MEM column, integer gigabytes above 10 GB, so +-0.5 GB), and
`vm_stat` reports system-wide free pages at page precision (noisy when other
work runs on the machine). Both are recorded raw.

Usage: python3 context_windows_probe.py <subcommand> --model <repo id> [options]
"""
import argparse
import json
import os
import random
import re
import subprocess

import threading
import time
import urllib.error
import urllib.request

GATEWAY = "http://127.0.0.1:11535"
CHAT = GATEWAY + "/v1/chat/completions"

_BLOCK = ("The lighthouse keeper counted the waves as they broke against the seawall, "
          "marking each seventh one in a small leather notebook. {} "
          "Morning fog delayed the ferry, and the gulls circled the harbour in slow, "
          "patient spirals while the fishermen mended their nets on the pier. ")
_MARKERS = ["Nearby,", "Meanwhile,", "Later,", "Somehow,", "Quietly,", "Often,", "Yesterday,",
            "Today,", "Besides,", "However,", "Beyond,", "Above,", "Below,", "Perhaps,",
            "Certainly,", "Suddenly,", "Gradually,", "Finally,"]

NEEDLE_FACT = "The access code for the harbour vault is 7391-KESTREL-42."
NEEDLE_ANSWER = "7391-KESTREL-42"
NEEDLE_QUESTION = ("What is the access code for the harbour vault mentioned in the document? "
                   "Reply with the code only, no explanation.")


# ----------------------------------------------------------------- prompts

def filler_blocks(target_tokens, chars_per_token, seed):
    """Filler of roughly target_tokens tokens as a list of blocks, unique per seed."""
    rng = random.Random(seed)
    n = max(1, int(target_tokens * chars_per_token / len(_BLOCK)))
    markers = list(_MARKERS)
    blocks = []
    for i in range(n):
        if i % len(markers) == 0:
            rng.shuffle(markers)
        blocks.append(_BLOCK.format(markers[i % len(markers)]))
    return blocks


def nonce_header(seed):
    return f"Document {seed:08x}-{random.Random(seed ^ 0x5A5A).randint(10 ** 7, 10 ** 8 - 1)}.\n\n"


def plain_prompt(target_tokens, chars_per_token, seed):
    return nonce_header(seed) + "".join(filler_blocks(target_tokens, chars_per_token, seed))


def needle_prompt(target_tokens, chars_per_token, seed, depth):
    blocks = filler_blocks(target_tokens, chars_per_token, seed)
    at = min(len(blocks) - 1, max(0, int(len(blocks) * depth)))
    blocks.insert(at, NEEDLE_FACT + " ")
    return nonce_header(seed) + "".join(blocks)


# ----------------------------------------------------------------- server

def api(path, body=None, timeout=30):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(GATEWAY + path, data=data,
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read())


def resident(model):
    for r in api("/api/state").get("resident", []):
        if r["repo_id"] == model:
            return r
    return None


def server_pid(model):
    out = subprocess.run(["ps", "-axo", "pid,command"], capture_output=True, text=True).stdout
    for line in out.splitlines():
        if "mlx_lm server" in line and model in line:
            return int(line.split()[0])
    return None


def load_model(model, timeout=900):
    start = time.time()
    if resident(model):
        return {"already_resident": True, "load_s": 0.0}
    api("/api/models/load", {"model": model})
    while time.time() - start < timeout:
        if resident(model):
            break
        time.sleep(2)
    else:
        raise SystemExit(f"model did not become resident within {timeout}s")
    # the pool lists the entry before the weights are in memory; a tiny request
    # returns only once the server answers, so its time is the load time
    warm = chat(model, plain_messages("Warm-up."), 1, timeout)
    if not warm["ok"]:
        raise SystemExit(f"warm-up failed: {warm['error']}")
    return {"already_resident": False, "load_s": round(time.time() - start, 1)}


def reload_model(model):
    """Unload and load again: a clean baseline, and it kills any prefill the
    server is still running for a request the gateway already gave up on."""
    for _ in range(30):
        try:
            api("/api/models/unload", {"model": model})
            break
        except urllib.error.HTTPError as e:
            if e.code != 409:
                raise
            time.sleep(10)  # the pool still counts a request in flight
    time.sleep(5)
    return load_model(model)


# ----------------------------------------------------------------- memory

_UNITS = {"K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40, "B": 1}


def top_mem_bytes(pid):
    """Process footprint as `top` reports it (integer G above 10 GB)."""
    if pid is None:
        return None
    out = subprocess.run(["top", "-l", "1", "-stats", "pid,mem", "-pid", str(pid)],
                         capture_output=True, text=True).stdout
    for line in out.splitlines():
        parts = line.split()
        if len(parts) >= 2 and parts[0] == str(pid):
            m = re.match(r"([\d.]+)([KMGTB]?)", parts[1])
            if m:
                return int(float(m.group(1)) * _UNITS.get(m.group(2) or "B", 1))
    return None


def vm_stat():
    out = subprocess.run(["vm_stat"], capture_output=True, text=True).stdout
    page = int(re.search(r"page size of (\d+)", out).group(1))
    fields = {}
    for line in out.splitlines()[1:]:
        if ":" in line:
            k, v = line.split(":", 1)
            v = v.strip().rstrip(".")
            if v.isdigit():
                fields[k.strip()] = int(v)
    free = (fields.get("Pages free", 0) + fields.get("Pages speculative", 0)) * page
    return {"free_bytes": free, "available_bytes": available_bytes(),
            "wired_bytes": fields.get("Pages wired down", 0) * page,
            "active_bytes": fields.get("Pages active", 0) * page,
            "compressor_bytes": fields.get("Pages occupied by compressor", 0) * page,
            "swapouts": fields.get("Swapouts", 0)}


def available_bytes():
    """Memory the kernel counts as available (the figure `memory_pressure`
    prints as the free percentage), including reclaimable file cache."""
    levels = []
    for _ in range(3):
        levels.append(int(subprocess.run(["sysctl", "-n", "kern.memorystatus_level"],
                                         capture_output=True, text=True).stdout.strip() or 0))
        time.sleep(1)
    level = sorted(levels)[1]
    total = int(subprocess.run(["sysctl", "-n", "hw.memsize"],
                               capture_output=True, text=True).stdout.strip() or 0)
    return total * level // 100


def load_average():
    return round(os.getloadavg()[0], 2)


class MemorySampler(threading.Thread):
    """Samples `top` and `vm_stat` until stopped: one sample per `interval`
    seconds at best, in practice every three to four seconds because a sample
    itself takes about three seconds (three `sysctl` reads a second apart)."""

    def __init__(self, pid, interval=3.0):
        super().__init__(daemon=True)
        self.pid, self.interval = pid, interval
        self.samples = []
        self._stop = threading.Event()

    def run(self):
        while not self._stop.is_set():
            t = time.time()
            self.samples.append({"t": round(t, 1), "top": top_mem_bytes(self.pid), "vm": vm_stat()})
            self._stop.wait(max(0.0, self.interval - (time.time() - t)))

    def stop(self):
        self._stop.set()
        self.join()
        return self.samples


def summarise(before, samples, after):
    def peak(key):
        vals = [s[key] for s in samples if s.get(key) is not None] if key == "top" else \
               [s["vm"]["free_bytes"] for s in samples]
        if not vals:
            return None
        return max(vals) if key == "top" else min(vals)
    return {
        "top_before": before["top"], "top_peak": peak("top"), "top_after": after["top"],
        "sys_free_before": before["vm"]["free_bytes"], "sys_free_min": peak("vm"),
        "sys_free_after": after["vm"]["free_bytes"],
        "swapouts_before": before["vm"]["swapouts"], "swapouts_after": after["vm"]["swapouts"],
        "compressor_before": before["vm"]["compressor_bytes"],
        "compressor_after": after["vm"]["compressor_bytes"],
        "samples": len(samples),
    }


def snapshot(pid):
    return {"top": top_mem_bytes(pid), "vm": vm_stat()}


# ----------------------------------------------------------------- requests

def chat(model, messages, max_tokens, timeout, extra=None):
    body = json.dumps({"model": model, "messages": messages, "max_tokens": max_tokens,
                       "temperature": 0, "stream": False, **(extra or {})}).encode()
    req = urllib.request.Request(CHAT, data=body, headers={"Content-Type": "application/json"})
    start = time.time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            data = json.loads(resp.read())
    except urllib.error.HTTPError as e:
        return {"ok": False, "error": f"HTTP {e.code}: {e.read().decode('utf-8', 'replace')[:200]}",
                "total_s": round(time.time() - start, 1)}
    except Exception as e:  # noqa: BLE001 - every failure mode is a result here
        return {"ok": False, "error": f"{type(e).__name__}: {str(e)[:200]}",
                "total_s": round(time.time() - start, 1)}
    total = round(time.time() - start, 1)
    usage = data.get("usage") or {}
    msg = (data.get("choices") or [{}])[0].get("message") or {}
    return {"ok": True, "total_s": total, "prompt_tokens": usage.get("prompt_tokens"),
            "completion_tokens": usage.get("completion_tokens"),
            "content": msg.get("content") or "",
            "reasoning": msg.get("reasoning") or msg.get("reasoning_content") or "",
            "finish_reason": (data.get("choices") or [{}])[0].get("finish_reason")}


def chat_stream(model, messages, max_tokens, timeout):
    """Streaming request; returns first-delta, first-content and total times."""
    body = json.dumps({"model": model, "messages": messages, "max_tokens": max_tokens,
                       "temperature": 0, "stream": True,
                       "stream_options": {"include_usage": True}}).encode()
    req = urllib.request.Request(CHAT, data=body, headers={"Content-Type": "application/json"})
    start = time.time()
    first_delta = first_content = None
    usage = None
    content = ""
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for raw in resp:
                line = raw.decode("utf-8", "replace").strip()
                if not line.startswith("data:"):
                    continue
                payload = line[5:].strip()
                if payload == "[DONE]":
                    break
                try:
                    obj = json.loads(payload)
                except ValueError:
                    continue
                if obj.get("usage"):
                    usage = obj["usage"]
                for ch in obj.get("choices") or []:
                    d = ch.get("delta") or {}
                    if d and first_delta is None:
                        first_delta = round(time.time() - start, 2)
                    if d.get("content"):
                        content += d["content"]
                        if first_content is None:
                            first_content = round(time.time() - start, 2)
    except urllib.error.HTTPError as e:
        return {"ok": False, "error": f"HTTP {e.code}: {e.read().decode('utf-8', 'replace')[:200]}",
                "total_s": round(time.time() - start, 1)}
    except Exception as e:  # noqa: BLE001
        return {"ok": False, "error": f"{type(e).__name__}: {str(e)[:200]}",
                "total_s": round(time.time() - start, 1)}
    return {"ok": True, "total_s": round(time.time() - start, 1), "first_delta_s": first_delta,
            "first_content_s": first_content, "prompt_tokens": (usage or {}).get("prompt_tokens"),
            "completion_tokens": (usage or {}).get("completion_tokens"),
            "content_chars": len(content)}


def plain_messages(text):
    return [{"role": "system", "content": "You are a test harness. Reply with exactly: OK"},
            {"role": "user", "content": text + "\n\nReply with exactly: OK"}]


def needle_messages(text):
    return [{"role": "system", "content": "You answer questions about the document exactly."},
            {"role": "user", "content": text + "\n\n" + NEEDLE_QUESTION}]


# ----------------------------------------------------------------- results

def results_path(args, model):
    os.makedirs(args.out, exist_ok=True)
    return os.path.join(args.out, model.split("/")[-1] + ".json")


def read_results(args, model):
    p = results_path(args, model)
    if os.path.exists(p):
        return json.load(open(p))
    return {"model": model}


def write_results(args, model, rec):
    json.dump(rec, open(results_path(args, model), "w"), indent=1)


def calibrate(model, floor, timeout):
    """One small probe: chars-per-token for this model's tokenizer."""
    text = plain_prompt(floor, 4.0, seed=1)
    r = chat(model, plain_messages(text), 1, timeout)
    if not r["ok"]:
        raise SystemExit(f"floor probe failed: {r['error']}")
    return len(text) / r["prompt_tokens"], r


MARGIN_BYTES = 24 << 30  # never plan a probe within this much of the machine's free memory


def projected_growth(probes, target):
    """Bytes the process is expected to grow by for `target` tokens: a least-
    squares line through every successful probe of 16K tokens or more (the
    fixed prefill buffers are the intercept, the per-token cache the slope),
    times 1.5. With fewer than two such probes, the steepest per-token rate
    seen; with none, None."""
    pts = [(p["prompt_tokens"], p["memory"]["top_peak"] - p["memory"]["top_before"])
           for p in probes
           if p.get("ok") and p.get("prompt_tokens") and (p.get("memory") or {}).get("top_peak")
           and p["memory"].get("top_before")]
    if not pts:
        return None
    # a probe that started from a baseline well above the model's clean idle
    # footprint ran on top of a retained prompt cache; its growth is not the
    # cost of its own prompt, so it is left out of the fit
    baseline = min(p["memory"]["top_before"] for p in probes
                   if p.get("ok") and (p.get("memory") or {}).get("top_before"))
    pts = [(p["prompt_tokens"], p["memory"]["top_peak"] - p["memory"]["top_before"])
           for p in probes
           if p.get("ok") and p.get("prompt_tokens") and (p.get("memory") or {}).get("top_peak")
           and p["memory"].get("top_before") and p["memory"]["top_before"] <= baseline * 1.25]
    big = [pt for pt in pts if pt[0] >= 16384]
    if len(big) < 2:
        return max(y / x for x, y in pts) * target * 1.5
    n = len(big)
    mx = sum(x for x, _ in big) / n
    my = sum(y for _, y in big) / n
    sxx = sum((x - mx) ** 2 for x, _ in big)
    slope = max(0.0, sum((x - mx) * (y - my) for x, y in big) / sxx) if sxx else 0.0
    intercept = max(0.0, my - slope * mx)
    return (intercept + slope * target) * 1.5


def memory_guard(pid, probes, target):
    """Refuse a probe whose projected footprint would leave less than MARGIN_BYTES free."""
    now = snapshot(pid)
    growth = projected_growth(probes, target)
    avail = available_bytes()
    if growth is None:
        return None
    total = int(subprocess.run(["sysctl", "-n", "hw.memsize"], capture_output=True, text=True).stdout)
    # the weights are file-backed and count as reclaimable in `avail`, so also
    # require the process's projected footprint to fit the machine outright
    if growth + MARGIN_BYTES > avail or (now["top"] or 0) + growth + MARGIN_BYTES > total:
        return {"ok": False, "skipped": True, "target": target, "total_s": 0.0,
                "error": f"skipped for memory safety: projected growth {growth / 2 ** 30:.1f} GB "
                         f"against {avail / 2 ** 30:.1f} GB available",
                "memory": {"top_before": now["top"], "top_peak": None, "top_after": None,
                           "sys_free_before": now["vm"]["free_bytes"], "available_before": avail}}
    return None


def measured_probe(model, target, cpt, seed, pid, timeout, probes=(), reload=False):
    """One plain probe with memory sampling around it."""
    if reload:
        info = reload_model(model)
        pid = server_pid(model) or pid
        time.sleep(5)
    guard = memory_guard(pid, probes, target)
    if reload and guard:
        guard["reloaded"] = True
    if guard:
        return guard
    text = plain_prompt(target, cpt, seed)
    pid = server_pid(model) or pid  # the server may have been restarted between probes
    before = snapshot(pid)
    sampler = MemorySampler(pid)
    sampler.start()
    r = chat(model, plain_messages(text), 1, timeout)
    samples = sampler.stop()
    time.sleep(5)
    after = snapshot(pid)
    r["target"] = target
    r["server_pid"] = pid
    if reload:
        r["reloaded_before"] = True
        r["reload_s"] = info["load_s"]
    r["load_average"] = load_average()
    r["memory"] = summarise(before, samples, after)
    r["memory"]["swap_grew"] = after["vm"]["swapouts"] - before["vm"]["swapouts"] > 1000
    if r["ok"] and r["prompt_tokens"]:
        r["tokens_per_s"] = round(r["prompt_tokens"] / r["total_s"], 1)
    r.pop("content", None)
    r.pop("reasoning", None)
    return r


# ----------------------------------------------------------------- commands

def cmd_load(args):
    info = load_model(args.model)
    rec = read_results(args, args.model)
    rec["load"] = info
    rec["server_pid_found"] = server_pid(args.model) is not None
    rec["idle_memory"] = snapshot(server_pid(args.model))
    write_results(args, args.model, rec)
    print(json.dumps(info))


def cmd_sweep(args):
    model = args.model
    rec = read_results(args, model)
    pid = server_pid(model)
    cpt, floor = calibrate(model, args.floor, args.timeout)
    rec["chars_per_token"] = round(cpt, 3)
    rec["floor_probe"] = {"prompt_tokens": floor["prompt_tokens"], "total_s": floor["total_s"]}
    print(f"floor {floor['prompt_tokens']} tokens in {floor['total_s']}s; chars/token {cpt:.2f}",
          flush=True)
    probes = rec.setdefault("sweep", [])
    seed = 1000 + len(probes)
    stopped_on_time = False
    for target in [int(x) for x in args.points.split(",") if x]:
        r = measured_probe(model, target, cpt, seed, pid, args.timeout, probes, args.reload)
        seed += 1
        probes.append(r)
        write_results(args, model, rec)
        print(f"  {target:>7} -> {'OK' if r['ok'] else 'FAIL'} "
              f"{r.get('prompt_tokens')} tokens {r['total_s']}s {r.get('tokens_per_s', '')} tok/s "
              f"mem {r['memory']['top_before']}->{r['memory']['top_peak']} "
              f"{r.get('error', '')[:80]}", flush=True)
        if r.get("skipped"):
            break
        if not r["ok"]:
            time.sleep(5)
            if not resident(model):
                print("  model no longer resident; reloading", flush=True)
                load_model(model)
                pid = server_pid(model)
        if args.stop_s and r["total_s"] > args.stop_s:
            stopped_on_time = True
            print(f"  stopping: {r['total_s']}s exceeds --stop-s {args.stop_s}", flush=True)
            break
    ok = [p for p in probes if p["ok"] and p.get("prompt_tokens")]
    lo = max((p["target"] for p in ok), default=args.floor)
    lo_pt = max((p["prompt_tokens"] for p in ok), default=floor["prompt_tokens"])
    hi, hi_err = None, None
    if args.lo and args.hi:
        lo, hi, hi_err = args.lo, args.hi, "bisection resumed from --lo/--hi"
        lo_pt = max((p["prompt_tokens"] for p in ok if p["target"] <= lo), default=lo_pt)
        while hi - lo > args.tolerance:
            mid = (lo + hi) // 2
            r = measured_probe(model, mid, cpt, seed, pid, args.timeout, probes, args.reload)
            seed += 1
            probes.append(r)
            write_results(args, model, rec)
            print(f"  bisect {mid} -> {'OK' if r['ok'] else 'FAIL'} {r.get('prompt_tokens')} "
                  f"tokens {r['total_s']}s {r.get('error', '')[:80]}", flush=True)
            if r["ok"]:
                lo, lo_pt = mid, r["prompt_tokens"]
            else:
                hi, hi_err = mid, r.get("error")
                if not r.get("skipped"):
                    time.sleep(5)
                    if not resident(model):
                        load_model(model)
                        pid = server_pid(model)
            if args.stop_s and r["total_s"] > args.stop_s:
                break
    elif args.cap and not stopped_on_time:
        # the cap first: if it succeeds there is nothing to bisect
        r = measured_probe(model, args.cap, cpt, seed, pid, args.timeout, probes, args.reload)
        seed += 1
        probes.append(r)
        write_results(args, model, rec)
        print(f"  cap {args.cap} -> {'OK' if r['ok'] else 'FAIL'} {r.get('prompt_tokens')} tokens "
              f"{r['total_s']}s {r.get('error', '')[:80]}", flush=True)
        if r.get("skipped"):
            hi, hi_err = args.cap, r["error"]
        elif r["ok"]:
            lo, lo_pt = args.cap, r["prompt_tokens"]
        else:
            hi, hi_err = args.cap, r.get("error")
            if not resident(model):
                load_model(model)
                pid = server_pid(model)
            while hi - lo > args.tolerance:
                mid = (lo + hi) // 2
                r = measured_probe(model, mid, cpt, seed, pid, args.timeout, probes, args.reload)
                seed += 1
                probes.append(r)
                write_results(args, model, rec)
                print(f"  bisect {mid} -> {'OK' if r['ok'] else 'FAIL'} {r.get('prompt_tokens')} "
                      f"tokens {r['total_s']}s {r.get('error', '')[:80]}", flush=True)
                if r["ok"]:
                    lo, lo_pt = mid, r["prompt_tokens"]
                elif r.get("skipped"):
                    hi, hi_err = mid, r.get("error")
                else:
                    hi, hi_err = mid, r.get("error")
                    time.sleep(5)
                    if not resident(model):
                        load_model(model)
                        pid = server_pid(model)
                if args.stop_s and r["total_s"] > args.stop_s:
                    print("  stopping bisection on --stop-s", flush=True)
                    break
    rec["verified"] = {"max_verified_prompt_tokens": lo_pt, "max_ok_target": lo,
                       "first_failing_target": hi, "failure_error": hi_err,
                       "stopped_on_time_budget": stopped_on_time}
    write_results(args, model, rec)
    print(json.dumps(rec["verified"]))


def cmd_needle(args):
    model = args.model
    rec = read_results(args, model)
    cpt = rec.get("chars_per_token") or calibrate(model, args.floor, args.timeout)[0]
    runs = rec.setdefault("needle", [])
    seed = 5000 + len(runs)
    for size in [int(x) for x in args.sizes.split(",") if x]:
        for depth in [float(x) for x in args.depths.split(",") if x]:
            text = needle_prompt(size, cpt, seed, depth)
            seed += 1
            if args.reload and size >= args.reload_above:
                reload_model(model)
                time.sleep(5)
            extra = None if args.thinking else {"chat_template_kwargs": {"enable_thinking": False}}
            r = chat(model, needle_messages(text), args.max_tokens, args.timeout, extra)
            hit = r.get("ok") and NEEDLE_ANSWER in (r.get("content") or "")
            hit_in_reasoning = r.get("ok") and NEEDLE_ANSWER in (r.get("reasoning") or "")
            run = {"target": size, "depth": depth, "thinking_enabled": bool(args.thinking),
                   "reloaded_before": bool(args.reload and size >= args.reload_above), "ok": r.get("ok"), "total_s": r["total_s"],
                   "prompt_tokens": r.get("prompt_tokens"),
                   "completion_tokens": r.get("completion_tokens"),
                   "finish_reason": r.get("finish_reason"), "hit": bool(hit),
                   "hit_in_reasoning": bool(hit_in_reasoning),
                   "content_chars": len(r.get("content") or ""),
                   "reasoning_chars": len(r.get("reasoning") or ""),
                   "error": r.get("error"), "load_average": load_average()}
            runs.append(run)
            write_results(args, model, rec)
            print(f"  {size:>7} @{depth:.1f} -> {'HIT ' if hit else 'miss'} "
                  f"{r.get('prompt_tokens')} tokens {r['total_s']}s "
                  f"fin={r.get('finish_reason')} reasoning={len(r.get('reasoning') or '')}ch "
                  f"{(r.get('content') or r.get('error') or '')[:60]!r}", flush=True)
            if not r.get("ok") and not resident(model):
                load_model(model)


def cmd_concurrency(args):
    model = args.model
    rec = read_results(args, model)
    pid = server_pid(model)
    cpt = rec.get("chars_per_token") or calibrate(model, args.floor, args.timeout)[0]
    out = rec.setdefault("concurrency", {})

    def one(seed):
        return chat_stream(model, plain_messages(plain_prompt(args.size, cpt, seed)),
                           args.max_tokens, args.timeout)

    before = snapshot(pid)
    sampler = MemorySampler(pid)
    sampler.start()
    single = one(7001)
    samples = sampler.stop()
    time.sleep(5)
    single["memory"] = summarise(before, samples, snapshot(pid))
    out["single"] = single
    write_results(args, model, rec)
    print(f"  single: {json.dumps({k: v for k, v in single.items() if k != 'memory'})}", flush=True)
    time.sleep(10)

    results = [None] * args.n
    threads = []

    def worker(i):
        results[i] = one(7100 + i)

    before = snapshot(pid)
    sampler = MemorySampler(pid)
    sampler.start()
    t0 = time.time()
    for i in range(args.n):
        threads.append(threading.Thread(target=worker, args=(i,)))
        threads[-1].start()
    for t in threads:
        t.join()
    wall = round(time.time() - t0, 1)
    samples = sampler.stop()
    time.sleep(5)
    out["concurrent"] = {"n": args.n, "wall_s": wall, "requests": results,
                         "memory": summarise(before, samples, snapshot(pid)),
                         "load_average": load_average()}
    write_results(args, model, rec)
    for r in results:
        print(f"  concurrent: {json.dumps(r)}", flush=True)
    print(f"  wall {wall}s; memory {out['concurrent']['memory']}")


def cmd_stream_control(args):
    """Streaming, plain, streaming at --size: does streaming change the wall
    time, and how repeatable is the single-request first-token figure."""
    model = args.model
    rec = read_results(args, model)
    cpt = rec.get("chars_per_token") or calibrate(model, args.floor, args.timeout)[0]
    out = rec.setdefault("stream_control", [])
    seed = 7200 + len(out)
    for mode in ("stream", "plain", "stream"):
        seed += 1
        msgs = plain_messages(plain_prompt(args.size, cpt, seed))
        if mode == "stream":
            r = chat_stream(model, msgs, args.max_tokens, args.timeout)
        else:
            r = chat(model, msgs, 1, args.timeout)
            r.pop("content", None)
            r.pop("reasoning", None)
        r["mode"] = mode
        r["load_average"] = load_average()
        out.append(r)
        write_results(args, model, rec)
        print(f"  {mode}: {json.dumps({k: r.get(k) for k in ('prompt_tokens', 'first_delta_s', 'total_s', 'error')})}",
              flush=True)
        time.sleep(5)


def cmd_unload(args):
    print(json.dumps(api("/api/models/unload", {"model": args.model})))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("cmd", choices=["load", "sweep", "needle", "concurrency",
                                    "stream-control", "unload"])
    ap.add_argument("--model", required=True)
    ap.add_argument("--out", default="results")
    ap.add_argument("--timeout", type=int, default=900)
    ap.add_argument("--floor", type=int, default=1024)
    ap.add_argument("--points", default="8192,32768,65536,98304")
    ap.add_argument("--cap", type=int, default=0, help="nominal cap target; 0 skips the cap probe")
    ap.add_argument("--tolerance", type=int, default=8192)
    ap.add_argument("--stop-s", type=float, default=0.0,
                    help="stop the sweep once a probe takes longer than this")
    ap.add_argument("--sizes", default="16384,65536")
    ap.add_argument("--depths", default="0.1,0.5,0.9")
    ap.add_argument("--max-tokens", type=int, default=64)
    ap.add_argument("--size", type=int, default=65536)
    ap.add_argument("--n", type=int, default=2)
    ap.add_argument("--reload", action="store_true",
                    help="unload and reload the model before every sweep probe")
    ap.add_argument("--reload-above", type=int, default=100000,
                    help="with --reload, the needle reloads only for prompts this large")
    ap.add_argument("--lo", type=int, default=0, help="bisect from this known-good target ...")
    ap.add_argument("--hi", type=int, default=0, help="... up to this known-failing target")
    ap.add_argument("--thinking", action="store_true",
                    help="leave the chat template's thinking mode on for the needle check")
    args = ap.parse_args()
    {"load": cmd_load, "sweep": cmd_sweep, "needle": cmd_needle,
     "concurrency": cmd_concurrency, "stream-control": cmd_stream_control,
     "unload": cmd_unload}[args.cmd](args)


if __name__ == "__main__":
    main()
