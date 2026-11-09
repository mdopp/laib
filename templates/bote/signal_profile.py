#!/usr/bin/env python3
"""The Bote's Signal profile: its name and picture, as the family sees them (owner, 2026-11-09).

post-deploy runs this with Signal on. It asks signal-cli's daemon (bote-signal, published on the
host's loopback) for `updateProfile` with PROFILE_NAME and PROFILE_AVATAR (a PNG in the
template's folder, sent as a data URI: the daemon reads no file of ours), and notes what it set in
`${DATA_DIR}/bote/signal/laib-profil.json`. It asks again only when the account, the name or the
picture's bytes differ from that note: Signal keeps the profile, and every upload is a new avatar
on its servers. An empty value leaves that part of the profile as it is.

A failure is one line and exit 0: the profile never makes the deploy red; the next apply tries
again, since nothing was noted. The error's text is not passed on: it names the number."""
import base64, hashlib, json, os, sys, urllib.request
from pathlib import Path


def fingerprint(account, name, avatar):
    return hashlib.sha256(json.dumps([account, name, hashlib.sha256(avatar).hexdigest() if avatar else ""]).encode()).hexdigest()


def main(env, here):
    account = env.get("SIGNAL_ACCOUNT", "")
    name = env.get("PROFILE_NAME", "").strip()
    avatar_name = env.get("PROFILE_AVATAR", "").strip()
    if not account or not (name or avatar_name):
        return
    avatar = b""
    if avatar_name:
        try:
            avatar = (here / avatar_name).read_bytes()
        except OSError:
            print("signal-profil: das Bild %s fehlt, nichts gesetzt" % avatar_name)
            return
    note = Path(env["LAIB_DATA_DIR"]) / "bote" / "signal" / "laib-profil.json"
    want = fingerprint(account, name, avatar)
    try:
        if json.loads(note.read_text()).get("fingerprint") == want:
            return
    except (OSError, ValueError, AttributeError):
        pass
    params = {"account": account}
    if name:
        params.update(givenName=name, familyName="")   # exactly this name, no leftover family name
    if avatar:
        params["avatar"] = "data:image/png;base64," + base64.b64encode(avatar).decode()
    url = env.get("SIGNAL_RPC_URL", "http://127.0.0.1:8085").rstrip("/") + "/api/v1/rpc"
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "updateProfile", "params": params}).encode()
    try:
        with urllib.request.urlopen(urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"}), timeout=30) as r:
            out = json.loads(r.read() or b"{}")
        if out.get("error") is not None:
            raise ValueError("signal-cli: Fehler %s" % (out["error"] or {}).get("code"))
    except Exception as e:  # never red: the profile is a nicety (owner, 2026-11-09)
        print("signal-profil: nicht gesetzt (%s), das nächste apply versucht es wieder" % type(e).__name__)
        return
    note.parent.mkdir(parents=True, exist_ok=True)
    note.write_text(json.dumps({"fingerprint": want, "name": name, "bild": avatar_name}) + "\n")
    print("signal-profil: gesetzt (%s)" % ", ".join(filter(None, ["Name „%s“" % name if name else "", "Bild %s" % avatar_name if avatar else ""])))


if __name__ == "__main__":
    main(os.environ, Path(sys.argv[0]).resolve().parent)
