"""aktualisierung: the week's updates (#287, #376, #528, #368).

Moved out of the Operator's Go (operator/loop.go patches, #839). The script
reads laib updates; it proposes itself, once while open, a service with
data's new image by digest, a value a status finding offers (Home
Assistant's extensions) and a new host image; firmware, minor versions and
registry errors go into the note for a human. The model sees the patch
versions and weighs which are worth proposing; check() lets it propose only
those, exactly as offered. Laib's own update is self-update.sh's (#547).
"""

import re

import laibskill
from laibskill import field, first_line

# a status finding's fix that is one value proposal (#528), filed as it stands:
# Home Assistant's check names the extensions to install this way
OFFER_FIX = re.compile(r"^laib propose --set services/([a-z0-9-]+)/values\.yml:([A-Z][A-Z0-9_]*)=(\S+)$", re.ASCII)


def run(s):
    s.offers = {}
    r = s.call("laib_updates")
    ups = r.json()
    if not isinstance(ups, list):
        return idle(s, "Aktualisierungen nicht lesbar: " + first_line(r.text) + ".")
    h = []
    inbox = s.call("laib_inbox").json()
    open_reasons = {field(p, "reason") for p in inbox} if isinstance(inbox, list) else set()

    def propose(what, **args):
        if args["reason"] in open_reasons:
            h.append(f"{what}: der Vorschlag ist noch offen.")
            return
        r = s.call("laib_propose", **args)
        h.append(f"{what} ließ sich nicht vorschlagen: {first_line(r.text)}." if r.error else f"{what} vorgeschlagen, für einen Menschen.")

    for u in ups:
        if field(u, "digest") and not field(u, "auto", False):
            what = f"{field(u, 'service')}: neues Image {field(u, 'image')}:{field(u, 'current')}"
            propose(what, reason=what + " (" + field(u, "digest") + ")", service=field(u, "service"), key=field(u, "key"),
                    value=f"{field(u, 'image')}:{field(u, 'current')}@{field(u, 'digest')}")
    findings = s.call("laib_diag").json()
    for f in findings if isinstance(findings, list) else []:
        svc, detail, m = field(f, "service"), field(f, "detail"), OFFER_FIX.match(field(f, "fix"))
        if field(f, "probe") == "status" and m and m.group(1) == svc:
            propose(f"{svc}: {detail}", reason=f"{svc}: {detail}", service=m.group(1), key=m.group(2), value=m.group(3))
        elif field(f, "probe") == "status" and detail.startswith("Firmware: "):
            h.append(f"{svc}: {detail}.")
    listed = ["Patch-Versionen, die du vorschlagen kannst:"]
    for u in ups:
        svc, key, image, cur, patch = (field(u, k) for k in ("service", "key", "image", "current", "patch"))
        if svc == "laib":
            continue
        if svc == "host" and patch:
            # the host image (#368): proposed here, a human approves
            diff = f"--- a/host/image\n+++ b/host/image\n@@ -1,2 +1,2 @@\n # tag: {key}\n-{image}@{cur}\n+{image}@{patch}\n"
            r = s.call("laib_propose", reason=f"Host-Image {key}: {cur} → {patch}", patch=diff)
            h.append(f"Host-Image {key} → {patch} ließ sich nicht vorschlagen: {first_line(r.text)}." if r.error else f"Host-Image {key} → {patch} vorgeschlagen, für einen Menschen.")
            continue
        if patch:
            s.offers[svc + " " + key] = image + ":" + patch
            listed.append(f"- service {svc}, key {key}, value {image}:{patch} (jetzt {cur})")
        if field(u, "minor"):
            h.append(f"{svc}: Minor-Version {field(u, 'minor')} (jetzt {cur}) für einen Menschen.")
        if field(u, "error"):
            h.append(f"{svc}: Registry nicht lesbar ({field(u, 'error')}).")
    if not s.offers:
        return idle(s, " ".join(h))
    return laibskill.done(context="\n".join(listed) + "\n", human=" ".join(h), checks=["laib_propose"])


def idle(s, human):
    note = ("Keine Patch-Versionen zum Vorschlagen. " + human).strip()
    s.call("laib_note", kind="watch", text=note)
    return laibskill.done(outcome="idle", note=note)


def check(s, tool, args):
    v = args.get("value")
    if not isinstance(v, str) or not v or s.offers.get(f"{args.get('service', '')} {args.get('key', '')}") != v:
        return "vorschlagen kannst du nur die Patch-Versionen oben, service, key und value genau so"
    return ""
