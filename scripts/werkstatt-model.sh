#!/usr/bin/env bash
# pi in this commit's Werkstatt image against a fake lease and model proxy
# (#261). The autoloop's own path runs as on the box: it asks the catalogue
# for the preset, takes the lease as `werkstatt`, starts pi with
# `--model laib-llama/<preset>` and gives the lease back. The fake records
# what reached it; the order and the headers are the check. A person's pi
# over SSH is the same binary: `pi --list-models` must show the provider.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMG="${WERKSTATT_IMAGE:-localhost/laib-werkstatt:model-test}"
PORT=18555
KEY=key261
WORK="$(mktemp -d)"
FAKE=""
cleanup() { [ -z "$FAKE" ] || kill "$FAKE" 2>/dev/null || true; rm -rf "$WORK"; }
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; echo "--- what reached the fake:" >&2; cat "$WORK/log.jsonl" >&2 || true; exit 1; }

[ -n "${WERKSTATT_IMAGE:-}" ] || podman build -q -t "$IMG" "$ROOT/werkstatt" >/dev/null

# the lease (internal/lease: POST/GET/DELETE /api/model-lease, the key as bearer)
# and the proxy (/v1/models marks allowed_in_mode; chat streams)
cat > "$WORK/fake.py" <<'PY'
import json, sys, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
LOG = open(sys.argv[2], "a")
PRESET, KEY = "qwen3.8-27b", sys.argv[3]
lease = {"state": "none"}
class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def rec(self, **kw):
        LOG.write(json.dumps(dict(m=self.command, p=self.path, auth=self.headers.get("Authorization"), **kw)) + "\n"); LOG.flush()
    def send(self, code, obj):
        b = json.dumps(obj).encode()
        self.send_response(code); self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
    def body(self):
        n = int(self.headers.get("Content-Length", 0) or 0)
        return json.loads(self.rfile.read(n) or b"{}") if n else {}
    def keyed(self):
        return self.headers.get("Authorization") == "Bearer " + KEY
    def do_GET(self):
        self.rec()
        if self.path == "/api/model-lease":
            return self.send(200, lease)
        if self.path == "/v1/models":
            return self.send(200, {"object": "list", "mode": "haushalt", "data": [
                {"id": "gemma-4-e4b", "object": "model", "allowed_in_mode": True},
                {"id": PRESET, "object": "model", "allowed_in_mode": lease.get("state") == "ready"}]})
        self.send(404, {})
    def do_DELETE(self):
        b = self.body(); self.rec(body=b)
        if not self.keyed(): return self.send(401, {"ok": False, "reason": "unauthorized"})
        lease.clear(); lease["state"] = "none"
        self.send(200, {"ok": True, "state": "released"})
    def do_POST(self):
        b = self.body(); self.rec(body={k: b.get(k) for k in ("model", "ttl_s", "holder", "stream")})
        if self.path == "/api/model-lease":
            if not self.keyed(): return self.send(401, {"ok": False, "reason": "unauthorized"})
            lease.update(state="ready", model="full", alias=b.get("model"), holder=b.get("holder"), expires_at=int(time.time()) + 600, renew_after=200)
            return self.send(200, dict(ok=True, allowed=[PRESET, "gemma-4-e4b"], **lease))
        if self.path == "/v1/chat/completions":
            if b.get("model") == PRESET and lease.get("state") != "ready":
                return self.send(409, {"error": {"message": "Modell nicht erlaubt"}})
            self.send_response(200); self.send_header("Content-Type", "text/event-stream"); self.end_headers()
            for d in ({"role": "assistant", "content": "Hallo aus dem Laib."}, {}):
                c = {"id": "x", "object": "chat.completion.chunk", "created": 1, "model": b.get("model"),
                     "choices": [{"index": 0, "delta": d, "finish_reason": None if d else "stop"}]}
                self.wfile.write(b"data: " + json.dumps(c).encode() + b"\n\n"); self.wfile.flush()
            self.wfile.write(b"data: [DONE]\n\n"); self.wfile.flush(); return
        self.send(404, {})
ThreadingHTTPServer(("0.0.0.0", int(sys.argv[1])), H).serve_forever()
PY
python3 "$WORK/fake.py" "$PORT" "$WORK/log.jsonl" "$KEY" > "$WORK/fake.out" 2>&1 &
FAKE=$!
for _ in $(seq 30); do curl -fsS "http://127.0.0.1:$PORT/api/model-lease" >/dev/null 2>&1 && break; sleep 0.5; done
: > "$WORK/log.jsonl"

