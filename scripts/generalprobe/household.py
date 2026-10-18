#!/usr/bin/env python3
"""Generalprobe (#248): the household's synthetic data, and the checks that it survived.

  household.py seed                    once, on the ServiceBay box, through each app's own API
  household.py check --phase before    the same checks against ServiceBay: they must all pass,
                                       or a check that fails later proves nothing
  household.py check --phase after     after the migration, against Laib

Runs on the box as core (python3 is there: ServiceBay's agent needs it), stdlib
only, every app on 127.0.0.1 at the port both control planes give it. The
facts (domain, the admin logins the wizard was given) come as JSON on stdin
for seed; seed writes what it made, synthetic passwords included, to
STATE (outside every data path, so nothing of it migrates). check reads it.

A check marked xfail(<issue>) is a decision of the owner that is not built
yet: it runs, and its failure is reported as expected; if it passes, the
run is red until the marker goes (a fixed gap must not hide behind it).
"""
import base64, hashlib, hmac, http.client, json, os, re, secrets, socket, ssl, struct, subprocess, sys, time, urllib.error, urllib.parse, urllib.request, uuid

STATE = "/var/tmp/generalprobe/household.json"
LOOP = "127.0.0.1"


# --- plumbing ----------------------------------------------------------------

def req(method, url, body=None, headers=None, form=None, files=None, auth=None, timeout=60, ctx=None):
    """(status, bytes, headers); never raises on an HTTP status."""
    h = dict(headers or {})
    data = None
    if files is not None:
        bnd = uuid.uuid4().hex
        parts = []
        for k, v in (form or {}).items():
            parts.append(f'--{bnd}\r\nContent-Disposition: form-data; name="{k}"\r\n\r\n{v}\r\n'.encode())
        for k, (name, blob, ctype) in files.items():
            parts.append(f'--{bnd}\r\nContent-Disposition: form-data; name="{k}"; filename="{name}"\r\nContent-Type: {ctype}\r\n\r\n'.encode() + blob + b"\r\n")
        data = b"".join(parts) + f"--{bnd}--\r\n".encode()
        h["Content-Type"] = f"multipart/form-data; boundary={bnd}"
    elif form is not None:
        data = urllib.parse.urlencode(form).encode()
        h["Content-Type"] = "application/x-www-form-urlencoded"
    elif body is not None:
        data = body if isinstance(body, bytes) else json.dumps(body).encode()
        h.setdefault("Content-Type", "application/json")
    if auth:
        h["Authorization"] = "Basic " + base64.b64encode(f"{auth[0]}:{auth[1]}".encode()).decode()
    r = urllib.request.Request(url, data=data, headers=h, method=method)
    try:
        with urllib.request.urlopen(r, timeout=timeout, context=ctx) as resp:
            return resp.status, resp.read(), resp.headers
    except urllib.error.HTTPError as e:
        return e.code, e.read(), e.headers
    except (urllib.error.URLError, OSError) as e:
        return 0, str(e).encode(), {}


def js(b):
    try:
        return json.loads(b)
    except ValueError:
        return None


def wait(what, fn, tries=60, pause=5):
    last = None
    for _ in range(tries):
        try:
            last = fn()
            if last:
                return last
        except Exception as e:  # an app still starting answers anything
            last = e
        time.sleep(pause)
    raise RuntimeError(f"{what}: gave up ({last})")


def sh(*cmd, inp=None, check=True):
    r = subprocess.run(cmd, input=inp, capture_output=True)
    if check and r.returncode:
        raise RuntimeError(f"{' '.join(cmd[:4])}…: rc {r.returncode}: {r.stderr.decode(errors='replace')[-400:]}")
    return r


def container(*names):
    """the running container whose name is one of names (ServiceBay's kube pods
    name them <pod>-<container>, Laib's Quadlet units systemd-<unit>)"""
    have = sh("podman", "ps", "--format", "{{.Names}}").stdout.decode().split()
    for n in names:
        if n in have:
            return n
    raise RuntimeError(f"none of {names} runs (running: {' '.join(have)})")


def pw():
    return "Gp-" + secrets.token_urlsafe(15)


# --- LLDAP: GraphQL for users and groups, LDAP password modify for the password

def ber(tag, body):
    n = len(body)
    k = (n.bit_length() + 7) // 8
    return bytes([tag]) + (bytes([n]) if n < 128 else bytes([0x80 | k]) + n.to_bytes(k, "big")) + body


def tlv(b, i):
    tag, n, i = b[i], b[i + 1], i + 2
    if n & 0x80:
        n, i = int.from_bytes(b[i:i + (n & 0x7F)], "big"), i + (n & 0x7F)
    return tag, b[i:i + n], i + n


def ldap(dn, password, op=None, port=3890):
    """bind as dn, then op; None or the first error"""
    with socket.create_connection((LOOP, port), timeout=10) as s:
        f = s.makefile("rb")
        bind = ber(0x60, ber(0x02, b"\x03") + ber(0x04, dn.encode()) + ber(0x80, password.encode()))
        for mid, rq in enumerate([bind] + ([op] if op else []), 1):
            s.sendall(ber(0x30, ber(0x02, bytes([mid])) + rq))
            head = f.read(2)
            n = int.from_bytes(f.read(head[1] & 0x7F), "big") if head[1] & 0x80 else head[1]
            msg = f.read(n)
            _, res, _ = tlv(msg, tlv(msg, 0)[2])
            _, code, i = tlv(res, 0)
            if code != b"\x00":
                return f"LDAP result {int.from_bytes(code, 'big')}"
    return None


# --- Bitwarden's client crypto, so "unlocks" means what the app means by it --

def b64(b):
    return base64.b64encode(b).decode()


def hkdf_expand(prk, info):
    return hmac.new(prk, info + b"\x01", hashlib.sha256).digest()


def aes(key, iv, data, decrypt=False):
    # FCOS has openssl and no Python crypto; the key and iv go on argv of a
    # synthetic test account only
    args = ["openssl", "enc", "-aes-256-cbc", "-K", key.hex(), "-iv", iv.hex()] + (["-d"] if decrypt else [])
    return sh(*args, inp=data).stdout


def enc_string(enc, mac, plain):
    iv = secrets.token_bytes(16)
    ct = aes(enc, iv, plain)
    return f"2.{b64(iv)}|{b64(ct)}|{b64(hmac.new(mac, iv + ct, hashlib.sha256).digest())}"


