"""Writer/reviewer replication of task 04-algorithms through Gropius, with full timing.

Cast: Qwen3-Coder-Next-4bit writes (agent, via opencode), Qwen3.8-27B-8bit reviews
(one-shot, direct API). One revise round. Every phase is timed; the reviewer's
TTFT includes the model swap, since Gropius is single-model-resident.

Usage: python3 writer_reviewer.py [--task 04-algorithms] [--keep-work]
"""
import argparse, datetime as dt, json, os, shutil, subprocess, sys, time, urllib.request, urllib.error

ROOT = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, ROOT)
from tasks import TASKS

WRITER = ("qwen3-coder-next-4bit-local", "gropius-s/mlx-community/Qwen3-Coder-Next-4bit")
REVIEWER = "mlx-community/Qwen3.8-27B-8bit"
GROPIUS = "http://127.0.0.1:11535/v1/chat/completions"
RUN_TIMEOUT = int(os.environ.get("WR_TIMEOUT", "900"))
API_TIMEOUT = int(os.environ.get("WR_API_TIMEOUT", "600"))

TL = []  # timeline entries
def mark(phase, start, extra=None):
    e = {"phase": phase, "start": start, "end": time.time(),
         "duration_s": round(time.time() - start, 1)}
    if extra:
        e.update(extra)
    TL.append(e)
    print(f"[{dt.datetime.now().strftime('%H:%M:%S')}] {phase}: {e['duration_s']}s"
          + (f" {extra}" if extra else ""), flush=True)
    return e


def chat(model, messages, max_tokens, stream=True):
    """POST to Gropius; returns (text, ttft, total). Streaming to capture TTFT."""
    body = json.dumps({"model": model, "messages": messages,
                       "max_tokens": max_tokens, "temperature": 0, "stream": stream}).encode()
    req = urllib.request.Request(GROPIUS, data=body,
                                 headers={"Content-Type": "application/json"})
    ttft = None
    chunks = []
    think = []
    start = time.time()
    try:
        with urllib.request.urlopen(req, timeout=API_TIMEOUT) as resp:
            if not stream:
                data = json.loads(resp.read())
                return data["choices"][0]["message"]["content"], None, round(time.time() - start, 1)
            for raw in resp:
                line = raw.decode("utf-8", "replace").strip()
                if not line.startswith("data: "):
                    continue
                payload = line[6:]
                if payload == "[DONE]":
                    break
                try:
                    d = json.loads(payload)["choices"][0].get("delta", {})
                except (KeyError, IndexError, json.JSONDecodeError):
                    d = {}
                piece = d.get("content")
                if piece:
                    if ttft is None:
                        ttft = round(time.time() - start, 1)
                    chunks.append(piece)
                # Qwen3.8 emits thinking as delta.reasoning; some servers use
                # delta.reasoning_content. Keepalives (": keepalive n/m") are
                # the server stalling while weights load — count them as a
                # swap estimate.
                rp = d.get("reasoning") or d.get("reasoning_content")
                if rp:
                    if ttft is None:
                        ttft = round(time.time() - start, 1)
                    think.append(rp)
    except urllib.error.HTTPError as e:
        raise RuntimeError(f"HTTP {e.code}: {e.read().decode('utf-8', 'replace')[:300]}") from None
    text = "".join(chunks)
    if not text and think:
        text = "[only reasoning content was emitted]\n" + "".join(think)
    return text, ttft, round(time.time() - start, 1)


def opencode_run(model_id, work, msg, log_path):
    env = dict(os.environ, OPENCODE_CONFIG=os.path.join(ROOT, "opencode.bench-local.jsonc"))
    start = time.time()
    with open(log_path, "w") as lf:
        try:
            p = subprocess.run(["opencode", "run", "-m", model_id, "--auto", "--dir", work, msg],
                               cwd=work, stdout=lf, stderr=subprocess.STDOUT,
                               timeout=RUN_TIMEOUT, text=True, env=env)
            exit_code = p.returncode
            timed_out = False
        except subprocess.TimeoutExpired:
            exit_code, timed_out = -9, True
    return round(time.time() - start, 1), exit_code, timed_out


def run_checker(task_name, work, seed_dir, port):
    checker = os.path.join(ROOT, "checks_src", task_name, "checker.py")
    p = subprocess.run([sys.executable, checker, work, seed_dir, str(port)],
                       capture_output=True, text=True, timeout=180)
    return json.loads(p.stdout)


