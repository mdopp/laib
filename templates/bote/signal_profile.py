#!/usr/bin/env python3
"""The Bote's Signal profile: its name, its description and its picture, as the family sees them
(owner, 2026-11-09).

post-deploy runs this with Signal on. It asks signal-cli's daemon (bote-signal, published on the
host's loopback) for `updateProfile` with PROFILE_NAME, PROFILE_ABOUT (the line under the name:
where the box and the Homes app are) and PROFILE_AVATAR (a PNG in the template's folder, sent as a
data URI: the daemon reads no file of ours), and notes what it set in
`${DATA_DIR}/bote/signal/laib-profil.json`. It asks again only when the account, the name, the
description or the picture's bytes differ from that note: Signal keeps the profile, and every
upload is a new avatar on its servers. An empty value leaves that part of the profile as it is.

Signal's apps cut a description at 140 graphemes (Signal-Android EditAboutState.ABOUT_MAX_GRAPHEMES);
signal-cli does not check, so a longer one loses whole " · " parts from its end here until it fits.

A failure is one line and exit 0: the profile never makes the deploy red; the next apply tries
again, since nothing was noted. The error's text is not passed on: it names the number."""
import base64, hashlib, json, os, sys, time, urllib.request
from pathlib import Path

ABOUT_MAX = 140


def fingerprint(account, name, avatar, about=""):
    return hashlib.sha256(json.dumps([account, name, hashlib.sha256(avatar).hexdigest() if avatar else "", about]).encode()).hexdigest()


def fitted(about):
    """the description within Signal's 140: whole " · " parts dropped from the end, a lone one cut"""
    parts = about.split(" · ")
    while len(parts) > 1 and len(" · ".join(parts)) > ABOUT_MAX:
        parts.pop()
    return " · ".join(parts)[:ABOUT_MAX]


def main(env, here):
    account = env.get("SIGNAL_ACCOUNT", "")
    name = env.get("PROFILE_NAME", "").strip()
    avatar_name = env.get("PROFILE_AVATAR", "").strip()
    about = fitted(env.get("PROFILE_ABOUT", "").strip())
    if not account or not (name or avatar_name or about):
        return
    avatar = b""
    if avatar_name:
        try:
            avatar = (here / avatar_name).read_bytes()
        except OSError:
            print("signal-profil: das Bild %s fehlt, nichts gesetzt" % avatar_name)
            return
    note = Path(env["LAIB_DATA_DIR"]) / "bote" / "signal" / "laib-profil.json"
    want = fingerprint(account, name, avatar, about)
    try:
        if json.loads(note.read_text()).get("fingerprint") == want:
            return
    except (OSError, ValueError, AttributeError):
        pass
    params = {"account": account}
    if name:
        params.update(givenName=name, familyName="")   # exactly this name, no leftover family name
    if about:
        params["about"] = about
    if avatar:
        params["avatar"] = "data:image/png;base64," + base64.b64encode(avatar).decode()
    url = env.get("SIGNAL_RPC_URL", "http://127.0.0.1:8085").rstrip("/") + "/api/v1/rpc"
    body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "updateProfile", "params": params}).encode()
    for versuch in range(int(env.get("SIGNAL_PROFILE_TRIES", "8"))):  # right after apply the daemon is still starting (2026-11-09: ConnectionResetError)
        try:
            with urllib.request.urlopen(urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"}), timeout=30) as r:
                out = json.loads(r.read() or b"{}")
            if out.get("error") is not None:
                raise ValueError("signal-cli: Fehler %s" % (out["error"] or {}).get("code"))
            break
        except ValueError as e:  # the daemon answered no: waiting does not help
            print("signal-profil: nicht gesetzt (%s), das nächste apply versucht es wieder" % type(e).__name__)
            return
        except Exception as e:  # never red: the profile is a nicety (owner, 2026-11-09)
            if versuch + 1 >= int(env.get("SIGNAL_PROFILE_TRIES", "8")):
                print("signal-profil: nicht gesetzt (%s), das nächste apply versucht es wieder" % type(e).__name__)
                return
            time.sleep(float(env.get("SIGNAL_PROFILE_WAIT", "15")))
    note.parent.mkdir(parents=True, exist_ok=True)
    note.write_text(json.dumps({"fingerprint": want, "name": name, "bild": avatar_name, "beschreibung": about}) + "\n")
    print("signal-profil: gesetzt (%s)" % ", ".join(filter(None, ["Name „%s“" % name if name else "", "Beschreibung" if about else "", "Bild %s" % avatar_name if avatar else ""])))


if __name__ == "__main__":
    main(os.environ, Path(sys.argv[0]).resolve().parent)
