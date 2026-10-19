#!/usr/bin/env python3
"""#396: Solaris's links into the old world, switched off explicitly (ADR 0015:
Solaris retires in phase 5; no shims for it). After migration day they fail
silently:

- SB_MCP_URL, SB_API_URL, SB_MINT_URL: the ServiceBay admin toolbox, its
  pollers and the delegated-admin mint at www.<domain> (now Haus's redirect).
  Administration is Laib's and the Operator's (ADR 0013); chat. is dark.
- DEADLINES_SYNC_URL(_BASE), CONTACTS_SYNC_URL(_BASE): the write-back into the
  residents' `solaris` calendars and address books. It needs Radicale rights
  that ServiceBay's radicale.yml carried and Laib's Radicale (owner_only)
  does not grant; solaris-radicale-rights.timer finds no radicale.yml.
  What it wrote stays in Radicale, readable.

An empty value disables each in Solaris. The ignored solaris.yml is edited in
place (a copy kept once as solaris.yml.before-396), the rights timer is
disabled, Solaris restarted. Idempotent. Run on the box as core:
  python3 scripts/solaris-links-off.py [--yml ~/.config/containers/systemd/solaris.yml]
"""
import argparse, os, re, shutil, subprocess, sys

OFF = ["SB_MCP_URL", "SB_API_URL", "SB_MINT_URL", "DEADLINES_SYNC_URL", "DEADLINES_SYNC_URL_BASE", "CONTACTS_SYNC_URL", "CONTACTS_SYNC_URL_BASE"]


def main():
    a = argparse.ArgumentParser()
    a.add_argument("--yml", default=os.path.expanduser("~/.config/containers/systemd/solaris.yml"))
    args = a.parse_args()
    src = open(args.yml).read()
    text, changed = src, []
    for name in OFF:
        pat = re.compile(r"(- name: " + re.escape(name) + r"\n(\s+)value: )(?!\"\"\n)[^\n]*\n")
        text, n = pat.subn(lambda m: m.group(1) + '""\n', text)
        if n:
            changed.append(name)
    if not changed:
        print("solaris: nothing to change, the old links are off")
        return 0
    keep = args.yml + ".before-396"
    if not os.path.exists(keep):
        shutil.copy2(args.yml, keep)
    tmp = args.yml + ".tmp"
    with open(tmp, "w") as f:
        f.write(text)
    os.replace(tmp, args.yml)
    print("solaris: switched off " + ", ".join(changed) + " (the old file: " + keep + ")")
    subprocess.run(["systemctl", "--user", "disable", "--now", "solaris-radicale-rights.timer"])
    subprocess.run(["systemctl", "--user", "daemon-reload"])
    rc = subprocess.run(["systemctl", "--user", "restart", "solaris.service"]).returncode
    print("solaris: restarted" if rc == 0 else "solaris: restart failed: systemctl --user status solaris.service")
    return rc


if __name__ == "__main__":
    sys.exit(main())