# the autoloop's functions, unchanged, in the image as the autoloop runs them
cat > "$WORK/run.py" <<'PY'
import importlib.util, json, pathlib
spec = importlib.util.spec_from_file_location("autoloop", "/opt/werkstatt/autoloop.py")
al = importlib.util.module_from_spec(spec); spec.loader.exec_module(al)
cfg = al.config_from_env()
preset = al.preset_to_use(cfg["models_url"], al.http_json)
note, renew = al.take_lease(cfg, preset, 600, al.http_json)
work = pathlib.Path("/tmp/ticket"); work.mkdir(exist_ok=True)
with al.renewing(cfg, preset, 600, renew, al.http_json):
    summary, capped, refusal = al.run_pi("Sag hallo, mit einem Satz, ohne Werkzeuge.", preset, work, 180)
print(json.dumps({"preset": preset, "note": note, "renew_after": renew, "summary": summary, "capped": capped, "refusal": refusal}))
PY
H="http://host.containers.internal:$PORT"
RUN=(podman run --rm --user dev --entrypoint "" -e MODEL_URL="$H/v1" -e MODEL_LEASE="$H/api/model-lease" -e MODEL_KEY="$KEY"
     -e PI_SKIP_VERSION_CHECK=1 -v "$WORK/run.py:/run.py:ro,Z" "$IMG")
"${RUN[@]}" pi --list-models > "$WORK/models.txt" 2>&1 || fail "pi --list-models exit $?: $(cat "$WORK/models.txt")"
grep -Eq '^laib-llama +qwen3\.8-27b' "$WORK/models.txt" || fail "a person's pi does not list the lease's catalogue: $(cat "$WORK/models.txt")"
echo "pi --list-models: $(grep -c '^laib-llama' "$WORK/models.txt") models from the laib-llama provider"
: > "$WORK/log.jsonl"
"${RUN[@]}" python3 /run.py > "$WORK/run.json" 2> "$WORK/run.err" || fail "the autoloop's run exit $?: $(tail -20 "$WORK/run.err")"
cat "$WORK/run.json"

python3 - "$WORK/log.jsonl" "$WORK/run.json" "$KEY" <<'PY'
import json, sys
log = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
run = json.loads(open(sys.argv[2]).read().strip().splitlines()[-1])
key = "Bearer " + sys.argv[3]
def first(pred, what):
    for i, e in enumerate(log):
        if pred(e): return i
    sys.exit(f"FAIL: {what} never reached the proxy; log: {log}")
assert run["preset"] == "qwen3.8-27b", run
assert run["summary"]["events"] > 0 and run["summary"]["last"] in ("agent_end", "agent_settled"), f"FAIL: pi did not run to its end: {run}"
assert not run["capped"] and not run["refusal"], run
take = first(lambda e: e["m"] == "POST" and e["p"] == "/api/model-lease", "the lease POST")
assert log[take]["auth"] == key and log[take]["body"]["holder"] == "werkstatt" and log[take]["body"]["model"] == "qwen3.8-27b", log[take]
cat = first(lambda e: e["m"] == "GET" and e["p"] == "/v1/models" and e["auth"] == key, "the extension's catalogue GET with the model key")
chat = first(lambda e: e["m"] == "POST" and e["p"] == "/v1/chat/completions", "pi's chat request")
assert log[chat]["body"]["model"] == "qwen3.8-27b" and log[chat]["body"]["stream"], log[chat]
assert log[chat]["auth"] == key, f"FAIL: pi's chat request does not carry the model key: {log[chat]}"
give = first(lambda e: e["m"] == "DELETE" and e["p"] == "/api/model-lease", "the lease DELETE")
assert log[give]["body"] == {"holder": "werkstatt"} and log[give]["auth"] == key, log[give]
assert take < cat < chat < give, f"FAIL: order lease {take}, catalogue {cat}, chat {chat}, release {give}"
assert not [e for e in log[give + 1:] if e["p"].startswith("/v1/")], "FAIL: pi talked to the proxy after the lease was given back"
print(f"order ok: lease #{take}, catalogue #{cat}, chat #{chat}, release #{give}")
PY
echo "werkstatt model test ok: pi has the laib-llama provider, and the autoloop's run took the lease, used it and gave it back"
