# =============================================================================
# hw/misc/meson.build  (DVF PCI device models fragment)
#
# NOTE: gp_gpu.c is a PCI device — it must use pci_ss (the Meson build set
# for hw/pci/ level devices), NOT softmmu_ss which is for hw/misc/ generics.
# The build script copies this file into <qemu-src>/hw/pci-dvf/ and appends
# subdir('pci-dvf') to <qemu-src>/hw/pci/meson.build so pci_ss is in scope.
#
# Existing device:
#   gp_gpu.c — CDAC GPGPU PCIe simulation (TYPE_CDAC_GPGPU = "gp_gpu")
#              Vendor: 0x1222  Device: 0x2221
#              BAR0: 16 KB register space
#              DMA engine (host ↔ 128 MB device DDR)
#              MMU page table walker
#              MSI-X (4 vectors, vector 0 used)
#
# To add a new PCI device model:
#   1. Add <name>.c to this directory (qemu-accelerator-models/hw/misc/)
#   2. Add  files('<name>.c')  to dvf_pci_sources below
#   3. Rebuild: cd qemu-accelerator-models && ninja -C ../builds/qemu-build
#   4. Verify:  builds/qemu/qemu-system-x86_64 -device <name>,help
# =============================================================================

dvf_pci_sources = files(
  'gp_gpu.c',
  # ── Add new PCI device models below this line ──
  # 'my_new_pci_device.c',
)

pci_ss.add(when : 'CONFIG_PCI', if_true : dvf_pci_sources)
