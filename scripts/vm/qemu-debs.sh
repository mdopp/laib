#!/bin/bash
# qemu's packages into ~/qemu-debs unless the cache brought them (#604):
# the runner's apt mirror stalled for 15-20 min in half of the runs.
# Without recommends (no GUI or sound); seabios and ipxe-qemu for the BIOS boot.
set -eu
pkgs="qemu-system-x86 qemu-utils seabios ipxe-qemu"
d="$HOME/qemu-debs" a="$HOME/qemu-archives"
if ls "$d"/*.deb >/dev/null 2>&1; then echo "qemu packages from the cache"; exit 0; fi
mkdir -p "$a/partial" "$d"
fetch() { timeout 180 sudo apt-get update -q && timeout 240 sudo apt-get install -y -q --no-install-recommends --download-only -o Acquire::Retries=3 -o Dir::Cache::archives="$a" $pkgs; }
# a stalled Azure mirror gets its minutes, then Ubuntu's own archive
fetch || { sudo sed -i 's|http://azure.archive.ubuntu.com|http://archive.ubuntu.com|g' /etc/apt/sources.list.d/*.sources /etc/apt/sources.list 2>/dev/null || true; fetch; }
cp "$a"/*.deb "$d"/
