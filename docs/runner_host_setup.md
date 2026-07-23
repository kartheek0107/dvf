# DVF Runner Host Setup Guide

> **Audience:** Infrastructure / platform engineers setting up a new CI runner machine.  
> **Frequency:** Done **once per machine**. Validation engineers who just want to add tests never need this guide.

---

## Overview

The DVF CI pipeline requires a **self-hosted GitLab runner** with physical QEMU/KVM
infrastructure pre-installed on the host. The runner is tagged `dvf-runner` in
`.gitlab-ci.yml` so GitLab routes all DVF jobs to it.

Every push to the repo triggers:
1. **build** — compiles the driver, orchestrator binary, and C test binaries
2. **deploy** — copies everything into the 9p share directory on this host
3. **test** — starts the Go orchestrator, boots QEMU VMs from the pre-built
   images on this host, runs tests, and reports results

The QEMU images and kernel on this host are **never rebuilt by CI**. They are
built once by the infra team and stay in place.

---

## Prerequisites

Install the following on the runner host machine (Fedora/RHEL shown; adjust for
your distro):

```bash
sudo dnf install -y \
    git gcc make python3 golang \
    qemu-system-x86_64 qemu-kvm \
    packer \
    libvirt-devel glib2-devel pixman-devel \
    bc bison flex openssl-devel elfutils-devel \
    rsync curl jq
```

Verify KVM access:

```bash
ls -la /dev/kvm
# Must exist. If not: sudo modprobe kvm_intel  (or kvm_amd)
# Then: sudo usermod -aG kvm $USER && newgrp kvm
```

Verify Go version (1.22+ required):

```bash
go version
```

---

## Directory Layout Expected by CI

All CI jobs hard-code these paths (configurable via CI variables):

```
$HOME/
├── VirtualMachines/
│   ├── qemu_bin/
│   │   └── bin/
│   │       └── qemu-system-x86_64   ← custom QEMU with gp_gpu device
│   └── linux/
│       └── arch/x86/boot/bzImage    ← kernel for the guest VM
└── qemu-rootfs/
    ├── rootfs.ext4                  ← guest root filesystem (built by Packer)
    └── share/                       ← 9p share (populated by CI deploy stage)
```

Create the skeleton now:

```bash
mkdir -p ~/VirtualMachines/qemu_bin/bin
mkdir -p ~/VirtualMachines/linux/arch/x86/boot
mkdir -p ~/qemu-rootfs/share
```

---

## Step 1 — Build the Custom QEMU (with `gp_gpu` Device)

The DVF tests require a QEMU build that includes the custom `gp_gpu` PCIe device
model. The device model source lives in `qemu-accelerator-models/` in this repo.

### 1a. Get the QEMU source

```bash
git clone https://gitlab.com/qemu-project/qemu.git ~/VirtualMachines/qemu-src
cd ~/VirtualMachines/qemu-src
git checkout v8.2.0          # use the version your team standardised on
```

### 1b. Inject the device models into the QEMU source tree

The repo ships proper `meson.build` files inside `qemu-accelerator-models/` so
you do **not** need to manually edit QEMU's build files.  The entire `hw/misc/`
subdirectory (sources + `meson.build`) is copied as a self-contained unit:

```bash
# From the driver-validation-suite repo root:

# Copy the whole DVF device model directory into the QEMU source tree.
# This brings both the .c sources AND the meson.build fragment.
cp -r qemu-accelerator-models/hw/misc \
      ~/VirtualMachines/qemu-src/hw/misc-dvf

# Wire it into QEMU's hw-level Meson graph (one line, done once):
echo "subdir('misc-dvf')" \
  >> ~/VirtualMachines/qemu-src/hw/meson.build
```

> **Developer shortcut (symlink instead of copy):**  
> If you are actively editing device model source files, use a symlink so changes
> in this repo are immediately visible to the QEMU build without re-copying:
>
> ```bash
> ln -s "$(realpath qemu-accelerator-models/hw/misc)" \
>        ~/VirtualMachines/qemu-src/hw/misc-dvf
> echo "subdir('misc-dvf')" >> ~/VirtualMachines/qemu-src/hw/meson.build
> ```

