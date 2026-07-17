#!/bin/bash
# ============================================================================
# DVF Manual Test VM — interactive shell for manual vecaddx testing
#
# Key fix: -machine q35 is required for MSI-X to work (real driver needs it)
#          security_model=none matches the original runQemu.sh
#          -enable-kvm -cpu host for speed
# ============================================================================

set -euo pipefail

QEMU_BIN="$HOME/VirtualMachines/qemu_bin/bin/qemu-system-x86_64"
KERNEL="$HOME/VirtualMachines/linux/arch/x86/boot/bzImage"
ROOTFS="$HOME/qemu-rootfs/rootfs.ext4"
SHARE_DIR="$HOME/qemu-rootfs/share"

VM_ID="manual-test-$(date +%s)"
OVERLAY="/tmp/dvf-manual/${VM_ID}.qcow2"

echo "=============================================="
echo " DVF Manual Test VM  (machine: q35 + MSI-X)"
echo " VM ID   : $VM_ID"
echo " Overlay : $OVERLAY"
echo " Share   : $SHARE_DIR"
echo "=============================================="
echo ""
echo " ── Paste in the VM shell ────────────────────"
echo ""
echo "   mount -t 9p -o trans=virtio,version=9p2000.L hostshare /mnt/share"
echo "   insmod /mnt/share/gpgpu_pcie_ep_driver.ko"
echo "   ls -la /dev/gp_gpu          # <-- should exist with real driver"
echo "   dmesg | tail -15            # <-- should show CDAC GPGPU device detected"
echo ""
echo "   cd /mnt/share/vishwa_tests/regression/vecaddx"
echo "   export LD_LIBRARY_PATH=/mnt/share/vishwa_tests/lib"
echo "   export OCL_ICD_VENDORS=/mnt/share/vishwa_tests/lib/OpenCL/vendors"
echo "   export POCL_DEVICES=basic"
echo "   export POCL_CACHE_DIR=/tmp/pocl_cache"
echo ""
echo "   /mnt/share/vishwa_tests/lib/ld-linux-x86-64.so.2 \\"
echo "     --library-path /mnt/share/vishwa_tests/lib \\"
echo "     ./vecaddx \\"
echo "     1>/tmp/stdout.txt 2>/tmp/stderr.txt"
echo "   echo \"Exit: \$?\""
echo "   unset LD_LIBRARY_PATH"
echo "   echo '=== STDERR ===' ; cat /tmp/stderr.txt | head -5"
echo "   echo '=== STDOUT (results) ===' ; cat /tmp/stdout.txt | grep -E 'result|PASS|FAIL|Error'"
echo ""
echo " Press Ctrl+A then X to exit QEMU."
echo "=============================================="
echo ""

mkdir -p /tmp/dvf-manual

echo "[+] Creating qcow2 overlay..."
qemu-img create -f qcow2 -b "$ROOTFS" -F raw "$OVERLAY"

echo "[+] Starting QEMU (q35 machine, KVM, MSI-X capable)..."
echo ""

"$QEMU_BIN" \
    -enable-kvm \
    -machine q35 \
    -cpu host \
    -kernel "$KERNEL" \
    -drive "file=${OVERLAY},format=qcow2,if=virtio" \
    -append "root=/dev/vda console=ttyS0 rw init=/bin/sh" \
    -m 1024 \
    -smp 2 \
    -nographic \
    -virtfs "local,path=${SHARE_DIR},mount_tag=hostshare,security_model=none,id=hostshare" \
    -device gp_gpu \
    -no-reboot

echo ""
echo "[+] VM exited. Cleaning up..."
rm -f "$OVERLAY"
echo "[+] Done."