def dec_string(enc, mac, s):
    t, rest = s.split(".", 1)
    iv, ct, m = (base64.b64decode(x) for x in rest.split("|"))
    if t != "2" or not hmac.compare_digest(hmac.new(mac, iv + ct, hashlib.sha256).digest(), m):
        raise ValueError("MAC does not match: not the key this was sealed with")
    return aes(enc, iv, ct, decrypt=True)


def bw_keys(email, password, iterations):
    mk = hashlib.pbkdf2_hmac("sha256", password.encode(), email.lower().encode(), iterations, 32)
    return mk, b64(hashlib.pbkdf2_hmac("sha256", mk, password.encode(), 1, 32)), hkdf_expand(mk, b"enc"), hkdf_expand(mk, b"mac")


# --- TOTP (RFC 6238), for Authelia's second factor ---------------------------

def totp(secret_b32, t=None):
    key = base64.b32decode(secret_b32 + "=" * (-len(secret_b32) % 8))
    d = hmac.new(key, struct.pack(">Q", int((t or time.time()) // 30)), hashlib.sha1).digest()
    o = d[-1] & 15
    return "%06d" % ((struct.unpack(">I", d[o:o + 4])[0] & 0x7FFFFFFF) % 1000000)


def png(rgb, size=24):
    """a small, valid, unique PNG (Immich keys assets by their checksum)"""
    raw = b"".join(b"\x00" + bytes(rgb) * size for _ in range(size))
    chunk = lambda t, d: struct.pack(">I", len(d)) + t + d + struct.pack(">I", __import__("zlib").crc32(t + d))
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", size, size, 8, 2, 0, 0, 0)) + chunk(b"IDAT", __import__("zlib").compress(raw)) + chunk(b"IEND", b"")


# --- ServiceBay's own install path -------------------------------------------

def servicebay_install(f, items):
    """Templates through ServiceBay's API as its wizard and agent-cli do
    (POST /api/install/template, one job at a time), with the values the
    operator would have typed into the wizard; everything else ServiceBay
    generates and seals into config.json itself."""
    base = "http://127.0.0.1:5888"
    st, _, h = req("POST", base + "/api/auth/login", {"username": f["sb_user"], "password": f["sb_password"]}, headers={"Origin": base})
    cookie = "; ".join(c.split(";")[0] for c in (h.get_all("Set-Cookie") or []))
    if st not in (200, 204) or "session=" not in cookie:
        raise RuntimeError(f"ServiceBay login: HTTP {st}")
    # the operator's browser session: a cookie, and the Origin ServiceBay's CSRF gate wants
    bearer = {"Cookie": cookie, "Origin": base}
    # the agent is connected when ServiceBay sees the box's containers through it
    last = []
    def agent():
        last[:] = req("GET", base + "/api/containers", headers=bearer)[:2]
        return b'servicebay' in last[1]
    try:
        wait("ServiceBay's agent", agent, tries=90, pause=2)
    except RuntimeError:
        print(f"GET /api/containers: HTTP {last[0]} {last[1][:400]!r}", file=sys.stderr)
        raise
    for name in items:
        src, _, tmpl = name.rpartition("/")
        body = {"template": tmpl, "variables": f.get("vars", {}).get(tmpl, {})}
        if src:
            body["templateSource"] = src
        for _ in range(60):   # 409: the previous job is still finishing
            st, b, _ = req("POST", base + "/api/install/template", body, headers=bearer)
            if st != 409:
                break
            time.sleep(5)
        job = (js(b) or {}).get("jobId")
        if not job:
            raise RuntimeError(f"install {name}: HTTP {st} {b[:300]!r}")
        print(f"servicebay install {name}: job {job}", flush=True)
        off, t0 = 0, time.time()
        while True:
            st, b, _ = req("GET", f"{base}/api/install/progress?jobId={job}&logsSince={off}")
            d = js(b) or {}
            logs = d.get("logs") or ""
            if logs:
                sys.stdout.write("".join("    " + l + "\n" for l in logs.splitlines()))
                sys.stdout.flush()
            off = d.get("logsOffset", off)
            phase = (d.get("job") or {}).get("phase")
            if phase == "needs_credentials":
                # ServiceBay asks for NPM's admin login when its own check ran before
                # NPM had seeded it (a slow first start); the operator types the
                # login the wizard was given, once NPM answers
                nv = f["vars"]["nginx"]
                wait("NPM's API", lambda: req("GET", "http://127.0.0.1:81/api/")[0] == 200, tries=60)
                st, b, _ = req("POST", base + "/api/install/credentials", {"jobId": job, "email": nv["NGINX_ADMIN_EMAIL"], "password": nv["NGINX_ADMIN_PASSWORD"]}, headers=bearer)
                print(f"    (the NPM login typed in: HTTP {st})", flush=True)
                time.sleep(5)
                continue
            if phase in ("done", "error", "aborted", "crashed") or time.time() - t0 > 1500:
                break
            time.sleep(5)
        _, b, _ = req("GET", f"{base}/api/install/current?jobId={job}", headers=bearer)
        for w in (js(b) or {}).get("warnings") or []:
            print(f"    warning: {w}")
        if phase != "done":
            raise RuntimeError(f"install {name}: phase {phase}: {(d.get('job') or {}).get('error')}")
        print(f"servicebay install {name}: done in {int(time.time() - t0)} s", flush=True)
    return bearer


# --- seed ----------------------------------------------------------------------

def lldap_token(f):
    st, b, _ = req("POST", f"http://{LOOP}:17170/auth/simple/login", {"username": "admin", "password": f["lldap_admin"]})
    if st != 200:
        raise RuntimeError(f"LLDAP admin login: HTTP {st}")
    return js(b)["token"]


def gql(token, q, v=None):
    st, b, _ = req("POST", f"http://{LOOP}:17170/api/graphql", {"query": q, "variables": v or {}}, headers={"Authorization": "Bearer " + token})
    d = js(b) or {}
    if st != 200 or d.get("errors"):
        raise RuntimeError(f"LLDAP GraphQL: HTTP {st} {d.get('errors')}")
    return d["data"]


def seed_people(f, s):
    tok = lldap_token(f)
    groups = {g["displayName"]: g["id"] for g in gql(tok, "{ groups { id displayName } }")["groups"]}
    for g in ("admins", "family"):
        if g not in groups:   # ServiceBay's auth post-deploy seeds both; say so if it did not
            print(f"LLDAP had no group {g}: ServiceBay's seed did not run; created")
            groups[g] = gql(tok, "mutation($n:String!){ createGroup(name:$n){ id } }", {"n": g})["createGroup"]["id"]
    base_dn = f["base_dn"]
    for uid, group in (("anna", "admins"), ("ben", "family"), ("clara", "family")):
        p = s["people"].setdefault(uid, {"password": pw(), "group": group, "email": f"{uid}@{f['domain']}"})
        gql(tok, "mutation($u:CreateUserInput!){ createUser(user:$u){ id } }", {"u": {"id": uid, "email": p["email"], "displayName": uid.title()}})
        gql(tok, "mutation($u:String!,$g:Int!){ addUserToGroup(userId:$u, groupId:$g){ ok } }", {"u": uid, "g": groups[group]})
        dn = f"uid={uid},ou=people,{base_dn}"
        op = ber(0x77, ber(0x80, b"1.3.6.1.4.1.4203.1.11.1") + ber(0x81, ber(0x30, ber(0x80, dn.encode()) + ber(0x82, p["password"].encode()))))
        e = ldap(f"uid=admin,ou=people,{base_dn}", f["lldap_admin"], op) or ldap(dn, p["password"])
        if e:
            raise RuntimeError(f"LLDAP password for {uid}: {e}")
        print(f"LLDAP: {uid} in {group}")
    # anna has a second factor: TOTP through Authelia's own CLI, in its own
    # storage, sealed with its storage encryption key (#228)
    secret = base64.b32encode(secrets.token_bytes(20)).decode().rstrip("=")
    ctr = container("auth-authelia")
    r = sh("podman", "exec", ctr, "authelia", "storage", "user", "totp", "generate", "anna", "--secret", secret, "--config", "/config/configuration.yml", check=False)
    if r.returncode:
        r = sh("podman", "exec", ctr, "authelia", "storage", "user", "totp", "generate", "anna", "--secret", secret)
    s["people"]["anna"]["totp"] = secret
    print("Authelia: TOTP registered for anna")


def authelia_login(f, s, uid, password, base, ctx=None, second=True):
    """first factor (and, for anna, TOTP) the way the portal does it; an error string or None"""
    host = f"auth.{f['domain']}"
    hd = {"X-Forwarded-Proto": "https", "X-Forwarded-Host": host, "X-Forwarded-For": "10.0.2.2", "Origin": f"https://{host}"}
    if base.startswith("http://"):
        hd["Host"] = host
    target = f"https://home.{f['domain']}/"
    st, b, h = req("POST", base + "/api/firstfactor", {"username": uid, "password": password, "keepMeLoggedIn": False, "targetURL": target}, headers=hd, ctx=ctx)
    if st != 200 or (js(b) or {}).get("status") != "OK":
        return f"first factor: HTTP {st} {b[:120]!r}"
    t = s["people"].get(uid, {}).get("totp")
    if t and second:
        hd["Cookie"] = "; ".join(c.split(";")[0] for c in (h.get_all("Set-Cookie") or []))
        st, b, _ = req("POST", base + "/api/secondfactor/totp", {"token": totp(t), "targetURL": target}, headers=hd, ctx=ctx)
        if st != 200 or (js(b) or {}).get("status") != "OK":
            return f"TOTP: HTTP {st} {b[:120]!r}"
    return None


def immich(path, method="GET", body=None, token=None, **kw):
    h = {"Authorization": "Bearer " + token} if token else {}
    st, b, _ = req(method, f"http://{LOOP}:2283/api{path}", body, headers=h, **kw)
    return st, (js(b) if b[:1] in (b"{", b"[") else b)


def immich_login(email, password):
    st, d = immich("/auth/login", "POST", {"email": email, "password": password})
    if st not in (200, 201):
        raise AssertionError(f"Immich login {email}: HTTP {st}")
    return d["accessToken"]


def seed_photos(f, s):
    wait("Immich", lambda: immich("/server/ping")[0] == 200)
    admin = immich_login(f["immich_email"], f["immich_password"])
    p = s["photos"] = {"email": f"anna@{f['domain']}", "password": pw(), "uploads": {}, "library": {}}
    st, d = immich("/admin/users", "POST", {"email": p["email"], "password": p["password"], "name": "Anna", "shouldChangePassword": False}, admin)
    if st not in (200, 201):
        raise RuntimeError(f"Immich user: HTTP {st} {d}")
    uid = d["id"]
    tok = immich_login(p["email"], p["password"])
    ids = []
    for i, rgb in enumerate(((200, 30, 30), (30, 200, 30), (30, 30, 200))):
        blob, name = png(rgb), f"generalprobe-{i}.png"
        st, d = immich("/assets", "POST", None, tok, files={"assetData": (name, blob, "image/png")},
                       form={"deviceAssetId": f"gp-{i}", "deviceId": "generalprobe", "fileCreatedAt": "2024-05-0%dT10:00:00.000Z" % (i + 1), "fileModifiedAt": "2024-05-0%dT10:00:00.000Z" % (i + 1)})
        if st not in (200, 201):
            raise RuntimeError(f"Immich upload: HTTP {st} {d}")
        ids.append(d["id"])
        p["uploads"][d["id"]] = hashlib.sha256(blob).hexdigest()
    st, d = immich("/albums", "POST", {"albumName": "Generalprobe Urlaub", "assetIds": ids}, tok)
    if st not in (200, 201):
        raise RuntimeError(f"Immich album: HTTP {st} {d}")
    p["album"] = d["id"]
    # an external library under the share, at the box's path: <share>/<user>/photos
    lib = os.path.join(f["data_dir"], "file-share/data/anna/photos")
    os.makedirs(lib, exist_ok=True)
    for i, rgb in enumerate(((240, 200, 0), (0, 200, 240))):
        blob = png(rgb, 32)
        open(os.path.join(lib, f"bibliothek-{i}.png"), "wb").write(blob)
        p["library"][f"bibliothek-{i}.png"] = hashlib.sha256(blob).hexdigest()
    st, d = immich("/libraries", "POST", {"ownerId": uid, "name": "Annas Fotos", "importPaths": ["/mnt/photos/anna/photos"]}, admin)
    if st not in (200, 201):
        raise RuntimeError(f"Immich library: HTTP {st} {d}")
    immich(f"/libraries/{d['id']}/scan", "POST", {}, admin)
    wait("Immich library scan", lambda: (immich(f"/libraries/{d['id']}/statistics", token=admin)[1] or {}).get("photos", 0) >= 2, pause=3)
    print(f"Immich: anna, 3 photos in an album, 2 in the library {lib}")


def seed_documents(f, s):
    auth = (f["paperless_user"], f["paperless_password"])
    wait("Paperless", lambda: req("GET", f"http://{LOOP}:8000/api/documents/", auth=auth)[0] == 200, tries=90)
    s["documents"] = {}
    for word, text in (("Zebrastreifen", "Mietvertrag Generalprobe Zebrastreifen 4711"), ("Kolibri", "Stromrechnung Generalprobe Kolibri 2024")):
        st, b, _ = req("POST", f"http://{LOOP}:8000/api/documents/post_document/", auth=auth, form={"title": f"Generalprobe {word}"},
                       files={"document": (f"{word}.txt", (text + "\n").encode(), "text/plain")})
        if st != 200:
            raise RuntimeError(f"Paperless upload: HTTP {st} {b[:200]!r}")
        def filed():   # the document is filed once Paperless lists it under its title
            d = js(req("GET", f"http://{LOOP}:8000/api/documents/?title__icontains=Generalprobe%20{word}", auth=auth)[1]) or {}
            return (d.get("results") or [None])[0]
        try:
            doc = wait(f"Paperless files {word}", filed, tries=120, pause=5)
        except RuntimeError:
            print(req("GET", f"http://{LOOP}:8000/api/tasks/", auth=auth)[1][:1500].decode(errors="replace"), file=sys.stderr)
            print(sh("sh", "-c", "podman ps --format '{{.Names}}' | grep -i paperless | head -3 | xargs -n1 podman logs --tail 30 2>&1", check=False).stdout.decode()[-4000:], file=sys.stderr)
            raise
        s["documents"][word] = doc["id"]
    print("Paperless: 2 documents")


def bw_login(email, password):
    st, b, _ = req("POST", f"http://{LOOP}:8222/identity/accounts/prelogin", {"email": email})
    it = (js(b) or {}).get("kdfIterations") or (js(b) or {}).get("KdfIterations") or 600000
    mk, h, enc, mac = bw_keys(email, password, it)
    st, b, _ = req("POST", f"http://{LOOP}:8222/identity/connect/token", form={
        "grant_type": "password", "username": email, "password": h, "scope": "api offline_access", "client_id": "web",
        "deviceType": "10", "deviceIdentifier": str(uuid.uuid5(uuid.NAMESPACE_DNS, "generalprobe.laib")), "deviceName": "generalprobe"})
    d = js(b) or {}
    if st != 200:
        raise AssertionError(f"Vaultwarden login: HTTP {st} {d.get('error_description') or d.get('message') or b[:120]!r}")
    key = d.get("Key") or d.get("key")
    sym = dec_string(enc, mac, key)   # the unlock: the master password opens the account key
    return d["access_token"], sym[:32], sym[32:]


def seed_vault(f, s):
    wait("Vaultwarden", lambda: req("GET", f"http://{LOOP}:8222/alive")[0] == 200)
    v = s["vault"] = {"email": f"ben@{f['domain']}", "password": pw(), "item": "Generalprobe WLAN-Schlüssel"}
    mk, h, enc, mac = bw_keys(v["email"], v["password"], 600000)
    sym = secrets.token_bytes(64)
    body = {"email": v["email"], "name": "Ben", "masterPasswordHash": h, "masterPasswordHint": None, "key": enc_string(enc, mac, sym),
            "kdf": 0, "kdfIterations": 600000, "keys": None}
    # the web vault's sign-up: a verification token (handed back directly when the
    # box sends no mail), then register/finish; the server keeps the sealed keys as given
    st, b, _ = req("POST", f"http://{LOOP}:8222/identity/accounts/register/send-verification-email",
                   {"email": v["email"], "name": "Ben", "receiveMarketingEmails": False})
    token = (js(b) if b[:1] == b'"' else b.decode(errors="replace").strip()) if st == 200 else None   # plain text unless Accept: json
    if token:
        fin = {"email": v["email"], "emailVerificationToken": token, "masterPasswordHash": h, "masterPasswordHint": None,
               "userSymmetricKey": body["key"], "kdf": 0, "kdfIterations": 600000,
               "userAsymmetricKeys": {"publicKey": b64(secrets.token_bytes(294)), "encryptedPrivateKey": enc_string(enc, mac, secrets.token_bytes(64))}}
        st, b, _ = req("POST", f"http://{LOOP}:8222/identity/accounts/register/finish", fin)
    else:
        st, b, _ = req("POST", f"http://{LOOP}:8222/identity/accounts/register", body)
    if st not in (200, 204):
        raise RuntimeError(f"Vaultwarden register: HTTP {st} {b[:200]!r}")
    tok, e2, m2 = bw_login(v["email"], v["password"])
    uid = (js(req("GET", f"http://{LOOP}:8222/api/accounts/profile", headers={"Authorization": "Bearer " + tok})[1]) or {}).get("id")
    st, b, _ = req("POST", f"http://{LOOP}:8222/api/ciphers", {"type": 2, "encryptedFor": uid, "name": enc_string(e2, m2, v["item"].encode()), "notes": None,
                   "secureNote": {"type": 0}, "favorite": False, "folderId": None, "organizationId": None}, headers={"Authorization": "Bearer " + tok})
    if st != 200:
        raise RuntimeError(f"Vaultwarden item: HTTP {st} {b[:200]!r}")
    print("Vaultwarden: ben with one item")


EVENT = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//generalprobe//\r\nBEGIN:VEVENT\r\nUID:gp-termin@laib\r\nDTSTAMP:20241001T080000Z\r\nDTSTART:20241224T170000Z\r\nDTEND:20241224T200000Z\r\nSUMMARY:Bescherung Generalprobe\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
CARD = "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:gp-kontakt@laib\r\nFN:Oma Generalprobe\r\nN:Generalprobe;Oma;;;\r\nTEL:+49301234567\r\nEND:VCARD\r\n"


def seed_dav(f, s):
    a = ("anna", s["people"]["anna"]["password"])
    u = f"http://{LOOP}:5232/anna"
    mkcol = ('<?xml version="1.0"?><D:mkcol xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav"><D:set><D:prop>'
             '<D:resourcetype><D:collection/><C:addressbook/></D:resourcetype><D:displayname>Kontakte</D:displayname></D:prop></D:set></D:mkcol>')
    for m, path, body, ct in (("MKCALENDAR", "/kalender/", b"", None), ("PUT", "/kalender/gp-termin.ics", EVENT.encode(), "text/calendar"),
                              ("MKCOL", "/kontakte/", mkcol.encode(), "application/xml"), ("PUT", "/kontakte/gp-kontakt.vcf", CARD.encode(), "text/vcard")):
        st, b, _ = req(m, u + path, body, auth=a, headers={"Content-Type": ct} if ct else {})
        if st not in (200, 201, 204):
            raise RuntimeError(f"Radicale {m} {path} as anna: HTTP {st} {b[:200]!r}")
    print("Radicale: anna's calendar and address book")


def ha(path, token, method="GET", body=None):
    st, b, _ = req(method, f"http://{LOOP}:8123/api{path}", body, headers={"Authorization": "Bearer " + token})
    return st, js(b)


def ha_token(f, s):
    """the long-lived token ServiceBay's post-deploy saved, or a session through
    HA's own login flow as the onboarded admin; either proves HA's auth store"""
    cfg = os.path.join(f["data_dir"], "home-assistant/homeassistant")
    for n in (".solaris-long-lived-token", ".solilos-long-lived-token"):
        p = os.path.join(cfg, n)
        if os.path.exists(p) and open(p).read().strip():
            return open(p).read().strip()
    cid = "http://127.0.0.1:8123/"
    st, b, _ = req("POST", f"http://{LOOP}:8123/auth/login_flow", {"client_id": cid, "handler": ["homeassistant", None], "redirect_uri": cid})
    flow = (js(b) or {}).get("flow_id")
    st, b, _ = req("POST", f"http://{LOOP}:8123/auth/login_flow/{flow}", {"client_id": cid, "username": f["ha_user"], "password": f["ha_password"]})
    code = (js(b) or {}).get("result")
    st, b, _ = req("POST", f"http://{LOOP}:8123/auth/token", form={"client_id": cid, "grant_type": "authorization_code", "code": code or ""})
    tok = (js(b) or {}).get("access_token")
    if not tok:
        raise AssertionError(f"HA login as {f['ha_user']}: HTTP {st} {b[:150]!r}")
    return tok


def seed_home(f, s):
    cfg = os.path.join(f["data_dir"], "home-assistant/homeassistant")
    wait("Home Assistant's login", lambda: req("GET", f"http://{LOOP}:8123/manifest.json")[0] == 200, tries=90)
    tok = ha_token(f, s)
    wait("Home Assistant", lambda: ha("/", tok)[0] == 200, tries=90)
    st, d = ha("/config/automation/config/generalprobe_licht", tok, "POST", {
        "id": "generalprobe_licht", "alias": "Generalprobe Licht", "description": "",
        "triggers": [{"trigger": "time", "at": "07:00:00"}], "conditions": [],
        "actions": [{"action": "persistent_notification.create", "data": {"message": "Guten Morgen"}}], "mode": "single"})
    if st != 200:
        raise RuntimeError(f"HA automation: HTTP {st} {d}")
    st, d = ha("/config/config_entries/flow", tok, "POST", {"handler": "shopping_list", "show_advanced_options": False})
    if st == 200 and d.get("type") == "form":
        ha(f"/config/config_entries/flow/{d['flow_id']}", tok, "POST", {})
    wait("HA automation", lambda: ha("/states/automation.generalprobe_licht", tok)[0] == 200, pause=2)
    # The Z-Wave JS entry needs a stick to be made through its flow; the box's
    # entry is what HA wrote when it was made: zwave_js at ServiceBay's 3001.
    # Written here with HA stopped (HA rewrites the file from memory).
    sh("systemctl", "--user", "stop", "home-assistant.service")
    p = os.path.join(cfg, ".storage/core.config_entries")
    doc = json.load(open(p))
    doc["data"]["entries"].append({"entry_id": "generalprobezwave0000000000000", "version": 1, "minor_version": 1, "domain": "zwave_js",
        "title": "Z-Wave JS", "data": {"url": "ws://127.0.0.1:3001", "usb_path": None, "integration_created_addon": False, "use_addon": False},
        "options": {}, "pref_disable_new_entities": False, "pref_disable_polling": False, "source": "user", "unique_id": "4242424242",
        "disabled_by": None, "created_at": "2024-01-01T00:00:00+00:00", "modified_at": "2024-01-01T00:00:00+00:00", "discovery_keys": {}, "subentries": []})
    json.dump(doc, open(p, "w"), indent=2)
    sh("systemctl", "--user", "start", "home-assistant.service")
    wait("Home Assistant again", lambda: req("GET", f"http://{LOOP}:8123/manifest.json")[0] == 200, tries=90)
    tok = ha_token(f, s)
    wait("Home Assistant's API", lambda: ha("/", tok)[0] == 200, tries=30)
    s["home"] = {"entries": sorted(e["domain"] for e in doc["data"]["entries"])}
    print(f"Home Assistant: automation, config entries {s['home']['entries']}")


def mqtt(args, check=False):
    return sh("podman", "run", "--rm", "--network", "host", "docker.io/library/eclipse-mosquitto:2", *args, check=check)


def seed_mqtt(f, s):
    r = mqtt(["mosquitto_pub", "-h", LOOP, "-u", f["mqtt_user"], "-P", f["mqtt_password"], "-t", "generalprobe/wohnzimmer/licht", "-m", "an", "-r", "-q", "1"])
    if r.returncode:
        raise RuntimeError(f"MQTT publish: {r.stderr.decode()[-200:]}")
    print("MQTT: a retained message on generalprobe/wohnzimmer/licht")


SMBCLIENT = "localhost/generalprobe-smbclient"


def smb(user, password, cmd, vol=()):
    """smbclient against the box's share, from a client container (CoreOS has none)"""
    if sh("podman", "image", "exists", SMBCLIENT, check=False).returncode:
        sh("podman", "build", "-q", "-t", SMBCLIENT, "-", inp=b"FROM docker.io/library/alpine:3.20\nRUN apk add --no-cache samba-client\n")
    return sh("podman", "run", "--rm", "--network", "host", *vol, SMBCLIENT, "smbclient",
              "//127.0.0.1/data", "-U", f"{user}%{password}", "-m", "SMB3", "-c", cmd, check=False)


def seed_share(f, s, bearer):
    # a file in the share as Syncthing or File Browser leaves one; the share user reads it
    open(os.path.join(f["data_dir"], "file-share/data/einkaufsliste.txt"), "w").write("Milch, Brot, Generalprobe\n")
    r = smb(f["share_user"], f["share_password"], "get einkaufsliste.txt /dev/stdout")
    if r.returncode or b"Generalprobe" not in r.stdout:
        raise RuntimeError(f"Samba get as {f['share_user']}: {(r.stdout + r.stderr).decode()[-300:]}")
    # the family's own logins: ServiceBay syncs LLDAP users into Samba; anna's
    # Samba password is set to her LLDAP one (the owner's decision, #229)
    base = "http://127.0.0.1:5888"
    req("POST", base + "/api/system/file-share/samba/users", {}, headers=bearer)
    st, b, _ = req("POST", base + "/api/system/file-share/samba/users/anna/set-password", {"password": s["people"]["anna"]["password"]}, headers=bearer)
    if st != 200 and b"useradd" in b:
        # ServiceBay 5.44.3's sync calls useradd, which servercontainers/samba (Alpine)
        # no longer has; what it means to do, with Alpine's adduser: a POSIX account
        # without home or shell, then smbpasswd -a into the passdb in samba-private
        print(f"ServiceBay could not add anna to Samba ({b[:120]!r}); its steps by hand with adduser")
        sh("podman", "exec", "file-share-samba", "adduser", "-D", "-H", "-s", "/sbin/nologin", "anna", check=False)
        pw_ = s["people"]["anna"]["password"]
        sh("podman", "exec", "-i", "file-share-samba", "smbpasswd", "-s", "-a", "anna", inp=f"{pw_}\n{pw_}\n".encode())
    elif st != 200:
        raise RuntimeError(f"ServiceBay: anna's Samba password: HTTP {st} {b[:200]!r}")
    print("Samba: a file in the share, anna's own login")


def syncthing_id():
    for ctr in ("file-share-syncthing", "systemd-file-share-syncthing"):
        r = sh("podman", "exec", ctr, "sh", "-c", "syncthing --device-id 2>/dev/null || syncthing device-id", check=False)
        if r.returncode == 0 and r.stdout.strip():
            return r.stdout.decode().strip()
    raise AssertionError("no Syncthing answers with its device ID")


def matter_files(d):
    out = {}
    for root, _, fs in os.walk(d):
        for n in fs:
            p = os.path.join(root, n)
            out[os.path.relpath(p, d)] = sh("podman", "unshare", "sha256sum", p).stdout.decode().split()[0]
    return out


def seed(f):
    os.makedirs(os.path.dirname(STATE), exist_ok=True)
    s = {"facts": f, "people": {}}
    bearer = servicebay_install(f, [])
    for step in (seed_people, seed_photos, seed_documents, seed_vault, seed_dav, seed_home, seed_mqtt):
        step(f, s)
    seed_share(f, s, bearer)
    s["syncthing"] = syncthing_id()
    mdir = os.path.join(f["data_dir"], "home-assistant/matter-server")
    sh("podman", "unshare", "sh", "-c", f"mkdir -p {mdir} && test -n \"$(ls -A {mdir})\" || echo generalprobe > {mdir}/generalprobe-fabric.json")
    s["matter"] = matter_files(mdir)
    json.dump(s, open(STATE, "w"), indent=1)
    print(f"seeded; state in {STATE}")


# --- checks ------------------------------------------------------------------
# Each runs in both phases and raises AssertionError with what the family
# would notice. XFAIL: decided by the owner, not built yet; only after.

XFAIL = {}   # every owner decision of #229 is built now (#263, #296)


def check_logins(f, s, phase):
    for uid, p in s["people"].items():
        if e := authelia_login(f, s, uid, p["password"], f"http://{LOOP}:9091"):
            raise AssertionError(f"{uid} cannot log in at Authelia with the old password: {e}")
    if authelia_login(f, s, "clara", "falsch-" + pw(), f"http://{LOOP}:9091", second=False) is None:
        raise AssertionError("Authelia accepted a wrong password: the check proves nothing")
    if phase == "after":   # and through Laib's Caddy, with the box's own CA
        time.sleep(31 - time.time() % 30)   # Authelia takes a TOTP code once: the next window
        ca = "/var/tmp/generalprobe/caddy-root.crt"
        sh("sh", "-c", f"cp {f['data_dir']}/caddy/data/caddy/pki/authorities/local/root.crt {ca}")
        ctx = ssl.create_default_context(cafile=ca)
        for uid, p in s["people"].items():
            if e := authelia_login(f, s, uid, p["password"], f"https://auth.{f['domain']}", ctx=ctx):
                raise AssertionError(f"{uid} cannot log in at https://auth.{f['domain']} (Caddy): {e}")
    return f"{len(s['people'])} people log in with their old passwords, anna with her TOTP too"


def check_photos(f, s, phase):
    p = s["photos"]
    tok = immich_login(p["email"], p["password"])
    st, albums = immich("/albums", token=tok)
    al = next((a for a in albums or [] if a["id"] == p["album"]), None)
    if not al or al.get("assetCount") != 3:
        raise AssertionError(f"the album is gone or not whole: {al and al.get('assetCount')}")
    for aid, want in p["uploads"].items():
        st, blob = immich(f"/assets/{aid}/original", token=tok)
        if st != 200 or hashlib.sha256(blob).hexdigest() != want:
            raise AssertionError(f"photo {aid}: HTTP {st}, not the uploaded bytes")
    return "anna logs in; her album with its 3 photos, the originals byte for byte"


def check_library(f, s, phase):
    p = s["photos"]
    tok = immich_login(p["email"], p["password"])
    st, d = immich("/search/metadata", "POST", {"size": 200}, tok)
    items = {a["originalFileName"]: a for a in (d or {}).get("assets", {}).get("items", [])}
    for name, want in p["library"].items():
        a = items.get(name)
        if not a:
            raise AssertionError(f"library photo {name} is not in Immich")
        st, blob = immich(f"/assets/{a['id']}/original", token=tok)
        if a.get("isOffline") or st != 200 or hashlib.sha256(blob).hexdigest() != want:
            raise AssertionError(f"library photo {name} ({a.get('originalPath')}): offline {a.get('isOffline')}, HTTP {st}")
    return "the external library's 2 photos are online, the originals byte for byte"


def check_documents(f, s, phase):
    auth = (f["paperless_user"], f["paperless_password"])
    for word, doc in s["documents"].items():
        st, b, _ = req("GET", f"http://{LOOP}:8000/api/documents/?query={word.lower()}", auth=auth)
        ids = [r["id"] for r in (js(b) or {}).get("results", [])]
        if st != 200 or doc not in ids:
            raise AssertionError(f"full-text search for {word}: HTTP {st}, found {ids}, want {doc}")
    return f"{len(s['documents'])} documents found by full-text search"


def check_vault(f, s, phase):
    v = s["vault"]
    tok, enc, mac = bw_login(v["email"], v["password"])
    st, b, _ = req("GET", f"http://{LOOP}:8222/api/sync", headers={"Authorization": "Bearer " + tok})
    names = [dec_string(enc, mac, c.get("name") or c.get("Name")).decode() for c in (js(b) or {}).get("ciphers", (js(b) or {}).get("Ciphers", []))]
    if v["item"] not in names:
        raise AssertionError(f"the item is not in the vault: {names}")
    return "ben unlocks his vault with the old master password; the item decrypts"


def check_dav(f, s, phase):
    a = ("anna", s["people"]["anna"]["password"])
    for path, uid in (("/kalender/gp-termin.ics", "gp-termin@laib"), ("/kontakte/gp-kontakt.vcf", "gp-kontakt@laib")):
        st, b, _ = req("GET", f"http://{LOOP}:5232/anna" + path, auth=a)
        if st != 200 or uid.encode() not in b:
            raise AssertionError(f"Radicale {path} with anna's LLDAP login: HTTP {st}")
    return "anna reads her event and her contact with her LLDAP login"


def check_home(f, s, phase):
    cfg = os.path.join(f["data_dir"], "home-assistant/homeassistant")
    wait("Home Assistant", lambda: req("GET", f"http://{LOOP}:8123/manifest.json")[0] == 200, tries=60)
    tok = ha_token(f, s)
    # a restarted HA answers before it has set up its integrations: wait until it runs
    try:
        wait("HA running", lambda: (ha("/config", tok)[1] or {}).get("state") == "RUNNING", tries=60, pause=5)
    except RuntimeError as e:
        raise AssertionError(str(e))
    st, _ = ha("/states/automation.generalprobe_licht", tok)
    if st != 200:
        jl = sh("sh", "-c", "for c in home-assistant-homeassistant systemd-home-assistant; do journalctl --user --no-pager -o cat CONTAINER_NAME=$c --since -20min; done", check=False).stdout
        errs = [l for l in jl.decode(errors="replace").splitlines() if re.search(r"ERROR|automation|Invalid config|recovery|Setup failed", l)][-20:]
        autos = [e.get("entity_id") for e in ha("/states", tok)[1] or [] if e.get("entity_id", "").startswith("automation.")]
        errs.append(f"automation entities: {autos}")
        conf = open(os.path.join(cfg, "configuration.yaml")).read()
        raise AssertionError(f"the automation is gone: HTTP {st}\n  HA's errors: " + "\n    ".join(errs) +
                             f"\n  automations.yaml: {os.path.getsize(os.path.join(cfg, 'automations.yaml'))} bytes\n  configuration.yaml:\n" + conf[:1500])
    st, entries = ha("/config/config_entries/entry", tok)
    have = sorted(e["domain"] for e in entries or [])
    gone = [d for d in s["home"]["entries"] if d not in have]
    if gone:
        raise AssertionError(f"config entries gone: {gone} (have {have})")
    doc = json.load(open(os.path.join(cfg, ".storage/core.config_entries")))
    url = next(e["data"]["url"] for e in doc["data"]["entries"] if e["domain"] == "zwave_js")
    port = "3001" if phase == "before" else "3000"
    if not url.endswith(":" + port):
        raise AssertionError(f"the Z-Wave JS entry points at {url}, not :{port}")
    if phase == "after":
        zs = json.loads(sh("podman", "unshare", "cat", os.path.join(f["data_dir"], "home-assistant/zwave-js/settings.json")).stdout)
        if zs.get("zwave", {}).get("serverPort") != 3000:
            raise AssertionError(f"Z-Wave JS UI's server port is {zs.get('zwave', {}).get('serverPort')}, the entry says 3000")
    return f"HA lets its admin in; the automation, {len(have)} config entries, Z-Wave JS at {url}"


def ha_ws_admin_only(token):
    """an admin-only command on HA's WebSocket API (config/auth/list): True if HA runs it"""
    sock = socket.create_connection((LOOP, 8123), timeout=20)
    key = base64.b64encode(os.urandom(16)).decode()
    sock.sendall(f"GET /api/websocket HTTP/1.1\r\nHost: {LOOP}:8123\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
                 f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n".encode())
    buf = b""
    while b"\r\n\r\n" not in buf:
        buf += sock.recv(4096)
    buf = buf.split(b"\r\n\r\n", 1)[1]

    def take(n):
        nonlocal buf
        while len(buf) < n:
            buf += sock.recv(65536)
        out, buf = buf[:n], buf[n:]
        return out

    def recv():
        b0, b1 = take(2)
        n = b1 & 0x7F
        n = struct.unpack(">H", take(2))[0] if n == 126 else struct.unpack(">Q", take(8))[0] if n == 127 else n
        return json.loads(take(n))

    def send(obj):
        data, mask = json.dumps(obj).encode(), os.urandom(4)
        n = len(data)
        head = bytes([0x81]) + (bytes([0x80 | n]) if n < 126 else bytes([0xFE]) + struct.pack(">H", n))
        sock.sendall(head + mask + bytes(c ^ mask[i % 4] for i, c in enumerate(data)))
    recv()
    send({"type": "auth", "access_token": token})
    if recv().get("type") != "auth_ok":
        raise AssertionError("HA's WebSocket API refuses the token")
    send({"id": 1, "type": "config/auth/list"})
    while (m := recv()).get("id") != 1:
        pass
    sock.close()
    return bool(m.get("success"))


GP_TOKENS = """name: gp-ha-tokens
template: {registry: generalprobe, name: gp-ha-tokens, version: 1}
# the Generalprobe's stand-in for Haus and the Bote (#349): it declares their Home Assistant
# tokens as they do, so their container gets them as podman secrets the probe can read
routes: []
secrets:
  - {name: HAUS_HA_TOKEN, external: true, optional: true}
  - {name: BOTE_HA_TOKEN, external: true, optional: true}
data: []
"""


def check_ha_tokens(f, s, phase):
    """#349: Home Assistant's post-deploy minted the tokens, nobody clicked them: the check
    lists the integrations with HA_TOKEN, Haus's token reads a state, the Bote's is refused
    an admin-only call. Haus and the Bote are not part of the migration: a stand-in service
    declares their tokens, as a consumer added after Home Assistant would."""
    if phase == "before":
        return "ServiceBay kept one administrator token in a file; Laib mints per consumer (after)"
    env = ["env", "LAIB_HOME=/mnt/data/laib", "LAIB_ACTOR=human:core", "/var/opt/laib/laib"]
    reg = "/mnt/data/laib/registries/generalprobe/templates/gp-ha-tokens"
    os.makedirs(reg, exist_ok=True)
    open(os.path.join(reg, "laib.yml"), "w").write(GP_TOKENS)
    open(os.path.join(reg, "values.example.yml"), "w").write("IMAGE: docker.io/library/busybox:stable\nHAUS: !secret HAUS_HA_TOKEN\nBOTE: !secret BOTE_HA_TOKEN\n")
    open(os.path.join(reg, "gp-ha-tokens.container"), "w").write(
        "[Container]\nImage=${IMAGE}\nExec=sleep infinity\nEnvironment=HAUS=${HAUS}\nEnvironment=BOTE=${BOTE}\n\n[Install]\nWantedBy=default.target\n")
    try:
        sh(*env, "add", "gp-ha-tokens")
        tok = {k: sh("podman", "secret", "inspect", "--showsecret", "--format", "{{.SecretData}}", f"laib.gp-ha-tokens.gp-ha-tokens.{k}").stdout.decode().strip()
               for k in ("HAUS", "BOTE")}
        st, state = ha("/states/sun.sun", tok["HAUS"])
        if st != 200:
            st, states = ha("/states", tok["HAUS"])
            state = (states or [None])[0] if st == 200 else None
        if not state:
            raise AssertionError(f"Haus's token reads no state: HTTP {st}")
        if not ha_ws_admin_only(tok["HAUS"]):
            raise AssertionError("Haus's token is no administrator's: voice counting would be refused")
        if ha("/states/" + state["entity_id"], tok["BOTE"])[0] != 200:
            raise AssertionError("the Bote's token reads no state")
        if ha_ws_admin_only(tok["BOTE"]):
            raise AssertionError("the Bote's token ran an admin-only command: whoever chats with the Bote is an administrator")
    finally:
        sh(*env, "remove", "gp-ha-tokens", check=False)
    sh("systemctl", "--user", "start", "home-assistant-check.service")
    status = json.load(open(os.path.join(f["data_dir"], "home-assistant/homeassistant/.laib-status.json")))
    if any("antwortet nicht" in x["detail"] for x in status["findings"]) or not status.get("detail"):
        raise AssertionError(f"the check does not list the integrations with HA_TOKEN: {status}")
    return f"Haus's token reads {state['entity_id']}, the Bote's is refused config/auth/list, the check: {status['detail']}"


def check_matter(f, s, phase):
    d = os.path.join(f["data_dir"], "home-assistant/matter-server" if phase == "before" else "home-assistant/matter")
    have = matter_files(d)
    lost = {k for k in s["matter"] if k not in have}   # the server rewrites its counters as it runs: the files, not their bytes
    if lost:
        raise AssertionError(f"the Matter fabric in {d} lacks {sorted(lost)}")
    return f"the Matter server's {len(s['matter'])} files are in {d}"


def check_mqtt(f, s, phase):
    r = mqtt(["mosquitto_sub", "-h", LOOP, "-u", f["mqtt_user"], "-P", f["mqtt_password"], "-t", "generalprobe/wohnzimmer/licht", "-C", "1", "-W", "15"])
    if r.returncode or r.stdout.decode().strip() != "an":
        raise AssertionError(f"MQTT with the old credentials: rc {r.returncode} {r.stdout.decode()!r} {r.stderr.decode()[-200:]}")
    r = mqtt(["mosquitto_sub", "-h", LOOP, "-u", f["mqtt_user"], "-P", "falsch", "-t", "x", "-C", "1", "-W", "5"])
    if r.returncode == 0:
        raise AssertionError("the broker let a wrong password in: the check proves nothing")
    return f"{f['mqtt_user']} connects with the old password and gets the retained message"


def check_share(f, s, phase):
    # ServiceBay's Samba sync keeps exactly the LLDAP users in the passdb: the
    # legacy share user is dropped by it, so a family member's own login is
    # what mounts the share on the box
    r = smb("anna", s["people"]["anna"]["password"], "get einkaufsliste.txt /dev/stdout")
    if r.returncode or b"Generalprobe" not in r.stdout:
        raise AssertionError(f"Samba as anna with her own password: {(r.stdout + r.stderr).decode()[-300:]}")
    return "anna mounts the share with her own login and reads the file"


def check_syncthing(f, s, phase):
    have = syncthing_id()
    if have != s["syncthing"]:
        raise AssertionError(f"Syncthing's device ID changed: {have}, before {s['syncthing']} (every device re-paired)")
    return "Syncthing keeps its device ID"


CHECKS = [("logins", check_logins), ("photos", check_photos), ("immich-library", check_library), ("documents", check_documents),
          ("vault", check_vault), ("calendar", check_dav), ("home-assistant", check_home), ("ha-tokens", check_ha_tokens), ("matter", check_matter), ("mqtt", check_mqtt),
          ("samba", check_share), ("syncthing", check_syncthing)]


def check(phase, only=None):
    s = json.load(open(STATE))
    f = s["facts"]
    red = 0
    for name, fn in CHECKS:
        if only and name not in only:
            continue
        xf = XFAIL.get(name) if phase == "after" else None
        try:
            what = fn(f, s, phase)
            if xf:
                red += 1
                print(f"XPASS {name}: {what} — it works now: drop the xfail ({xf})")
            else:
                print(f"ok    {name}: {what}")
        except Exception as e:
            if xf:
                print(f"xfail {name}: {e}\n      expected: {xf}")
            else:
                red += 1
                print(f"ROT   {name}: {e}")
        sys.stdout.flush()
    print(f"{phase}: {red} red")
    return 1 if red else 0


def main():
    a = sys.argv[1:]
    if a[:1] == ["install"]:
        servicebay_install(json.load(sys.stdin), a[1:])
        return 0
    if a[:1] == ["seed"]:
        seed(json.load(sys.stdin))
        return 0
    if a[:1] == ["check"] and len(a) >= 3 and a[1] == "--phase":
        return check(a[2], a[3:])
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main())