> **Adding a new device model later:**  
> Add `<name>.c` to `qemu-accelerator-models/hw/misc/` and add one line to
> `qemu-accelerator-models/hw/misc/meson.build` — no changes to QEMU's own
> build files are ever needed again.

### 1c. Configure and compile

```bash
cd ~/VirtualMachines/qemu-src

./configure \
    --prefix=$HOME/VirtualMachines/qemu_bin \
    --target-list=x86_64-softmmu \
    --enable-kvm \
    --disable-docs \
    --disable-werror

make -j$(nproc)
make install
```

### 1d. Verify

```bash
~/VirtualMachines/qemu_bin/bin/qemu-system-x86_64 \
    -device gp_gpu,help 2>&1 | head -5
# Should print the gp_gpu device properties, not "unknown device"
```

---

## Step 2 — Build the Linux Kernel

The guest VM boots with a custom kernel. A standard x86_64 defconfig kernel works
fine as a starting point.

### 2a. Get the kernel source

```bash
git clone --depth=1 \
    https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git \
    ~/VirtualMachines/linux
cd ~/VirtualMachines/linux
```

### 2b. Configure

```bash
make defconfig
make kvm_guest.config   # overlay KVM-optimised options

# Ensure 9p virtfs is enabled (needed for the share mount):
scripts/config --enable CONFIG_NET_9P
scripts/config --enable CONFIG_NET_9P_VIRTIO
scripts/config --enable CONFIG_9P_FS
scripts/config --enable CONFIG_9P_FS_POSIX_ACL
scripts/config --enable CONFIG_VIRTIO_PCI
scripts/config --enable CONFIG_VIRTIO_BLK
scripts/config --enable CONFIG_VIRTIO_NET

make olddefconfig
```

### 2c. Compile

```bash
make -j$(nproc) bzImage
```

The output is at `arch/x86/boot/bzImage` — exactly where CI expects it.

### 2d. Verify the path

```bash
ls -lh ~/VirtualMachines/linux/arch/x86/boot/bzImage
```

---

## Step 3 — Build the Guest Root Filesystem (Packer)

The rootfs is a minimal Alpine Linux image with the DVF Python agent pre-baked in.
It is built using HashiCorp Packer from `guest-os/packer/` in this repo.

### 3a. Install Packer (if not already installed)

```bash
# Fedora/RHEL:
sudo dnf install -y packer

# Or install from HashiCorp directly:
curl -fsSL https://apt.releases.hashicorp.com/gpg | sudo gpg --dearmor \
    -o /usr/share/keyrings/hashicorp.gpg
# Follow https://developer.hashicorp.com/packer/install for your distro
```

### 3b. Build the image

```bash
cd ~/driver-validation-suite/guest-os

make build
# This:
#   - Downloads Alpine Linux 3.20 virt ISO (~50 MB)
#   - Boots a temporary QEMU VM
#   - Installs Alpine, Python 3, and the DVF agent
#   - Outputs guest-os/output/dvf-guest.ext4
# Takes 10-15 minutes on first run.
```

### 3c. Install to the expected path

```bash
cp guest-os/output/dvf-guest.ext4 ~/qemu-rootfs/rootfs.ext4
ls -lh ~/qemu-rootfs/rootfs.ext4
# Should be around 200-500 MB
```

> **Note:** You only need to rebuild this image if:
> - The DVF Python agent code (`python-agent/`) changes significantly, OR
> - You want to upgrade the Alpine base image.
>
> For normal test additions, the rootfs never needs to be rebuilt — the agent
> reads test binaries from the 9p share at runtime.

---

## Step 4 — Register the GitLab Runner

### 4a. Install the GitLab Runner binary

```bash
# The RPM is included in the repo root as a convenience:
sudo rpm -ivh ~/driver-validation-suite/gitlab-runner_amd64.rpm

# Or install from GitLab's official repo:
# https://docs.gitlab.com/runner/install/linux-repository.html
```

