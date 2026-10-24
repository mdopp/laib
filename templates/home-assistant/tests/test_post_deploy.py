"""#349: home-assistant's post-deploy mints the Home Assistant tokens itself,
against a fake Home Assistant (its HTTP API and its WebSocket API).

ServiceBay onboarded Home Assistant as OSCAR_HA_ADMIN_USERNAME with a
generated OSCAR_HA_ADMIN_PASSWORD and kept one administrator token in a file
under HA's config folder that every consumer read. Here the hook onboards
(fresh box) or logs in (a box from ServiceBay, the carried password) only
when it has no administrator token that works, mints one token per consumer
and hands each back (`laib-secret:`): Haus and the check get administrator
tokens, the Automat too (HA's automation config API is admin-only), and the
Bote's `ha` tool a token of a separate non-admin user "bote"."""
import base64, hashlib, json, os, socket, struct, subprocess, tempfile, threading, unittest, urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "post-deploy"
ADMIN_PW = "-ha-admin-pw-9q"
BOTE_PW = "-ha-bote-pw-3x"
ADMIN_SECRETS = ("HA_TOKEN", "HAUS_HA_TOKEN", "BOTE_AUTOMAT_HA_TOKEN")
# #591: each consumer on its own Home Assistant user, never a person's: (username, kind)
MACHINES = {"HA_TOKEN": ("laib-pruefung", "user"), "HAUS_HA_TOKEN": ("haus", "admin"),
            "BOTE_AUTOMAT_HA_TOKEN": ("bote-automat", "admin"), "BOTE_HA_TOKEN": ("bote", "user")}
PASSWORDS = {"HA_CHECK_PASSWORD": "-ha-check-pw-1a", "HA_HAUS_PASSWORD": "-ha-haus-pw-2b", "HA_BOTE_AUTOMAT_PASSWORD": "-ha-automat-pw-4c",
             "HA_MUSIC_ASSISTANT_PASSWORD": "-ha-ma-pw-5d"}
HA = {}
MA = {}


def fresh(onboarded=None):
    """onboarded: None for a fresh Home Assistant, else (username, password) of its owner"""
    HA.clear()
    HA.update(users={}, creds={}, tokens={}, names={}, codes={}, flows={}, logins=[], onboarding=set(), core={}, core_updates=0, n=0,
              entries=[], subentries={}, not_loaded=0, states={}, calls=[], ma_url="", ma_reauth=False,
              registry=[], pipelines=[], pipeline_updates=[], engines={"stt": [], "tts": []}, wyoming_up={}, reloads=[], storage=None,
              restarts=0)
    MA.clear()
    MA.update(tokens={"ma-tok": "laib"}, n=0, providers={}, flows={})
    if onboarded:
        uid = add_user("Owner", admin=True, owner=True)
        HA["creds"][onboarded[0]] = (onboarded[1], uid)
        HA["onboarding"] = {"user", "core_config", "analytics", "integration"}


def add_user(name, admin, owner=False):
    HA["n"] += 1
    uid = f"user{HA['n']}"
    HA["users"][uid] = {"id": uid, "name": name, "is_owner": owner, "is_active": True, "local_only": False, "system_generated": False,
                        "group_ids": ["system-admin" if admin else "system-users"]}
    return uid


def token_for(uid, long_lived, name=None):
    if long_lived and name and any(HA["names"].get(t) == (uid, name) for t in HA["tokens"]):
        raise ValueError(f"{name} already exists")   # HA: each client_name only once per user (auth/__init__.py)
    HA["n"] += 1
    t = f"{'llat' if long_lived else 'access'}-{HA['n']}-{uid}"
    HA["tokens"][t] = uid
    if long_lived:
        HA["names"][t] = (uid, name)
    return t


def is_admin(uid):
    return "system-admin" in HA["users"][uid]["group_ids"]


def save():
    """HA's .storage/core.config_entries, the data of every entry (#540: the hook finds Wyoming's host and port there)"""
    if HA["storage"]:
        Path(HA["storage"]).parent.mkdir(parents=True, exist_ok=True)
        Path(HA["storage"]).write_text(json.dumps({"version": 1, "minor_version": 5, "key": "core.config_entries",
                                                   "data": {"entries": [{k: v for k, v in e.items() if k != "state"} for e in HA["entries"]]}}))


def save_auth(data_dir):
    """HA's .storage/auth, as HA writes it, the token values in it too (#591: the hook reads names only)"""
    path = Path(data_dir, "home-assistant", "homeassistant", ".storage", "auth")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps({"version": 1, "minor_version": 1, "key": "auth", "data": {
        "users": list(HA["users"].values()), "groups": [],
        "credentials": [{"id": "c-" + n, "user_id": i, "auth_provider_type": "homeassistant", "auth_provider_id": None, "data": {"username": n}}
                        for n, (_, i) in HA["creds"].items()],
        "refresh_tokens": [{"id": "rt-" + t, "user_id": u, "client_name": HA["names"][t][1], "token_type": "long_lived_access_token",
                            "token": t, "jwt_key": "k-" + t} for t, u in HA["tokens"].items() if t in HA["names"]]}}))


def username(uid):
    return next((u for u, (_, i) in HA["creds"].items() if i == uid), None)


