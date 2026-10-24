#!/bin/bash
# qemu's packages into ~/qemu-debs unless the cache brought them (#604).
# The runner's apt mirror stalled for minutes in half the runs, apt's own
# fetch with it: apt only names the files (--print-uris), curl fetches them
# in parallel, each with a deadline and other mirrors after it, and every
# file is checked against apt's hash. Without recommends (no GUI or
# sound); seabios and ipxe-qemu for the BIOS boot.
set -eu
pkgs="qemu-system-x86 qemu-utils seabios ipxe-qemu"
d="$HOME/qemu-debs"
if ls "$d"/*.deb >/dev/null 2>&1; then echo "qemu packages from the cache"; exit 0; fi
mkdir -p "$d"
timeout 120 sudo apt-get update -q || echo "apt-get update did not finish; the lists the runner has"
# the runner's sources are mirror+file: URIs; only the path from pool/ on counts
list=$(apt-get install -y -q --no-install-recommends --print-uris $pkgs | grep "^'.*/pool/" || true)
[ -n "$list" ] || { echo "apt named no files for $pkgs"; exit 1; }
get() { # get <uri> <file> <ALG:hash>, the hash apt names (from the signed index; MD5Sum on the runner)
  path=pool/${1#*/pool/}
  case ${3%%:*} in SHA512) sum=sha512sum ;; SHA256) sum=sha256sum ;; MD5Sum) sum=md5sum ;; *) echo "unknown hash for $2: $3"; return 1 ;; esac
  for base in http://archive.ubuntu.com/ubuntu http://azure.archive.ubuntu.com/ubuntu http://de.archive.ubuntu.com/ubuntu http://us.archive.ubuntu.com/ubuntu; do
    if curl -fsSL --connect-timeout 5 --max-time 60 -o "$d/$2.part" "$base/$path" && echo "${3#*:}  $d/$2.part" | "$sum" -c --quiet; then
      mv "$d/$2.part" "$d/$2"; echo "ok $2 ($base)"; return 0
    fi
  done
  echo "no mirror delivered $2"; return 1
}
export -f get; export d
echo "$list" | awk '{gsub("\x27","",$1); print $1, $2, $4}' | xargs -P 8 -n 3 bash -c 'get "$0" "$1" "$2"'
