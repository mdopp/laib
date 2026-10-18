#!/usr/bin/env bash
# Start the KVM guest the VM jobs drive (vm-install, generalprobe), daemonized,
# from the job's working directory: qemu.pid, qmp.sock and serial.log land there.
#
# $1 = qemu -boot argument: order=cd (disk first, empty disk falls through to the stick),
# once=d (the stick once = reinstall; without "once" the VM would reinstall forever,
# the job the EFI post-install step does on real hardware), order=c (the disk only)
# DISKS = the drives in this order (default: the OS disk and the two data disks)
# ISO   = the stick (default laib-vm.iso); IGN instead boots the OS disk as it is
#         with this Ignition file (a Fedora CoreOS qemu image, no stick, no -boot)
# MEM, CPUS (4096, 2); FWD = more hostfwd rules, "tcp::5888-:5888 …"
drives=()
for d in ${DISKS:-disk.qcow2 data1.qcow2 data2.qcow2}; do
  f=qcow2; [[ $d == *.raw ]] && f=raw
  # the OS disk without QEMU's zero detection: detect-zeroes=unmap turns the guest's
  # zero writes into qcow2 discards, and a hard reset in the middle of that left a
  # root no real disk would (#81: the same cut was green in run 38308352527 without it,
  # red with it, and a guest-side sync did not help). The data disks keep it (thin images).
  if [ "$d" = disk.qcow2 ]; then drives+=(-drive "file=$d,if=virtio,format=$f")
  else drives+=(-drive "file=$d,if=virtio,format=$f,discard=unmap,detect-zeroes=unmap"); fi
done
boot=(-cdrom "${ISO:-laib-vm.iso}" -boot "$1")
[ -n "${IGN:-}" ] && boot=(-fw_cfg "name=opt/com.coreos/config,file=$IGN")
nic="user,model=virtio,hostfwd=tcp::2222-:22,hostfwd=tcp::18080-:8080"
for f in ${FWD:-}; do nic="$nic,hostfwd=$f"; done
rm -f qmp.sock
qemu-system-x86_64 -enable-kvm -cpu host -m "${MEM:-4096}" -smp "${CPUS:-2}" -display none -daemonize \
  -pidfile qemu.pid -serial file:serial.log -qmp unix:qmp.sock,server,nowait "${drives[@]}" \
  "${boot[@]}" -nic "$nic"