class FakeHA(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def reply(self, code, body=None):
        data = json.dumps(body).encode() if body is not None else b""
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def body(self):
        raw = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        if self.headers.get("Content-Type", "").startswith("application/x-www-form-urlencoded"):
            return dict(urllib.parse.parse_qsl(raw.decode()))
        return json.loads(raw or b"{}")

    def user(self):
        a = self.headers.get("Authorization", "")
        return HA["tokens"].get(a[7:]) if a.startswith("Bearer ") else None

    def do_GET(self):
        if self.path == "/api/websocket":
            return self.websocket()
        if self.path == "/auth/providers":
            if "user" not in HA["onboarding"]:
                return self.reply(400, {"message": "Onboarding not finished", "code": "onboarding_required"})
            return self.reply(200, {"providers": [{"type": "homeassistant", "id": None, "name": "Home Assistant Local"}]})
        if self.path == "/api/onboarding":
            if len(HA["onboarding"]) == 4:
                return self.reply(404)
            return self.reply(200, [{"step": s, "done": s in HA["onboarding"]} for s in ("user", "core_config", "analytics", "integration")])
        uid = self.user()
        if uid is None:
            return self.reply(401)
        if self.path == "/api/":
            return self.reply(200, {"message": "API running."})
        if self.path == "/api/config":   # the core config every user may read (#561)
            return self.reply(200, dict({"latitude": 32.87336, "longitude": -117.22743, "elevation": 0, "time_zone": "UTC", "country": None}, **HA["core"]))
        if self.path == "/api/config/config_entries/entry":
            return self.reply(200, [{"entry_id": "e-met", "domain": "met", "title": "Home", "state": "loaded"}]
                              + [{k: v for k, v in e.items() if k not in ("data", "token")} for e in HA["entries"]])   # every user may list them over REST, as in HA
        if self.path == "/api/states":   # every entity's state, as HA lists them (#540)
            return self.reply(200, [dict(v, entity_id=k) for k, v in HA["states"].items()])
        if self.path == "/api/states/sun.sun":
            return self.reply(200, {"entity_id": "sun.sun", "state": "above_horizon"})
        if self.path.startswith("/api/states/update."):
            s = HA["states"].get(self.path[len("/api/states/"):])
            return self.reply(200, s) if s else self.reply(404)
        self.reply(404)

    def restarting(self):
        """#557: HA restarts under the hook: the connection drops without an answer, HA_RESTARTS times"""
        if HA.get("restarts", 0) > 0:
            HA["restarts"] -= 1
            self.close_connection = True
            self.connection.shutdown(socket.SHUT_RDWR)
            return True
        return False

    def do_POST(self):
        b = self.body()
        if self.path == "/api/config/config_entries/flow" and b.get("handler") == "wyoming" and self.restarting():
            return
        if self.path.startswith("/api/services/"):   # #528: update.install, homeassistant.restart
            if self.user() is None or not is_admin(self.user()):
                return self.reply(401)
            HA["calls"].append((self.path, b))
            if self.path == "/api/services/update/install":
                st = HA["states"][b["entity_id"]]
                st["state"], st["attributes"]["installed_version"] = "off", st["attributes"]["latest_version"]
            return self.reply(200, [])
        if self.path == "/api/onboarding/users":
            if "user" in HA["onboarding"]:
                return self.reply(403, {"message": "User step already done"})
            uid = add_user(b["name"], admin=True, owner=True)
            HA["creds"][b["username"]] = (b["password"], uid)
            HA["onboarding"].add("user")
            HA["codes"]["code-onb"] = uid
            return self.reply(200, {"auth_code": "code-onb"})
        if self.path == "/auth/login_flow":
            HA["flows"]["flow1"] = b["client_id"]
            return self.reply(200, {"type": "form", "flow_id": "flow1", "step_id": "init"})
        if self.path.startswith("/auth/login_flow/"):
            HA["logins"].append(b["username"])
            pw, uid = HA["creds"].get(b["username"], (None, None))
            if pw is None or pw != b["password"]:
                return self.reply(200, {"type": "form", "flow_id": "flow1", "errors": {"base": "invalid_auth"}})
            HA["codes"]["code-" + uid] = uid
            return self.reply(200, {"type": "create_entry", "result": "code-" + uid})
        if self.path == "/auth/token":
            uid = HA["codes"].pop(b.get("code"), None)
            if b.get("grant_type") != "authorization_code" or uid is None:
                return self.reply(400, {"error": "invalid_request"})
            return self.reply(200, {"access_token": token_for(uid, False), "token_type": "Bearer", "refresh_token": "r", "expires_in": 1800})
        uid = self.user()
        if self.path.startswith("/api/config/config_entries/"):
            if uid is None or not is_admin(uid):
                return self.reply(401 if uid is None else 403)
            if self.path.endswith("/reload"):   # #540: an entry set up again, synchronously, as HA does
                eid = self.path.split("/")[-2]
                e = next((e for e in HA["entries"] if e["entry_id"] == eid), None)
                if e is None:
                    return self.reply(404)
                HA["reloads"].append(eid)
                up = e["domain"] == "wyoming" and e["data"]["port"] in HA["wyoming_up"]
                e["state"] = "loaded" if up else "setup_retry"
                for reg in HA["registry"]:
                    if reg["config_entry_id"] == eid:
                        HA["states"][reg["entity_id"]] = {"state": "unknown" if up else "unavailable"}
                return self.reply(200, {"require_restart": False})
            return self.config_flow(b)
        if self.path.startswith("/api/onboarding/"):
            step = self.path.rsplit("/", 1)[1]
            if uid is None or step in HA["onboarding"] or (step == "integration" and not b.get("redirect_uri")):
                return self.reply(403 if uid else 401)
            HA["onboarding"].add(step)
            return self.reply(200, {})
        self.reply(404)

    def config_flow(self, b):
        """HA's config flows over REST, the two the Ollama integration has (2026.10):
        the entry (one step, url; title = the url) and its conversation subentry;
        CalDAV's (#495), and Music Assistant's (#526)"""
        if self.path == "/api/config/config_entries/flow" and b.get("handler") == "caldav":
            HA["flows"]["cf1"] = "caldav"
            return self.reply(200, {"type": "form", "flow_id": "cf1", "handler": "caldav", "step_id": "user"})
        if self.path == "/api/config/config_entries/flow/cf1":   # HA's caldav flow: url, username, password, verify_ssl; title = username
            if HA.get("radicale_pw") != b.get("password"):
                return self.reply(200, {"type": "form", "flow_id": "cf1", "errors": {"base": "invalid_auth"}})
            e = {"entry_id": f"e-caldav-{len(HA['entries'])}", "domain": "caldav", "title": b["username"], "data": b}
            HA["entries"].append(e)
            return self.reply(200, {"type": "create_entry", "flow_id": "cf1", "result": {k: e[k] for k in ("entry_id", "domain", "title")}})
        if b.get("handler") == "music_assistant" or self.path.startswith("/api/config/config_entries/flow/m"):
            return self.ma_flow(b)
        if b.get("handler") == "wyoming" or self.path.startswith("/api/config/config_entries/flow/w"):
            return self.wyoming_flow(b)
        if self.path == "/api/config/config_entries/flow":
            if b.get("handler") != "ollama":
                return self.reply(400, {"message": "Invalid handler specified"})
            HA["flows"]["of1"] = "ollama"
            return self.reply(200, {"type": "form", "flow_id": "of1", "handler": "ollama", "step_id": "user"})
        if self.path == "/api/config/config_entries/flow/of1":
            url = b["url"]
            if any(e["domain"] == "ollama" and e["data"]["url"] == url for e in HA["entries"]):
                return self.reply(200, {"type": "abort", "flow_id": "of1", "reason": "already_configured"})
            e = {"entry_id": f"e-ollama-{len(HA['entries'])}", "domain": "ollama", "title": url, "data": {"url": url}}
            HA["entries"].append(e)
            return self.reply(200, {"type": "create_entry", "flow_id": "of1", "result": {k: e[k] for k in ("entry_id", "domain", "title")}})
        if self.path == "/api/config/config_entries/subentries/flow":
            eid, kind = b["handler"]
            if kind != "conversation" or not any(e["entry_id"] == eid for e in HA["entries"]):
                return self.reply(400, {"message": "Invalid handler specified"})
            if HA["not_loaded"]:
                HA["not_loaded"] -= 1
                return self.reply(200, {"type": "abort", "flow_id": "sf0", "reason": "entry_not_loaded"})
            HA["flows"]["sf1"] = eid
            return self.reply(200, {"type": "form", "flow_id": "sf1", "handler": [eid, "conversation"], "step_id": "set_options"})
        if self.path == "/api/config/config_entries/subentries/flow/sf1":
            eid = HA["flows"].pop("sf1")
            sub = {"subentry_id": f"s{len(HA['subentries'].get(eid, []))}", "subentry_type": "conversation", "title": b["name"],
                   "unique_id": None, "data": {k: v for k, v in b.items() if k != "name"}}
            HA["subentries"].setdefault(eid, []).append(sub)
            ent = "conversation." + b["name"].lower()   # the agent is an entity of the entry (#540: a pipeline names it)
            HA["registry"].append({"entity_id": ent, "config_entry_id": eid, "platform": "ollama", "disabled_by": None})
            HA["states"][ent] = {"state": "unknown"}
            return self.reply(200, {"type": "create_entry", "flow_id": "sf1", "title": b["name"]})
        self.reply(404)

    def wyoming_flow(self, b):
        """homeassistant/components/wyoming/config_flow.py (2026.10): one step, host and port; the title is the
        server's first program (data.py get_name); no abort for a server HA has already: a second entry"""
        if self.path == "/api/config/config_entries/flow":
            fid = f"wf{len(HA['flows'])}"
            HA["flows"][fid] = "wyoming"
            return self.reply(200, {"type": "form", "flow_id": fid, "handler": "wyoming", "step_id": "user"})
        fid = self.path.rsplit("/", 1)[1]
        if HA["flows"].pop(fid, None) != "wyoming":
            return self.reply(404)
        title = HA["wyoming_up"].get(b.get("port")) if b.get("host") == "127.0.0.1" else None
        if title is None:
            return self.reply(200, {"type": "form", "flow_id": fid, "step_id": "user", "errors": {"base": "cannot_connect"}})
        e = {"entry_id": f"e-wy-{len(HA['entries'])}", "domain": "wyoming", "title": title, "state": "loaded",
             "data": {"host": b["host"], "port": b["port"]}}
        HA["entries"].append(e)
        ent = {"faster-whisper": "stt.faster_whisper", "piper": "tts.piper", "openwakeword": "wake_word.openwakeword"}[title]
        HA["registry"].append({"entity_id": ent, "config_entry_id": e["entry_id"], "platform": "wyoming", "disabled_by": None})
        HA["states"][ent] = {"state": "unknown"}
        save()
        return self.reply(200, {"type": "create_entry", "flow_id": fid, "title": title, "result": {k: e[k] for k in ("entry_id", "domain", "title")}})

    def ma_flow(self, b):
        """homeassistant/components/music_assistant/config_flow.py (2026.10): user (url) → the browser
        step auth (with `my` loaded, default_config) → the code MA's login returns → finish_auth,
        which exchanges it for HA's own long-lived MA token; a reauth goes the same way"""
        if self.path == "/api/config/config_entries/flow":
            HA["flows"]["mf1"] = "user"
            return self.reply(200, {"type": "form", "flow_id": "mf1", "handler": "music_assistant", "step_id": "user"})
        fid = self.path.rsplit("/", 1)[1]
        step = HA["flows"].get(fid)
        if step in ("user",):
            if b.get("url") != HA["ma_url"]:
                return self.reply(200, {"type": "form", "flow_id": fid, "step_id": "user", "errors": {"base": "cannot_connect"}})
            if any(e["domain"] == "music_assistant" for e in HA["entries"]):
                return self.reply(200, {"type": "abort", "flow_id": fid, "reason": "already_configured"})
        if step in ("user", "reauth_confirm"):
            HA["flows"][fid] = "auth"
            return self.reply(200, {"type": "external", "flow_id": fid, "step_id": "auth", "url": HA["ma_url"] + "/login?return_url=x"})
        if step == "auth":
            if b.get("code") not in MA["tokens"]:
                return self.reply(200, {"type": "abort", "flow_id": fid, "reason": "auth_error"})
            HA["flows"][fid] = ("finish_auth", b["code"])
            return self.reply(200, {"type": "external_done", "flow_id": fid, "step_id": "finish_auth"})
        if isinstance(step, tuple):
            del HA["flows"][fid]
            MA["n"] += 1
            own = f"ma-ha-{MA['n']}"   # HA's own long-lived MA token, made with the code (create_long_lived_token)
            MA["tokens"][own] = MA["tokens"][step[1]]
            if fid == "mr1":
                HA["ma_reauth"] = False
                next(e for e in HA["entries"] if e["domain"] == "music_assistant").update(state="loaded", token=own)
                return self.reply(200, {"type": "abort", "flow_id": fid, "reason": "reauth_successful"})
            HA["entries"].append({"entry_id": "e-ma", "domain": "music_assistant", "title": "Music Assistant", "state": "loaded", "token": own})
            return self.reply(200, {"type": "create_entry", "flow_id": fid, "result": {"entry_id": "e-ma"}})
        self.reply(404)

    # --- the WebSocket API, as Home Assistant speaks it ---------------------
    def websocket(self):
        key = self.headers["Sec-WebSocket-Key"]
        accept = base64.b64encode(hashlib.sha1((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()).digest()).decode()
        self.send_response(101)
        self.send_header("Upgrade", "websocket")
        self.send_header("Connection", "Upgrade")
        self.send_header("Sec-WebSocket-Accept", accept)
        self.end_headers()
        self.wfile.flush()
        self.ws_send({"type": "auth_required", "ha_version": "2026.10.2"})
        msg = self.ws_recv()
        token = msg.get("access_token")
        uid = HA["tokens"].get(token)
        if msg.get("type") != "auth" or uid is None:
            self.ws_send({"type": "auth_invalid", "message": "Invalid access token or password"})
            return
        self.ws_send({"type": "auth_ok", "ha_version": "2026.10.2"})
        self.ws_send({"type": "event", "event": {"noise": "an event between results"}})   # the client reads past it
        while True:
            try:
                msg = self.ws_recv()
            except (ConnectionError, ValueError, struct.error):  # the client hung up
                return
            if msg is None:
                return
            self.ws_send(self.ws_command(uid, msg, token))

    def ws_command(self, uid, m, current=None):
        t, i = m["type"], m["id"]
        ok = lambda r=None: {"id": i, "type": "result", "success": True, "result": r}
        err = lambda code, text: {"id": i, "type": "result", "success": False, "error": {"code": code, "message": text}}
        if t == "auth/long_lived_access_token":
            try:
                return ok(token_for(uid, True, m["client_name"]))
            except ValueError:
                return err("unknown_error", "Unknown error")   # what HA answers
        if t == "auth/current_user":   # every user may ask who they are; is_admin is the owner or the admin group (auth/models.py)
            u = HA["users"][uid]
            return ok({"id": uid, "name": u["name"], "is_owner": u["is_owner"], "is_admin": u["is_owner"] or is_admin(uid)})
        if t == "auth/refresh_tokens":
            return ok([{"id": "rt-" + tok, "client_name": HA["names"][tok][1], "type": "long_lived_access_token", "is_current": tok == current}
                       for tok in HA["tokens"] if tok in HA["names"] and HA["names"][tok][0] == uid])
        if t == "auth/delete_refresh_token":
            tok = m["refresh_token_id"][3:]
            if HA["names"].get(tok, (None,))[0] != uid:
                return err("invalid_token_id", "Received invalid token")
            del HA["tokens"][tok], HA["names"][tok]
            return ok()
        if not is_admin(uid):
            return err("unauthorized", "Unauthorized")
        if t == "config/auth/list":
            return ok([dict(u, username=username(u["id"])) for u in HA["users"].values()])
        if t == "config/auth/create":
            nid = add_user(m["name"], admin="system-admin" in (m.get("group_ids") or []))
            return ok({"user": dict(HA["users"][nid], username=None)})
        if t == "config/auth/update":
            HA["users"][m["user_id"]]["group_ids"] = m["group_ids"]
            return ok({"user": HA["users"][m["user_id"]]})
        if t == "config/auth_provider/homeassistant/create":
            if username(m["user_id"]):
                return err("credentials_exist", "User already has credentials for this provider")
            HA["creds"][m["username"]] = (m["password"], m["user_id"])
            return ok()
        if t == "config/auth_provider/homeassistant/admin_change_password":
            if not HA["users"][uid]["is_owner"]:
                return err("unauthorized", "Unauthorized")
            name = username(m["user_id"])
            HA["creds"][name] = (m["password"], m["user_id"])
            return ok()
        if t == "config_entries/flow/progress":
            return ok([{"flow_id": "mr1", "handler": "music_assistant", "step_id": "reauth_confirm", "context": {"source": "reauth"}}]
                      if HA.get("ma_reauth") else [])
        if t == "config_entries/subentries/list":
            return ok([{k: v for k, v in sub.items() if k != "data"} for sub in HA["subentries"].get(m["entry_id"], [])])
        if t == "config/entity_registry/list":
            return ok(HA["registry"])
        if t == "assist_pipeline/pipeline/list":
            return ok({"pipelines": HA["pipelines"], "preferred_pipeline": HA["pipelines"][0]["id"] if HA["pipelines"] else None})
        if t == "assist_pipeline/pipeline/update":   # PIPELINE_FIELDS, every one required as in HA
            fields = {k: v for k, v in m.items() if k not in ("id", "type", "pipeline_id")}
            p = next((p for p in HA["pipelines"] if p["id"] == m["pipeline_id"]), None)
            if p is None or set(fields) != set(p) - {"id"}:
                return err("invalid_format", f"fields {sorted(fields)}")
            p.update(fields)
            HA["pipeline_updates"].append(m["pipeline_id"])
            return ok(p)
        if t in ("stt/engine/list", "tts/engine/list"):
            kind = t.split("/")[0]
            lang = m.get("language") or ""
            return ok({"providers": [dict(x, supported_languages=[l for l in x["supported_languages"] if l.split("_")[0].split("-")[0] == lang])
                                     for x in HA["engines"][kind]]})
        if t == "config/core/update":
            HA["core"].update({k: v for k, v in m.items() if k not in ("id", "type")})
            HA["core_updates"] += 1
            return ok()
        return err("unknown_command", t)

    def ws_send(self, obj):
        data = json.dumps(obj).encode()
        n = len(data)
        head = bytes([0x81, n]) if n < 126 else bytes([0x81, 126]) + struct.pack(">H", n) if n < 65536 else bytes([0x81, 127]) + struct.pack(">Q", n)
        self.wfile.write(head + data)
        self.wfile.flush()

    def ws_recv(self):
        b0, b1 = self.rfile.read(2)
        if b0 & 0x0F == 8:
            return None
        if not b1 & 0x80:
            raise AssertionError("a client frame must be masked (RFC 6455 5.1)")
        n = b1 & 0x7F
        if n == 126:
            n = struct.unpack(">H", self.rfile.read(2))[0]
        elif n == 127:
            n = struct.unpack(">Q", self.rfile.read(8))[0]
        mask = self.rfile.read(4)
        data = bytes(c ^ mask[k % 4] for k, c in enumerate(self.rfile.read(n)))
        return json.loads(data)


class Hook(unittest.TestCase):
    def setUp(self):
        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), FakeHA)
        self.srv.daemon_threads = True
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.addCleanup(self.srv.shutdown)

    def run_hook(self, tokens=None, user="laib", password=ADMIN_PW, bote_password=BOTE_PW, frage="http://127.0.0.1:1", install="", ma="http://127.0.0.1:1",
                 stimme=(1, 1, 1), data_dir="", zuhause=None, wait="60"):
        HA["ma_url"] = ma
        env = {"PATH": os.environ["PATH"], "LAIB_DOMAIN": "box.test", "LAIB_TZ": "Europe/Berlin", "FRAGE_URL": frage, "HA_RETRY_SECONDS": "0.1", "HA_WAIT_SECONDS": wait, "HA_INSTALL": install,
               "MA_URL": ma, "LAIB_DATA_DIR": data_dir or "/nonexistent",
               "STIMME_WHISPER_PORT": str(stimme[0]), "STIMME_PIPER_PORT": str(stimme[1]), "STIMME_WAKEWORD_PORT": str(stimme[2]),
               "HA_URL": f"http://127.0.0.1:{self.srv.server_port}", "HA_ADMIN_USER": user,
               "LAIB_SECRET_HA_ADMIN_PASSWORD": password, "LAIB_SECRET_HA_BOTE_PASSWORD": bote_password}
        env.update({"LAIB_SECRET_" + k: v for k, v in PASSWORDS.items()})
        if data_dir:
            save_auth(data_dir)
        for name, value in (tokens or {}).items():
            env["LAIB_SECRET_" + name] = value
        env.update(zuhause or {})
        p = subprocess.run([str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
        handed = dict(l[len("laib-secret: "):].split("=", 1) for l in p.stdout.splitlines() if l.startswith("laib-secret: "))
        rest = "\n".join(l for l in (p.stdout + "\n" + p.stderr).splitlines() if not l.startswith("laib-secret: "))
        for value in list(handed.values()) + list((tokens or {}).values()) + [password, bote_password] + list(PASSWORDS.values()) + list(HA["tokens"]):
            if value:
                self.assertNotIn(value, rest, "a token or a password in the hook's log (#214)")
        return p, handed, rest

    def kind(self, token):
        uid = HA["tokens"].get(token)
        return None if uid is None else ("admin" if is_admin(uid) else "user")

    def assertOnItsOwnUser(self, handed):
        """#591: each handed token a long-lived one of its consumer's own user, of its kind"""
        for name, tok in handed.items():
            if name in MACHINES:
                self.assertEqual((username(HA["tokens"].get(tok)), self.kind(tok)), MACHINES[name], name)
                self.assertTrue(tok.startswith("llat-"), f"{name} is a short-lived session, not a long-lived token")


class PostDeploy(Hook):

    def test_an_approved_extension_is_installed_once_and_ha_restarts(self):
        # #528: the Operator proposed HA_INSTALL from the check's finding; a person approved it.
        # Installed only while HA offers exactly that version; firmware a person did not name stays.
        fresh()
        upd = lambda old, new, cls=None: {"state": "on", "attributes": {"installed_version": old, "latest_version": new, "device_class": cls}}
        HA["states"].update({"update.senec": upd("2026.6.3", "2026.10.0"), "update.meross": upd("1.0", "1.2"),
                             "update.plug_firmware": upd("9.5.52", "9.5.60", "firmware")})
        p, _, log = self.run_hook(install="update.senec=2026.10.0,update.meross=1.1")
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(HA["calls"], [("/api/services/update/install", {"entity_id": "update.senec"}), ("/api/services/homeassistant/restart", {})], log)
        self.assertIn("update.meross 1.1: nichts zu installieren", log)
        self.assertEqual(HA["states"]["update.plug_firmware"]["state"], "on")
        HA["calls"].clear()
        p, _, log = self.run_hook(install="update.senec=2026.10.0")
        self.assertEqual((p.returncode, HA["calls"]), (0, []), "installed is installed: no second install, no restart")

    def test_a_fresh_home_assistant_is_onboarded_and_every_token_minted(self):
        fresh()
        p, handed, log = self.run_hook()
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(HA["onboarding"], {"user", "core_config", "analytics", "integration"}, "onboarding left half done")
        self.assertEqual(HA["creds"]["laib"][0], ADMIN_PW)
        self.assertEqual(HA["core"].get("time_zone"), "Europe/Berlin")
        self.assertEqual(set(handed), set(MACHINES))
        self.assertOnItsOwnUser(handed)
        self.assertEqual(len({handed[n] for n in handed}), 4, "one token per consumer, each revocable on its own")
        laib = HA["creds"]["laib"][1]
        self.assertFalse([n for t, n in HA["names"].items() if n[0] == laib], "a consumer's token on the administrator's own user (#591)")
        self.assertEqual({HA["users"][i]["name"] for _, i in HA["creds"].values()}, {"Laib", "Laib-Prüfung", "Haus", "Bote-Automat", "Bote"})
        bote = HA["tokens"][handed["BOTE_HA_TOKEN"]]
        self.assertEqual(self.kind(handed["BOTE_HA_TOKEN"]), "user", "the Bote's token must not be an administrator's")
        self.assertEqual((username(bote), HA["users"][bote]["group_ids"]), ("bote", ["system-users"]))
        self.assertEqual(HA["creds"]["bote"][0], BOTE_PW)

    def test_working_tokens_are_kept_and_nobody_logs_in(self):
        fresh()
        _, handed, _ = self.run_hook()
        HA["logins"].clear()
        p, again, log = self.run_hook(tokens=handed)
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(again, {}, "a working token was replaced")
        self.assertEqual(HA["logins"], [], "logged in although every token works")

    def test_a_revoked_token_is_replaced_with_the_admin_token_that_still_works(self):
        fresh()
        _, handed, _ = self.run_hook()
        del HA["tokens"][handed["HAUS_HA_TOKEN"]], HA["tokens"][handed["BOTE_HA_TOKEN"]]
        HA["logins"].clear()
        p, again, log = self.run_hook(tokens=handed, password="")
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(set(again), {"HAUS_HA_TOKEN", "BOTE_HA_TOKEN"})
        self.assertOnItsOwnUser(again)
        self.assertEqual(HA["logins"], ["haus", "bote"], "the administrator logged in although an administrator token works")

    def test_a_secret_lost_while_ha_still_holds_its_token_by_name_is_replaced(self):
        # HA keeps one long-lived token per name and user: Laib lost HA_TOKEN (or it was set to junk),
        # HA still has "laib: home-assistant-check"; the old one goes, a new one is minted
        fresh()
        _, handed, _ = self.run_hook()
        tokens = dict(handed, HA_TOKEN="junk")
        p, again, log = self.run_hook(tokens=tokens)
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(set(again), {"HA_TOKEN"})
        self.assertOnItsOwnUser(again)
        self.assertNotIn(handed["HA_TOKEN"], HA["tokens"], "the old token of that name is still valid in HA")
        self.assertEqual(self.kind(handed["HAUS_HA_TOKEN"]), "admin", "another consumer's token was revoked")

    def test_a_box_from_servicebay_logs_in_as_its_admin_and_the_bote_stays_no_admin(self):
        # ServiceBay onboarded as oscar; the owner once pasted an administrator's token as BOTE_HA_TOKEN,
        # and a "bote" user exists that someone made an administrator
        fresh(onboarded=("oscar", ADMIN_PW))
        owner = next(iter(HA["users"]))
        pasted = token_for(owner, True)
        bote = add_user("Bote", admin=True)
        HA["creds"]["bote"] = ("an old password", bote)
        p, handed, log = self.run_hook(tokens={"BOTE_HA_TOKEN": pasted}, user="oscar")
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(HA["logins"][0], "oscar")
        self.assertEqual(set(handed), set(MACHINES) | {"HA_RETIRED_TOKENS"}, "the pasted admin token stayed the Bote's")
        self.assertEqual(HA["tokens"][handed["BOTE_HA_TOKEN"]], bote, "a second Bote user instead of the one there")
        self.assertEqual(HA["users"][bote]["group_ids"], ["system-users"])
        self.assertOnItsOwnUser(handed)
        self.assertIn(pasted, HA["tokens"], "the owner's own token was revoked: not Laib's to take")
        p, again, log = self.run_hook(tokens=dict(handed, BOTE_HA_TOKEN=handed["BOTE_HA_TOKEN"]), user="oscar")
        self.assertEqual((p.returncode, again), (0, {"HA_RETIRED_TOKENS": ""}), log)
        self.assertIn(pasted, HA["tokens"], "a token Laib did not mint was revoked once the Bote's own one worked")
        self.assertIn("not one Laib minted", log)

    def test_an_old_box_moves_each_token_to_its_own_user_and_revokes_the_old_one_only_after(self):
        # #591: #349 minted HA_TOKEN, HAUS_HA_TOKEN and BOTE_AUTOMAT_HA_TOKEN on the administrator,
        # a person's user: every Haus action showed in HA's logbook as that person
        owner, old = old_box()
        p, handed, log = self.run_hook(tokens=old)
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(set(handed), {"HA_TOKEN", "HAUS_HA_TOKEN", "BOTE_AUTOMAT_HA_TOKEN", "HA_RETIRED_TOKENS"}, log)
        self.assertOnItsOwnUser(handed)
        for name, tok in old.items():
            self.assertIn(tok, HA["tokens"], f"the old {name} revoked before its consumer had the new one")
        # laib stored the hand-back and re-applies the consumers, then home-assistant with what it stored
        stored = dict(old, **handed)
        HA["logins"].clear()
        p, again, log = self.run_hook(tokens=stored)
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(again, {"HA_RETIRED_TOKENS": ""}, "a verified token was replaced, or the old ones are still held")
        for name, tok in old.items():
            if name != "BOTE_HA_TOKEN":   # on the Bote's own user already
                self.assertNotIn(tok, HA["tokens"], f"the old {name} still works on the owner's user")
        self.assertEqual([n for t, n in HA["names"].items() if n[0] == owner], [], "a Laib token left on the owner's user")
        self.assertIn('the old token "laib: haus" revoked', log)
        self.assertEqual(HA["logins"], [], "logged in although every token works")
        p, third, log = self.run_hook(tokens=dict(stored, **again))
        self.assertEqual((p.returncode, third), (0, {}), "a third run changed something: " + log)

    def test_an_old_token_stays_while_its_successor_does_not_work(self):
        owner, old = old_box()
        _, handed, _ = self.run_hook(tokens=old)
        del HA["tokens"][handed["HAUS_HA_TOKEN"]]   # the new one refused (revoked by hand) before it was verified
        p, again, log = self.run_hook(tokens=dict(old, **handed))
        self.assertEqual(p.returncode, 0, log)
        self.assertIn(old["HAUS_HA_TOKEN"], HA["tokens"], "the old token went although its successor never worked")
        self.assertNotIn(old["HA_TOKEN"], HA["tokens"])
        self.assertOnItsOwnUser({"HAUS_HA_TOKEN": again["HAUS_HA_TOKEN"]})
        self.assertTrue(again.get("HA_RETIRED_TOKENS", "").startswith("HAUS_HA_TOKEN="), "the old one forgotten")
        _, last, log = self.run_hook(tokens={**old, **handed, **again})
        self.assertNotIn(old["HAUS_HA_TOKEN"], HA["tokens"], log)
        self.assertEqual(last, {"HA_RETIRED_TOKENS": ""})

    def test_whoever_else_is_in_home_assistant_is_listed_and_nothing_is_deleted(self):
        # #591 point 4: users from before (a ServiceBay admin with its bridge token, a second user
        # of the owner's name) are the owner's decision; the hook names them, never their tokens' values
        owner, old = old_box()
        legacy = add_user("Legacy Admin", admin=True)
        HA["creds"]["legacy"] = ("x", legacy)
        secret = token_for(legacy, True, "legacy-bridge")
        add_user("Owner", admin=False)
        users = len(HA["users"])
        with tempfile.TemporaryDirectory() as d:
            p, handed, log = self.run_hook(tokens=old, data_dir=d)
            self.assertEqual(p.returncode, 0, log)
            self.assertIn("not Laib's", log)
            self.assertIn("„Legacy Admin“ (administrator): „legacy-bridge“", log)
            self.assertIn("„Owner“ (owner, HA_ADMIN_USER): „laib: home-assistant-check“", log, "Laib's old token names on a person's user are not named")
            self.assertEqual(log.count("„Owner“"), 2, "the second user of that name is not listed")
            self.assertNotIn("Haus“", log, "Laib's own users listed as strangers")
            self.assertNotIn(secret, log)
            self.assertNotIn("k-" + secret, log)
        self.assertEqual(len(HA["users"]), users + 3, "a user deleted, or more than Laib-Prüfung, Haus and Bote-Automat added")
        self.assertIn(secret, HA["tokens"])

    def test_no_admin_login_and_no_token_is_red_and_says_what_to_set(self):
        fresh(onboarded=("oscar", "the real one"))
        p, handed, log = self.run_hook(user="oscar")
        self.assertNotEqual(p.returncode, 0)
        self.assertEqual(handed, {})
        self.assertIn("laib secret set HA_ADMIN_PASSWORD", log)
        self.assertIn("HA_ADMIN_USER", log)


def old_box():
    """a box from before #591: #349 minted the check's, Haus's and the Automat's tokens on the
    administrator, the owner's own user; the Bote has its own user already"""
    fresh(onboarded=("laib", ADMIN_PW))
    owner = next(iter(HA["users"]))
    old = {n: token_for(owner, True, label) for n, label in (("HA_TOKEN", "laib: home-assistant-check"), ("HAUS_HA_TOKEN", "laib: haus"),
                                                              ("BOTE_AUTOMAT_HA_TOKEN", "laib: bote-automat"))}
    bote = add_user("Bote", admin=False)
    HA["creds"]["bote"] = (BOTE_PW, bote)
    old["BOTE_HA_TOKEN"] = token_for(bote, True, "laib: bote")
    return owner, old


def fake_frage():
    """Frage's Ollama face (#491): the model list the integration's config flow reads"""
    class F(BaseHTTPRequestHandler):
        def do_GET(self):
            body = json.dumps({"models": [{"name": "frage", "model": "frage"}]} if self.path == "/api/tags" else {}).encode()
            self.send_response(200 if self.path == "/api/tags" else 404)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *a):
            pass
    srv = ThreadingHTTPServer(("127.0.0.1", 0), F)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


class FrageAgent(Hook):
    """#491: Frage as Assist's conversation agent. HA 2026.10 has no built-in
    integration for an OpenAI server with its own URL; its Ollama integration
    takes any URL, and Frage speaks Ollama's /api/tags and /api/chat — the
    path Solaris's post-deploy took for conversation.solaris."""

    def setUp(self):
        super().setUp()
        self.frage = fake_frage()
        self.addCleanup(self.frage.shutdown)
        self.url = f"http://127.0.0.1:{self.frage.server_port}"

    def ollama(self):
        return [e for e in HA["entries"] if e["domain"] == "ollama"]

    def test_frage_is_wired_as_a_conversation_agent_once(self):
        fresh()
        HA["not_loaded"] = 1   # a fresh entry loads asynchronously: the subentry flow aborts until it has
        p, handed, log = self.run_hook(frage=self.url)
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual([e["data"]["url"] for e in self.ollama()], [self.url])
        subs = HA["subentries"][self.ollama()[0]["entry_id"]]
        self.assertEqual([(s["subentry_type"], s["title"], s["data"]["model"]) for s in subs], [("conversation", "Frage", "frage")])
        self.assertNotIn("llm_hass_api", subs[0]["data"], "Frage runs no HA tools: HA's own intents answer commands")
        HA["logins"].clear()
        p, _, log = self.run_hook(tokens=handed, frage=self.url)
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(len(self.ollama()), 1, "a second Ollama entry for Frage")
        self.assertEqual(len(HA["subentries"][self.ollama()[0]["entry_id"]]), 1, "a second Frage agent")
        self.assertEqual(HA["logins"], [], "logged in although every token works")

    def test_an_entry_from_before_gets_its_agent(self):
        # the owner added the Ollama integration for Frage by hand, without an agent
        fresh()
        HA["entries"].append({"entry_id": "e-hand", "domain": "ollama", "title": self.url, "data": {"url": self.url}})
        p, _, log = self.run_hook(frage=self.url)
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(len(self.ollama()), 1)
        self.assertEqual([s["title"] for s in HA["subentries"]["e-hand"]], ["Frage"])

    def test_without_frage_nothing_is_wired_and_the_apply_stays_green(self):
        fresh()
        p, _, log = self.run_hook()   # FRAGE_URL answers nothing
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(self.ollama(), [])
        self.assertIn("laib add frage", log, "the hook does not say how Frage becomes the agent")

    def test_the_calendars_come_in_as_has_own_radicale_account_once(self):   # #495
        fresh()
        HA["radicale_pw"] = "-ha-cal-pw"
        p, handed, log = self.run_hook(tokens={"HA_RADICALE_PASSWORD": "-ha-cal-pw"})
        self.assertEqual(p.returncode, 0, log)
        cal = [e for e in HA["entries"] if e["domain"] == "caldav"]
        self.assertEqual([(e["title"], e["data"]["url"]) for e in cal], [("homeassistant", "http://127.0.0.1:5232/")], log)
        p, _, log = self.run_hook(tokens=dict(handed, HA_RADICALE_PASSWORD="-ha-cal-pw"))
        self.assertEqual(len([e for e in HA["entries"] if e["domain"] == "caldav"]), 1, "a second CalDAV entry")
        HA["radicale_pw"] = "other"
        HA["entries"].clear()
        p, _, log = self.run_hook(tokens={"HA_RADICALE_PASSWORD": "-ha-cal-pw"})
        self.assertEqual(p.returncode, 0, "a refused CalDAV entry fails the apply: " + log)
        self.assertIn("CalDAV entry NOT set up", log)
        fresh()
        p, _, log = self.run_hook()
        self.assertEqual(p.returncode, 0, log)
        self.assertIn("no HA_RADICALE_PASSWORD", log)


class Stimme(Hook):
    """#540: Solaris off, Stimme on. HA's post-deploy adds Stimme's Wyoming entries once, found by
    host and port in HA's .storage (the REST list has no data), takes over the one Solaris' Whisper
    left on 10300, and moves a pipeline part whose engine is gone to Stimme or Frage; a part that
    works, or a legacy provider, stays the owner's choice."""

    def setUp(self):
        super().setUp()
        self.socks = []
        for _ in range(3):   # Stimme's three Wyoming servers: a port that answers
            sk = socket.socket()
            sk.bind(("127.0.0.1", 0))
            sk.listen(8)
            self.socks.append(sk)
            self.addCleanup(sk.close)
        self.ports = tuple(sk.getsockname()[1] for sk in self.socks)
        self.frage = fake_frage()
        self.addCleanup(self.frage.shutdown)
        self.url = f"http://127.0.0.1:{self.frage.server_port}"
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)

    def kraftwerk(self):
        """the Kraftwerk after its Solaris units went (#540): Solaris' Whisper entry on 10300 (now Stimme's port)
        waits in setup_retry, its TTS on 10203 and its Ollama facade on 8787 are gone, the pipeline used them"""
        fresh()
        HA["storage"] = os.path.join(self.tmp.name, "home-assistant", "homeassistant", ".storage", "core.config_entries")
        w, pi, ww = self.ports
        HA["wyoming_up"] = {w: "faster-whisper", pi: "piper", ww: "openwakeword"}
        HA["entries"] += [{"entry_id": "e-sol-whisper", "domain": "wyoming", "title": "faster-whisper", "state": "setup_retry", "data": {"host": "localhost", "port": w}},
                          {"entry_id": "e-sol-tts", "domain": "wyoming", "title": "Solaris TTS", "state": "setup_retry", "data": {"host": "127.0.0.1", "port": 10203}},
                          {"entry_id": "e-sol", "domain": "ollama", "title": "http://127.0.0.1:8787/ollama", "state": "setup_retry", "data": {"url": "http://127.0.0.1:8787/ollama"}}]
        HA["registry"] += [{"entity_id": "stt.faster_whisper", "config_entry_id": "e-sol-whisper", "platform": "wyoming", "disabled_by": None},
                           {"entity_id": "tts.solaris_tts", "config_entry_id": "e-sol-tts", "platform": "wyoming", "disabled_by": None},
                           {"entity_id": "conversation.solaris", "config_entry_id": "e-sol", "platform": "ollama", "disabled_by": None}]
        HA["states"].update({"stt.faster_whisper": {"state": "unavailable"}, "tts.solaris_tts": {"state": "unavailable"},
                             "conversation.solaris": {"state": "unavailable"}, "tts.google_translate_de": {"state": "unknown"},
                             "conversation.home_assistant": {"state": "unknown"}})
        HA["engines"] = {"stt": [{"engine_id": "stt.faster_whisper", "supported_languages": ["de", "en"]}],
                         "tts": [{"engine_id": "tts.piper", "supported_languages": ["de_DE"]}, {"engine_id": "tts.google_translate_de", "supported_languages": ["de"]}]}
        pipe = lambda i, name, stt, tts, conv: {"id": i, "name": name, "language": "de", "stt_engine": stt, "stt_language": "de", "tts_engine": tts,
                                                "tts_language": "de", "tts_voice": "martin", "conversation_engine": conv, "conversation_language": "de",
                                                "wake_word_entity": None, "wake_word_id": None, "prefer_local_intents": False}
        HA["pipelines"] = [pipe("p-sol", "Solaris", "stt.faster_whisper", "tts.solaris_tts", "conversation.solaris"),
                           pipe("p-own", "Eigene", "cloud", "tts.google_translate_de", "conversation.home_assistant")]
        save()

    def wyoming(self):
        return sorted((e["data"]["port"], e["title"]) for e in HA["entries"] if e["domain"] == "wyoming")

    def test_stimme_is_wired_once_and_a_pipeline_whose_engines_went_follows(self):
        self.kraftwerk()
        w, pi, ww = self.ports
        p, handed, log = self.run_hook(frage=self.url, stimme=self.ports, data_dir=self.tmp.name)
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(self.wyoming(), sorted([(w, "faster-whisper"), (10203, "Solaris TTS"), (pi, "piper"), (ww, "openwakeword")]),
                         "Solaris' Whisper entry on Stimme's port is Stimme's; nothing deleted, nothing twice")
        self.assertEqual(HA["reloads"], ["e-sol-whisper"], "the entry that gave up while 10300 was dark was not set up again")
        sol, own = HA["pipelines"]
        self.assertEqual((sol["stt_engine"], sol["tts_engine"], sol["tts_language"], sol["tts_voice"], sol["conversation_engine"]),
                         ("stt.faster_whisper", "tts.piper", "de_DE", None, "conversation.frage"), log)
        self.assertIn('Assist pipeline "Solaris"', log)
        self.assertTrue(sol["prefer_local_intents"], "#557: with Frage as the agent, HA's own intents must answer first")
        self.assertFalse(own["prefer_local_intents"], "a pipeline on HA's own agent is the owner's choice")
        self.assertEqual(own["stt_engine"], "cloud", "a legacy provider was replaced")
        self.assertEqual((own["tts_engine"], own["conversation_engine"]), ("tts.google_translate_de", "conversation.home_assistant"),
                         "a part that works is the owner's choice")
        self.assertEqual(HA["pipeline_updates"], ["p-sol"])
        HA["reloads"].clear()
        p, _, log = self.run_hook(tokens=handed, frage=self.url, stimme=self.ports, data_dir=self.tmp.name)
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(len(self.wyoming()), 4, "a second run added a Wyoming entry")
        self.assertEqual((HA["pipeline_updates"], HA["reloads"]), (["p-sol"], []), "a second run changed something")
        self.assertIn("Stimme's Wyoming entries are there", log)

    def test_without_stimme_nothing_is_wired_and_the_apply_stays_green(self):
        self.kraftwerk()
        p, _, log = self.run_hook(data_dir=self.tmp.name)   # Stimme's ports answer nothing, no Frage
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(len(self.wyoming()), 2)
        self.assertEqual(HA["pipeline_updates"], [], "a pipeline moved to nothing")
        self.assertIn("after laib add stimme: laib apply home-assistant", log)

    def test_unreadable_storage_adds_no_entry_it_might_have_twice_and_is_red(self):
        self.kraftwerk()
        p, handed, log = self.run_hook(stimme=self.ports)   # LAIB_DATA_DIR without HA's .storage
        self.assertEqual(p.returncode, 1, "#557/#588: green with a NOT set up line\n" + log)
        self.assertEqual(len(self.wyoming()), 2)
        self.assertIn("Wyoming for Stimme NOT set up", log)
        self.assertIn("BOTE_HA_TOKEN", handed, "the tokens are handed back before the red")

    def test_home_assistant_restarting_under_the_hook_is_waited_for(self):   # #557: laib add stimme while HA restarted
        self.kraftwerk()
        HA["restarts"] = 1
        p, _, log = self.run_hook(frage=self.url, stimme=self.ports, data_dir=self.tmp.name)
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(len(self.wyoming()), 4, log)
        self.assertNotIn("NOT set up", log)
        self.assertIn("waiting for Home Assistant", log)

    def test_home_assistant_that_stays_away_is_red_not_green(self):
        self.kraftwerk()
        HA["restarts"] = 99
        p, _, log = self.run_hook(frage=self.url, stimme=self.ports, data_dir=self.tmp.name, wait="1")
        self.assertEqual(p.returncode, 1, log)
        self.assertIn("Wyoming for Stimme NOT set up", log)
        self.assertIn("laib apply home-assistant", log)

    def test_a_pipeline_already_on_frage_prefers_has_intents_once(self):   # #557
        self.kraftwerk()
        HA["pipelines"][1].update(conversation_engine="conversation.frage")
        p, handed, log = self.run_hook(frage=self.url, stimme=self.ports, data_dir=self.tmp.name)
        self.assertEqual(p.returncode, 0, log)
        self.assertTrue(all(x["prefer_local_intents"] for x in HA["pipelines"]), HA["pipelines"])
        self.assertIn("prefer_local_intents", log)
        n = len(HA["pipeline_updates"])
        p, _, log = self.run_hook(tokens=handed, frage=self.url, stimme=self.ports, data_dir=self.tmp.name)
        self.assertEqual((p.returncode, len(HA["pipeline_updates"])), (0, n), "a second run changed a pipeline")

    def test_a_stimme_that_does_not_speak_the_language_leaves_the_part(self):
        self.kraftwerk()
        HA["engines"]["tts"] = [{"engine_id": "tts.piper", "supported_languages": ["en_US"]}]
        p, _, log = self.run_hook(frage=self.url, stimme=self.ports, data_dir=self.tmp.name)
        self.assertEqual(p.returncode, 0, log)
        sol = HA["pipelines"][0]
        self.assertEqual((sol["tts_engine"], sol["conversation_engine"]), ("tts.solaris_tts", "conversation.frage"))
        self.assertIn("tts.piper speaks no de", log)


