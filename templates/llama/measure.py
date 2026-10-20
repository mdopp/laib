#!/usr/bin/env python3
"""Measure prompt eval (#525): the same ~14k-token prompt, before and after a
presets.json change, and what the llama log says about where the weights sit.

On the box, next to presets.json:
    python3 ~/.config/containers/systemd/llama/measure.py --label before
change presets.json (batch, ubatch, flashAttn, nCpuMoe, ngl, parallel),
apply llama, then
    python3 ~/.config/containers/systemd/llama/measure.py --label after

Each run sends one request with cache_prompt off and max_tokens 1, so the
time is prompt eval, and reads the router's own `timings`. The first run
after a start includes the cold load (wall time); prompt_per_second does
not. The log lines are the last ones of llama.service since the window
given: `offloaded N/M layers to GPU` and the CPU_Mapped buffer say whether
experts sit on the CPU. The last line is JSON, to compare two runs.
"""
import argparse, json, os, re, statistics, subprocess, sys, time, urllib.request

LOG_KEYS = {
    "offloaded": re.compile(r"offloaded (\d+)/(\d+) layers to GPU"),
    "cpu_mapped": re.compile(r"CPU_Mapped model buffer size\s*=\s*([\d.]+ ?MiB)"),
    "cuda_model": re.compile(r"CUDA0 model buffer size\s*=\s*([\d.]+ ?MiB)"),
    "cuda_compute": re.compile(r"CUDA0 compute buffer size\s*=\s*([\d.]+ ?MiB)"),
    "n_batch": re.compile(r"\bn_batch\s*=\s*(\d+)"),
    "n_ubatch": re.compile(r"\bn_ubatch\s*=\s*(\d+)"),
    "flash_attn": re.compile(r"\bflash_attn\s*=\s*(\S+)"),
    "n_seq_max": re.compile(r"\bn_seq_max\s*=\s*(\d+)"),
}


def prompt(tokens):
    """A deterministic prompt of roughly `tokens` tokens (numbers and German: about 1.8 per word); the run reports the real prompt_n."""
    words, i = [], 0
    while len(words) < tokens * 11 // 20:
        words += f"Zeile {i}: Im Haushalt steht Termin {i * 7919 % 1000} am Tag {i % 28 + 1}, Raum {i % 9}.".split()
        i += 1
    return " ".join(words) + "\nAntworte mit einem Wort: ok."


def once(url, model, text, key=""):
    body = json.dumps({"model": model, "max_tokens": 1, "cache_prompt": False,
                       "messages": [{"role": "user", "content": text}]}).encode()
    req = urllib.request.Request(url.rstrip("/") + "/v1/chat/completions", body, {"Content-Type": "application/json"})
    if key:
        req.add_header("Authorization", "Bearer " + key)
    t = time.monotonic()
    with urllib.request.urlopen(req, timeout=900) as r:
        out = json.load(r)
    tm = out.get("timings") or {}
    return {"prompt_n": tm.get("prompt_n"), "prompt_ms": tm.get("prompt_ms"),
            "prompt_per_second": tm.get("prompt_per_second"), "wall_s": round(time.monotonic() - t, 2)}


def from_log(text):
    """The last value of each key in the llama log."""
    out = {}
    for line in text.splitlines():
        for k, rx in LOG_KEYS.items():
            m = rx.search(line)
            if m:
                out[k] = "/".join(m.groups())
    return out


def main(argv=None):
    here = os.path.dirname(os.path.abspath(__file__))
    household = ""
    try:
        household = json.load(open(os.path.join(here, "presets.json")))["household"]
    except (OSError, KeyError, ValueError):
        pass
    a = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    a.add_argument("--url", default="http://127.0.0.1:11435")
    a.add_argument("--model", default=household)
    a.add_argument("--tokens", type=int, default=14000)
    a.add_argument("--runs", type=int, default=3)
    a.add_argument("--since", default="-2h", help="journalctl --since for the log lines")
    a.add_argument("--label", default="")
    o = a.parse_args(argv)
    if not o.model:
        sys.exit("no --model and no presets.json next to measure.py")
    text, runs = prompt(o.tokens), []
    for i in range(o.runs):
        r = once(o.url, o.model, text, os.environ.get("MODEL_KEY", ""))
        print(f"run {i + 1}: {r['prompt_n']} tokens in {r['prompt_ms']} ms = {r['prompt_per_second']} t/s (wall {r['wall_s']} s)")
        runs.append(r)
    log = subprocess.run(["journalctl", "--user", "-u", "llama", "--since", o.since, "--no-pager", "-o", "cat"],
                         capture_output=True, text=True).stdout
    seen = from_log(log)
    for k, v in seen.items():
        print(f"log {k}: {v}")
    rates = [r["prompt_per_second"] for r in runs if r["prompt_per_second"]]
    print(json.dumps({"label": o.label, "model": o.model, "median_t_s": round(statistics.median(rates), 1) if rates else None,
                      "runs": runs, "log": seen}))


if __name__ == "__main__":
    main()