### 4b. Register with your GitLab instance

```bash
sudo gitlab-runner register
```

Answer the prompts:

| Prompt | Value |
|---|---|
| GitLab instance URL | `https://your-gitlab.example.com/` |
| Registration token | Found in GitLab → Project → Settings → CI/CD → Runners |
| Description | `dvf-runner-<hostname>` |
| Tags | **`dvf-runner`** ← this is critical, must match `.gitlab-ci.yml` |
| Executor | `shell` |

### 4c. Enable and start

```bash
sudo systemctl enable --now gitlab-runner
sudo systemctl status gitlab-runner
```

### 4d. Grant KVM access to the runner user

```bash
sudo usermod -aG kvm gitlab-runner
# Log out and back in, or:
sudo -u gitlab-runner newgrp kvm
```

---

## Step 5 — First Deploy (Populate the 9p Share)

Run the deploy script once manually to pre-populate the share before the first
CI push:

```bash
cd ~/driver-validation-suite

bash scripts/deploy_share.sh \
    --skip-vishwa-build    # skip if Vishwa source is not present
    # Remove --skip-driver-build if you want the driver rebuilt too
```

Verify the share layout:

```bash
ls ~/qemu-rootfs/share/
# Expected:
#   gpgpu_pcie_ep_driver.ko
#   python-agent/
#   start_agent.sh
#   vishwa_tests/
```

---

## Step 6 — Smoke Test the Full Stack

Run the CI pipeline locally to confirm everything works end-to-end:

```bash
cd ~/driver-validation-suite
bash scripts/run_ci_locally.sh
```

Or manually trigger just the QEMU boot:

```bash
~/VirtualMachines/qemu_bin/bin/qemu-system-x86_64 \
    -kernel ~/VirtualMachines/linux/arch/x86/boot/bzImage \
    -drive file=~/qemu-rootfs/rootfs.ext4,format=raw,if=virtio \
    -append "root=/dev/vda console=ttyS0 rw init=/bin/bash" \
    -m 1024 -smp 2 -nographic \
    -device gp_gpu \
    -virtfs local,path=~/qemu-rootfs/share,mount_tag=hostshare,security_model=mapped,id=hostshare
# Should drop into a root shell. Ctrl-A X to exit.
```

---

## Summary Checklist

```
[ ] KVM available: ls /dev/kvm
[ ] Custom QEMU built with gp_gpu device:
      ~/VirtualMachines/qemu_bin/bin/qemu-system-x86_64
[ ] Kernel bzImage present:
      ~/VirtualMachines/linux/arch/x86/boot/bzImage
[ ] Guest rootfs present:
      ~/qemu-rootfs/rootfs.ext4
[ ] 9p share directory exists:
      ~/qemu-rootfs/share/
[ ] GitLab runner registered with tag: dvf-runner
[ ] gitlab-runner user is in the kvm group
[ ] First deploy_share.sh run completed successfully
[ ] Smoke test QEMU boot succeeds
```

Once all boxes are ticked, every `git push` to the repo by any team member
automatically builds, deploys, and validates their changes through the full
QEMU-backed pipeline — with no further setup required on their part.

---

## Updating Infrastructure

| What changed | What to redo |
|---|---|
| `python-agent/` code changed significantly | Rebuild rootfs: `cd guest-os && make build`, then `cp output/dvf-guest.ext4 ~/qemu-rootfs/rootfs.ext4` |
| `qemu-accelerator-models/` device model `.c` changed | Recompile QEMU: `cd ~/VirtualMachines/qemu-src && make -j$(nproc) && make install` |
| New device model added to `qemu-accelerator-models/hw/misc/` | Add `files('<name>.c')` to `hw/misc/meson.build` in this repo, then recompile QEMU (Step 1c) |
| Kernel config change needed | Recompile kernel (Step 2) |
| New test binary added to repo | Nothing — CI `deploy-share` stage handles it automatically |
| New `.so` dependency in a test | Nothing — `bundle_libs.sh` in `deploy-share` handles it automatically |