class Zuhause(Hook):
    """#561: box.yml zuhause is Home Assistant's home (latitude, longitude, elevation, time zone,
    country), set through config/core/update only when it differs; without coordinates HA's
    location stays as the owner set it, and the hook says why (Laib never looks an address up).
    A fictitious household; the coordinates never reach the log."""
    HOME = {"ZUHAUSE_STREET": "Musterweg 12", "ZUHAUSE_CITY": "Musterstadt", "ZUHAUSE_COUNTRY": "DE",
            "ZUHAUSE_LATITUDE": "52.1234", "ZUHAUSE_LONGITUDE": "8.0456", "ZUHAUSE_ELEVATION": "63"}

    def test_the_unit_names_zuhause_so_laib_hands_it_to_this_hook_and_applies_it_on_a_move(self):
        self.assertIn("${ZUHAUSE_REV}", (HOOK.parent / "home-assistant.container").read_text())

    def test_the_household_coordinates_become_has_home_once(self):
        fresh(onboarded=("laib", ADMIN_PW))
        p, _, log = self.run_hook(zuhause=self.HOME)
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual({k: HA["core"].get(k) for k in ("latitude", "longitude", "elevation", "time_zone", "country")},
                         {"latitude": 52.1234, "longitude": 8.0456, "elevation": 63, "time_zone": "Europe/Berlin", "country": "DE"}, log)
        self.assertEqual(HA["core_updates"], 1)
        self.assertNotIn("52.1234", log, "the coordinates in the hook's log")
        self.assertNotIn("Musterweg", log, "the street in the hook's log")
        p, _, log = self.run_hook(zuhause=self.HOME)
        self.assertEqual((p.returncode, HA["core_updates"]), (0, 1), "the same home twice is one update: " + log)

    def test_without_coordinates_has_location_stays(self):
        fresh(onboarded=("laib", ADMIN_PW))
        HA["core"].update(latitude=1.5, longitude=2.5)
        p, _, log = self.run_hook(zuhause={"ZUHAUSE_CITY": "Musterstadt", "ZUHAUSE_LATITUDE": "", "ZUHAUSE_LONGITUDE": ""})
        self.assertEqual((p.returncode, HA["core_updates"]), (0, 0), log)
        self.assertIn("no latitude/longitude", log)
        p, _, log = self.run_hook()
        self.assertEqual((p.returncode, HA["core_updates"], HA["core"]["latitude"]), (0, 0, 1.5), log)
        self.assertNotIn("zuhause", log, "without the block the hook has nothing to say about it")


