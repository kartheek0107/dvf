# =============================================================================
# qemu-accelerator-models/meson.build
#
# Builds a standalone qemu-system-x86_64 binary with all DVF device models
# baked in.  The output binary is self-contained: deploying users only need
# the resulting executable — no ~/VirtualMachines/ tree, no QEMU source.
#
# Usage (first build — clones QEMU automatically):
#   cd qemu-accelerator-models
#   meson setup ../builds/qemu-build
#   ninja -C ../builds/qemu-build
#
# Usage (reuse an existing QEMU source tree):
#   meson setup ../builds/qemu-build -Dqemu_source_dir=/path/to/qemu-src
#
# Output:
#   ../builds/qemu-build/qemu-system-x86_64  — copy of the final binary
#   ../builds/qemu/bin/qemu-system-x86_64    — installed (ninja install)
#
# Adding a new device model:
#   1. Add <name>.c to hw/misc/
#   2. Add files('<name>.c') to the dvf_device_sources list in hw/misc/meson.build
#   3. Run: ninja -C ../builds/qemu-build
#   4. Add the new device to go-orchestrator/configs/device_registry.json
#
# =============================================================================

project('dvf-qemu', 'c',
  version : '0.1.0',
  license : 'GPL-2.0-only',
  meson_version : '>= 0.63.0',
  default_options : ['warning_level=1'],
)

# ---------------------------------------------------------------------------
# Options (override on the meson setup command line with -D<option>=<value>)
# ---------------------------------------------------------------------------
qemu_version    = get_option('qemu_version')
qemu_source_dir = get_option('qemu_source_dir')

# ---------------------------------------------------------------------------
# Device model sources — used as tracked inputs so Ninja re-runs the build
# when any .c file changes.
# ---------------------------------------------------------------------------
device_model_sources = files(
  'hw/misc/gp_gpu.c',
  # ── Add new device model .c files here to have them tracked by Ninja ──
  # 'hw/misc/my_new_device.c',
)

# ---------------------------------------------------------------------------
# Build target
#
# Delegates to scripts/build_qemu_with_models.sh which:
#   1. Clones QEMU at qemu_version (or reuses qemu_source_dir if provided)
#   2. Copies hw/misc/*.c + hw/misc/meson.build into QEMU's hw/misc-dvf/
#   3. Appends  subdir('misc-dvf')  to QEMU's hw/meson.build (idempotent)
#   4. Runs QEMU's own meson setup + ninja inside the cloned source
#   5. Installs the binary and places a copy at @OUTDIR@/qemu-system-x86_64
# ---------------------------------------------------------------------------
bash = find_program('bash', required : true)

custom_target('qemu-system-x86_64',
  input         : device_model_sources,
  output        : 'qemu-system-x86_64',
  build_by_default : true,
  install       : true,
  install_dir   : 'bin',
  command       : [
    bash,
    meson.project_source_root() / 'scripts' / 'build_qemu_with_models.sh',
    meson.project_source_root(),   # ARG1: qemu-accelerator-models/ root
    '@OUTDIR@',                    # ARG2: Meson build output directory
    qemu_version,                  # ARG3: QEMU version tag  (e.g. v8.2.0)
    qemu_source_dir,               # ARG4: pre-cloned source path (or "")
  ],
)
