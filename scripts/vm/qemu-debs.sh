#!/bin/bash
# qemu's packages into ~/qemu-debs unless the cache brought them (#604).
# The runner's apt mirror stalled for minutes in half the runs, apt's own
# fetch with it: apt only names the files (--print-uris), curl fetches them
# in parallel, each with a deadline and other mirrors after it, and every
# file is checked against apt's SHA256. Without recommends (no GUI or
# sound); seabios and ipxe-qemu for the BIOS boot.
set -eu
pkgs="qemu-system-x86 qemu-utils seabios ipxe-qemu"
d="$HOME/qemu-debs"
if ls "$d"/*.deb >/dev/null 2>&1; then echo "qemu packages from the cache"; exit 0; fi
mkdir -p "$d"
timeout 120 sudo apt-get update -q || echo "apt-get update did not finish; the lists the runner has"
list=$(apt-get install -y -q --no-install-recommends --print-uris $pkgs | grep "^'http")
[ -n "$list" ] || { echo "apt named no files for $pkgs"; exit 1; }
get() { # get <uri> <file> <sha256>
  path=${1#*/ubuntu/}
  for base in http://archive.ubuntu.com/ubuntu http://azure.archive.ubuntu.com/ubuntu http://de.archive.ubuntu.com/ubuntu http://us.archive.ubuntu.com/ubuntu; do
    if curl -fsSL --connect-timeout 5 --max-time 60 -o "$d/$2.part" "$base/$path" && echo "$3  $d/$2.part" | sha256sum -c --quiet; then
      mv "$d/$2.part" "$d/$2"; echo "ok $2 ($base)"; return 0
    fi
  done
  echo "no mirror delivered $2"; return 1
}
export -f get; export d
echo "$list" | awk '{gsub("\x27","",$1); sub("SHA256:","",$4); print $1, $2, $4}' | xargs -P 8 -n 3 bash -c 'get "$0" "$1" "$2"'