class FakeMA(BaseHTTPRequestHandler):
    """Music Assistant 2.10.7's JSON-RPC API, the part HA's hook speaks: its Home Assistant
    plugin (providers/hass/setup_flow.py: one form, url, verify_ssl, token)"""
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def reply(self, code, body=None):
        data = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        b = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)))
        if MA["tokens"].get((self.headers.get("Authorization") or "")[7:]) is None:
            return self.reply(401, "Authentication required")
        c, a = b["command"], b.get("args") or {}
        form = lambda fid: {"flow_id": fid, "type": "form", "step_id": "user", "errors": {},
                            "entries": [{"key": "url"}, {"key": "verify_ssl"}, {"key": "token"}]}
        if c == "auth/me":
            return self.reply(200, {"username": "laib"})
        if c == "config/providers":
            return self.reply(200, [p for p in MA["providers"].values() if p["domain"] == a.get("provider_domain")])
        if c == "config/providers/setup" and a["provider_domain"] == "hass":
            MA["flows"]["h1"] = None
            return self.reply(200, form("h1"))
        if c == "config/providers/reconfigure":
            MA["flows"]["h2"] = a["instance_id"]
            return self.reply(200, form("h2"))
        if c == "config/flows/submit":
            v, iid = a["values"], MA["flows"].pop(a["flow_id"])
            uid = HA["tokens"].get(v.get("token"))
            if uid is None or not is_admin(uid):   # MA reads HA's users (config/auth/list): an administrator's token
                return self.reply(200, dict(form(a["flow_id"]), errors={"base": "auth_failed"}))
            iid = iid or "hass"
            MA["providers"][iid] = {"instance_id": iid, "domain": "hass", "status": "loaded", "url": v["url"], "token": v["token"]}
            return self.reply(200, {"flow_id": a["flow_id"], "type": "finish", "step_id": "finish", "result": {"instance_id": iid}})
        self.reply(400, f"Invalid Command: {c}")


