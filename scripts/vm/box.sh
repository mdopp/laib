# Sourced by the VM jobs (vm-install, generalprobe) from the job's working
# directory: the SSH line into the guest, a clean power-off (QEMU gone), and
# waiting for laib after a boot.
SSH="ssh -q -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -i key -p 2222 core@127.0.0.1"
off() { local p; p=$(cat qemu.pid 2>/dev/null) || return 0; $SSH 'sudo systemctl poweroff' || true
  for i in $(seq 1 60); do kill -0 "$p" 2>/dev/null || return 0; sleep 2; done; sudo kill "$p"; sleep 3; }
ready() { for i in $(seq 1 90); do $SSH 'curl -fsS http://127.0.0.1:5900/ready' >/dev/null 2>&1 && { echo "ready after $((i*10)) s"; return 0; }; sleep 10; done
  echo "laib never became ready"; return 1; }
marker() { debugfs -R "cat /marker" foreign.raw 2>/dev/null | grep -qx "keep me" || { echo "the file on the foreign disk is gone"; return 1; }; echo "foreign disk: its file is intact"; }
# The prelude of a remote script that runs laib: $L = lw, a verb that meets the
# apply lock (exit 4, docs/INTERFACE.md) tries again for up to 3 min; any other
# exit code fails as before. The box's converge timer fires 2 min after each
# boot, while a step may be at its first mutating verb (#211).
LW='export LAIB_HOME=/mnt/data/laib LAIB_ACTOR=human:core; L=lw; lw() { local i rc; for i in $(seq 1 60); do rc=0; /var/opt/laib/laib "$@" || rc=$?; [ "$rc" = 4 ] || return "$rc"; echo "laib $1: the lock is busy (the converge timer, #211), trying again" >&2; sleep 3; done; return 4; }'