def unittest_output(work):
    p = subprocess.run([sys.executable, "-m", "unittest", "discover", "-v"], cwd=work,
                       capture_output=True, text=True, timeout=120)
    return p.stderr[-4000:]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--task", default="04-algorithms")
    ap.add_argument("--keep-work", action="store_true")
    ap.add_argument("--review-only", action="store_true",
                    help="reuse the existing workdir; re-run only review + final check")
    args = ap.parse_args()

    task = TASKS[args.task]
    work = os.path.join(ROOT, "results", "work", "writer-reviewer", args.task)
    seed_dir = os.path.join(ROOT, "results", "seeds", args.task)
    if args.review_only and os.path.exists(work):
        print(f"[review-only] reusing workdir {work}", flush=True)
    else:
        if os.path.exists(work):
            shutil.rmtree(work)
        os.makedirs(work)
        os.makedirs(seed_dir, exist_ok=True)
        for path, content in task["files"].items():
            fp = os.path.join(seed_dir, path)
            os.makedirs(os.path.dirname(fp), exist_ok=True)
            if not os.path.exists(fp):
                with open(fp, "w") as f:
                    f.write(content)
            shutil.copy(fp, os.path.join(work, path))
        with open(os.path.join(work, "PROMPT.md"), "w") as f:
            f.write(task["prompt"])
    logs = os.path.join(ROOT, "results", "logs")
    os.makedirs(logs, exist_ok=True)
    state = {}

    wall_start = time.time()

    out = os.path.join(ROOT, "results", "work", "writer-reviewer", "state.json")
    if args.review_only and os.path.exists(out):
        with open(out) as f:
            state = json.load(f)
    else:
        # 1. Writer run (agent). Swap to Coder-Next happens inside this call if needed.
        msg = ("Read PROMPT.md in the current directory and complete the task it describes. "
               "Work only inside the current directory. Do not create README or extra docs.")
        d, code, to = opencode_run(WRITER[1], work, msg, os.path.join(logs, "wr--writer.log"))
        state["writer_run"] = {"duration_s": d, "exit_code": code, "timed_out": to}
        mark("1_writer_run", wall_start, state["writer_run"])

        # 2. Intermediate check + test output for the reviewer
        t = time.time()
        v1 = run_checker(args.task, work, seed_dir, 21301)
        mark("2_writer_check", t, {"score": v1.get("score")})
        state["writer_check"] = v1

    tests_out = unittest_output(work)

    # 3. Reviewer one-shot (swap to Qwen3.8-27B-8bit happens here)
    # 3a. swap probe: a tiny request that only measures load-from-disk + first token
    t = time.time()
    _, swap_ttft, swap_total = chat(REVIEWER, [{"role": "user", "content": "Reply with OK."}], 8)
    mark("3a_swap_probe", t, {"ttft_s": swap_ttft, "total_s": swap_total})
    state["swap_probe"] = {"ttft_s": swap_ttft, "total_s": swap_total}
    with open(os.path.join(work, "diff_lib.py")) as f:
        impl = f.read()
    review_prompt = f"""You are reviewing a Python implementation against its specification.

The file `diff_lib.py` below was written to satisfy the docstrings inside it, so that the
test suite `test_diff_lib.py` passes. The current unittest output follows.

Your job: verify each function/class against its docstring, and list every defect that
would fail a test, with the exact fix required. If a function is correct, say so in one
line. Be concrete and terse: cite the function, the defect, the fix. End with a line
"VERDICT: PASS" if all tests would pass, else "VERDICT: FIX".

--- diff_lib.py ---
{impl}
--- end diff_lib.py ---

--- unittest output (tail) ---
{tests_out}
--- end unittest output ---"""
    t = time.time()
    review, ttft, total = chat(REVIEWER, [{"role": "user", "content": review_prompt}], 8192)
    mark("3_review", t, {"ttft_s": ttft, "total_s": total, "chars": len(review)})
    state["review"] = {"ttft_s": ttft, "total_s": total, "text": review}
    with open(os.path.join(ROOT, "results", "work", "writer-reviewer", "review.txt"), "w") as f:
        f.write(review)
    with open(os.path.join(work, "REVIEW.md"), "w") as f:
        f.write("A reviewer examined your implementation of the PROMPT.md task against the "
                "docstrings and the test suite. Their findings:\n\n" + review +
                "\n\nApply every valid fix. Do not modify test_diff_lib.py.")

    if not args.review_only:
        # 4. Revise round (swap back to Coder-Next happens here)
        t = time.time()
        d, code, to = opencode_run(WRITER[1], work,
                                   "Read REVIEW.md in the current directory. It contains a reviewer's "
                                   "findings on your implementation of the task in PROMPT.md. Apply the "
                                   "valid fixes so all tests pass. Work only inside the current directory.",
                                   os.path.join(logs, "wr--revise.log"))
        state["revise_run"] = {"duration_s": d, "exit_code": code, "timed_out": to}
        mark("4_revise_run", t, state["revise_run"])

        # 5. Final check
        t = time.time()
        v2 = run_checker(args.task, work, seed_dir, 21302)
        mark("5_final_check", t, {"score": v2.get("score")})
        state["final_check"] = v2

    state["total_wall_s"] = round(time.time() - wall_start, 1)
    state["timeline"] = TL
    with open(out, "w") as f:
        json.dump(state, f, indent=1)

    print(f"\n=== writer/reviewer {args.task} ===")
    if "writer_check" in state:
        print(f"writer check : {state['writer_check'].get('score')}/{state['writer_check'].get('max')}")
    print(f"review ttft  : {ttft}s (includes swap to {REVIEWER})")
    print(f"review chars : {len(review)}")
    if "final_check" in state:
        print(f"final check  : {state['final_check'].get('score')}/{state['final_check'].get('max')}")
    print(f"total wall   : {state['total_wall_s']}s")
    print(f"state        : {out}")


if __name__ == "__main__":
    main()