class MusicAssistant(Hook):
    """#526: Home Assistant's post-deploy connects Music Assistant both ways once MA's
    post-deploy handed it a token: MA's Home Assistant plugin (the family's login into
    MA) and HA's Music Assistant integration, whose browser step the hook answers with
    that token, as MA's /login would."""

    def setUp(self):
        super().setUp()
        self.ma = ThreadingHTTPServer(("127.0.0.1", 0), FakeMA)
        self.ma.daemon_threads = True
        threading.Thread(target=self.ma.serve_forever, daemon=True).start()
        self.addCleanup(self.ma.server_close)
        self.addCleanup(self.ma.shutdown)
        self.url = f"http://127.0.0.1:{self.ma.server_port}"

    def entries(self):
        return [e for e in HA["entries"] if e["domain"] == "music_assistant"]

    def test_music_assistant_is_connected_both_ways_once(self):
        fresh()
        p, handed, log = self.run_hook(tokens={"HA_MUSIC_ASSISTANT_TOKEN": "ma-tok"}, ma=self.url)
        self.assertEqual(p.returncode, 0, log)
        self.assertIn("MUSIC_ASSISTANT_HA_TOKEN", handed)
        self.assertEqual(self.kind(handed["MUSIC_ASSISTANT_HA_TOKEN"]), "admin", "MA reads HA's users: an administrator's token")
        self.assertEqual(HA["names"][handed["MUSIC_ASSISTANT_HA_TOKEN"]], (HA["creds"]["music-assistant"][1], "laib: music-assistant"),
                         "Music Assistant's token on a person's user, not its own (#591)")
        self.assertEqual(HA["users"][HA["creds"]["music-assistant"][1]]["name"], "Music Assistant")
        (plug,) = MA["providers"].values()
        self.assertEqual((plug["url"], plug["token"]), ("https://home.box.test", handed["MUSIC_ASSISTANT_HA_TOKEN"]),
                         "MA sends the browser to the plugin's URL to sign in: 127.0.0.1 would be the phone itself")
        (entry,) = self.entries()
        self.assertEqual(MA["tokens"][entry["token"]], "laib")
        self.assertNotIn("NOT connected", log)
        HA["logins"].clear()
        p, again, log = self.run_hook(tokens=dict(handed, HA_MUSIC_ASSISTANT_TOKEN="ma-tok"), ma=self.url)
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(again, {}, "a working token was replaced")
        self.assertEqual(len(self.entries()), 1, "a second Music Assistant entry")
        self.assertEqual(list(MA["providers"].values())[0]["token"], handed["MUSIC_ASSISTANT_HA_TOKEN"])
        self.assertEqual(HA["logins"], [], "logged in although every token works")

    def test_an_old_plugin_token_on_the_owner_moves_and_goes_once_the_plugin_has_the_new_one(self):
        owner, old = old_box()
        old["MUSIC_ASSISTANT_HA_TOKEN"] = token_for(owner, True, "laib: music-assistant")
        _, handed, _ = self.run_hook(tokens=dict(old, HA_MUSIC_ASSISTANT_TOKEN="ma-tok"), ma=self.url)
        self.assertEqual(MA["providers"]["hass"]["token"], handed["MUSIC_ASSISTANT_HA_TOKEN"])
        self.assertIn(old["MUSIC_ASSISTANT_HA_TOKEN"], HA["tokens"])
        p, again, log = self.run_hook(tokens=dict(old, **handed, HA_MUSIC_ASSISTANT_TOKEN="ma-tok"), ma=self.url)
        self.assertEqual((p.returncode, again), (0, {"HA_RETIRED_TOKENS": ""}), log)
        self.assertNotIn(old["MUSIC_ASSISTANT_HA_TOKEN"], HA["tokens"])
        self.assertEqual(MA["providers"]["hass"]["token"], handed["MUSIC_ASSISTANT_HA_TOKEN"])

    def test_a_plugin_whose_token_ha_revoked_gets_a_new_one(self):
        fresh()
        _, handed, _ = self.run_hook(tokens={"HA_MUSIC_ASSISTANT_TOKEN": "ma-tok"}, ma=self.url)
        del HA["tokens"][handed["MUSIC_ASSISTANT_HA_TOKEN"]]
        MA["providers"]["hass"]["status"] = "auth_required"
        p, again, log = self.run_hook(tokens=dict(handed, HA_MUSIC_ASSISTANT_TOKEN="ma-tok"), ma=self.url)
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(self.kind(again["MUSIC_ASSISTANT_HA_TOKEN"]), "admin")
        self.assertEqual(MA["providers"]["hass"]["token"], again["MUSIC_ASSISTANT_HA_TOKEN"], "the plugin keeps the revoked token")

    def test_an_integration_asking_to_sign_in_again_gets_the_token(self):
        fresh()
        _, handed, _ = self.run_hook(tokens={"HA_MUSIC_ASSISTANT_TOKEN": "ma-tok"}, ma=self.url)
        (entry,) = self.entries()
        del MA["tokens"][entry["token"]]   # HA's MA token expired (MA's long-lived tokens last a year)
        entry["state"], HA["ma_reauth"], HA["flows"]["mr1"] = "setup_error", True, "reauth_confirm"
        p, _, log = self.run_hook(tokens=dict(handed, HA_MUSIC_ASSISTANT_TOKEN="ma-tok"), ma=self.url)
        self.assertEqual(p.returncode, 0, log)
        self.assertEqual(entry["state"], "loaded")
        self.assertIn(entry["token"], MA["tokens"])

    def test_without_music_assistant_nothing_is_wired(self):
        fresh()
        p, handed, log = self.run_hook(ma=self.url)
        self.assertEqual(p.returncode, 0, log)
        self.assertNotIn("MUSIC_ASSISTANT_HA_TOKEN", handed, "an administrator token for a Music Assistant that is not there")
        self.assertEqual((self.entries(), MA["providers"]), ([], {}))
        self.assertIn("laib add music-assistant", log)

    def test_a_music_assistant_that_refuses_its_token_leaves_the_apply_green(self):
        fresh()
        p, handed, log = self.run_hook(tokens={"HA_MUSIC_ASSISTANT_TOKEN": "revoked"}, ma=self.url)
        self.assertEqual(p.returncode, 0, log)
        self.assertIn("Music Assistant NOT connected", log)
        self.assertEqual(self.entries(), [])


if __name__ == "__main__":
    unittest.main()
