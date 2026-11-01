# archiv

The message archive of ADR 0029, one instance per person: `laib add archiv
--person <login>`. The program, its contract and its limits are in
[mdopp/archiv](https://github.com/mdopp/archiv) (`apps/archiv/README.md`).
What the box adds: the one name `archiv.<domain>` (#986), the two folders,
FUSE for the vault, the proxy secret, the status file.

Unproven until the first install on the Kraftwerk: gocryptfs mounting in
rootless podman on Fedora CoreOS with `/dev/fuse` and `SYS_ADMIN`.
