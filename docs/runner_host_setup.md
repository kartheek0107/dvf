# DVF Runner Host Setup Guide — Complete From Scratch

> **Audience:** Anyone setting up a new CI runner machine for the first time.
> This guide assumes you have never touched this project before.
> **Time required:** ~60–90 minutes on a fast internet connection.

---

## Table of Contents

1. [What is DVF and Why This Guide Exists](#1-what-is-dvf-and-why-this-guide-exists)
2. [Hardware & OS Requirements](#2-hardware--os-requirements)
3. [Clone the Repository](#3-clone-the-repository)
4. [Configure Your Environment](#4-configure-your-environment)
5. [Install System Packages](#5-install-system-packages)
6. [Enable KVM Virtualisation](#6-enable-kvm-virtualisation)
7. [Install Docker & Start Background Services](#7-install-docker--start-background-services)
8. [Build the Custom QEMU Binary](#8-build-the-custom-qemu-binary)
9. [Build the Linux Kernel](#9-build-the-linux-kernel)
10. [Build the Guest Root Filesystem](#10-build-the-guest-root-filesystem)
11. [Build the Go Orchestrator](#11-build-the-go-orchestrator)
12. [Build the C Test Binaries](#12-build-the-c-test-binaries)
13. [Deploy Artifacts to the 9p Share](#13-deploy-artifacts-to-the-9p-share)
14. [Register the GitLab Runner](#14-register-the-gitlab-runner)
15. [Smoke Test — Verify Everything Works](#15-smoke-test--verify-everything-works)
16. [Final Checklist](#16-final-checklist)
17. [Troubleshooting](#17-troubleshooting)
18. [Updating Infrastructure Later](#18-updating-infrastructure-later)

---

## 1. What is DVF and Why This Guide Exists

**DVF (Device Validation Framework)** is a CI system that:

1. Boots real Linux kernel VMs inside QEMU with a **custom simulated GPU device** (`gp_gpu`)
2. Loads a Linux kernel driver inside that VM
3. Runs compiled C/Python validation test programs against the driver
4. Reports pass/fail results back to GitLab CI

Every `git push` to this repo automatically triggers the full pipeline. For that to work, a **self-hosted GitLab runner** must be permanently running on a machine that has:
- QEMU with the custom `gp_gpu` device model compiled in
- A pre-built Linux kernel image
- A pre-built guest OS disk image (rootfs)

This guide sets all of that up from zero.

---

## 2. Hardware & OS Requirements

| Requirement | Minimum | Recommended |
|---|---|---|
| CPU | x86-64 with VT-x/AMD-V | 8+ cores |
| RAM | 8 GB | 16 GB+ |
| Disk | 50 GB free | 100 GB+ |
| OS | Fedora 38+ / RHEL 9+ / Ubuntu 22.04+ | Fedora 40 |
| Network | Internet access to clone repos | — |

**Critical:** VT-x or AMD-V (hardware virtualisation) **must be enabled in BIOS**. Without it, QEMU/KVM will not work.

---

## 3. Clone the Repository

Everything lives in this single repository. Clone it first — all subsequent steps run from inside it.

```bash
# Pick a location on your machine. $HOME is fine.
cd ~

# Clone the repo (replace the URL with your actual GitLab remote)
git clone https://github.com/kartheek0107/dvf.git

# Enter the directory — STAY HERE for the rest of this guide
cd driver-validation-suite
```

> **Note:** If you are on a machine with no public internet access, clone via SSH:
> `git clone git@https://github.com/kartheek0107/dvf.git`

Confirm you are in the right place:

```bash
ls
# You should see: README.md  scripts/  go-orchestrator/  python-agent/  ...
```

---

## 4. Configure Your Environment

The project uses a `.env` file to store all machine-specific paths. The defaults work for most setups without any changes.

```bash
# Copy the example file
cp .env.example .env

# Open it and read it — the defaults are usually fine
cat .env
```

The key variables and their defaults:

| Variable | Default | What it is |
|---|---|---|
| `KERNEL_BUILD_DIR` | `$HOME/VirtualMachines/linux` | Where the Linux kernel is cloned & built |
| `QEMU_VERSION` | `v8.2.0` | Which QEMU version to build |
| `ROOTFS_PATH` | `$HOME/qemu-rootfs/rootfs.ext4` | Path to the guest VM disk image |
| `SHARE_DIR` | `$HOME/qemu-rootfs/share` | 9p shared folder (host ↔ guest) |
| `VISHWA_CODE_DIR` | `$HOME/cdac/FW_SW_Milestone_2/code` | CDAC proprietary source (optional) |

Source the file so the variables are active in your shell:

```bash
source .env
echo $DVF_ROOT   # should print the full path to driver-validation-suite/
```

---

## 5. Install System Packages

### Fedora / RHEL / CentOS

```bash
sudo dnf install -y \
    gcc gcc-c++ make git \
    python3 python3-pip \
    golang \
    ninja-build meson pkg-config \
    glib2-devel pixman-devel zlib-devel \
    qemu-img rsync curl jq \
    bc bison flex openssl-devel \
    elfutils-libelf-devel \
    pocl pocl-devel ocl-icd
```

### Ubuntu / Debian

```bash
sudo apt-get update
sudo apt-get install -y \
    gcc g++ make git \
    python3 python3-pip \
    golang-go \
    ninja-build meson pkg-config \
    libglib2.0-dev libpixman-1-dev zlib1g-dev \
    qemu-utils rsync curl jq \
    bc bison flex libssl-dev \
    libelf-dev \
    pocl-opencl-icd
```

### Verify Go version (must be 1.22+)

```bash
go version
# Expected: go version go1.22.x linux/amd64 (or higher)
```

If Go is too old or missing, install manually:

```bash
# Download Go 1.22 (adjust version as needed)
wget https://go.dev/dl/go1.22.5.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.22.5.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc
go version
```

---

## 6. Enable KVM Virtualisation

KVM is required to run QEMU VMs at usable speed.

```bash
# Check if KVM device exists
ls -la /dev/kvm
```

**If `/dev/kvm` does not exist:**

```bash
# Check if the CPU supports virtualisation
grep -c 'vmx\|svm' /proc/cpuinfo
# Must return a number > 0. If 0, enable VT-x/AMD-V in BIOS.

# Load the KVM kernel modules
sudo modprobe kvm
sudo modprobe kvm_intel   # For Intel CPUs
# OR
sudo modprobe kvm_amd     # For AMD CPUs

# Make them load automatically on boot
echo kvm_intel | sudo tee -a /etc/modules-load.d/kvm.conf
```

**Add your user to the `kvm` group:**

```bash
sudo usermod -aG kvm $USER

# Apply the group change immediately (or log out and back in)
newgrp kvm

# Confirm
groups | grep kvm   # Should show 'kvm' in the list
```

**Verify:**

```bash
ls -la /dev/kvm
# crw-rw----. 1 root kvm 10, 232 ... /dev/kvm   ← this is correct
```

---

## 7. Install Docker & Start Background Services

DVF uses PostgreSQL (for test result storage) and Redis (for event streaming). The easiest way to run them is via Docker.

### Install Docker

**Fedora/RHEL:**
```bash
sudo dnf install -y docker docker-compose
sudo systemctl enable --now docker
sudo usermod -aG docker $USER
newgrp docker
```

**Ubuntu:**
```bash
sudo apt-get install -y docker.io docker-compose
sudo systemctl enable --now docker
sudo usermod -aG docker $USER
newgrp docker
```

**Verify:**
```bash
docker --version
docker-compose --version   # or: docker compose version
```

### Start PostgreSQL and Redis

From inside the `driver-validation-suite/` directory:

```bash
docker-compose up -d

# Verify both containers are running
docker-compose ps
# Should show: dvf-postgres  Running, dvf-redis  Running
```

If `docker-compose` is not available but `docker compose` (plugin form) is:

```bash
docker compose up -d
```

> **Note:** The database persists in named Docker volumes across reboots. It only resets if you run `docker compose down -v`.

---

## 8. Build the Custom QEMU Binary

DVF requires QEMU with the `gp_gpu` PCIe device model compiled in. The device model source code is inside this repo at `qemu-accelerator-models/`. A build script handles everything automatically.

```bash
# From the repo root:
bash qemu-accelerator-models/scripts/build_qemu_with_models.sh \
    "$(pwd)/qemu-accelerator-models" \
    "$(pwd)/builds/qemu-build" \
    "v8.2.0" \
    ""
```

This will:
1. Clone the QEMU `v8.2.0` source (~400 MB)
2. Inject the `gp_gpu` device model source into the QEMU build tree
3. Configure with `--target-list=x86_64-softmmu --enable-kvm`
4. Compile with all available CPU cores (takes **15–30 minutes**)

After completion, copy the binary to the canonical path:

```bash
mkdir -p builds/qemu
cp builds/qemu-build/qemu-system-x86_64 builds/qemu/qemu-system-x86_64
chmod +x builds/qemu/qemu-system-x86_64
```

**Verify the `gp_gpu` device is compiled in:**

```bash
builds/qemu/qemu-system-x86_64 -device gp_gpu,help 2>&1 | head -5
# Should print: gp_gpu options:  (NOT "unknown device 'gp_gpu'")
```

If you see `unknown device`, the build failed to include the device model — see [Troubleshooting](#17-troubleshooting).

---

## 9. Build the Linux Kernel

The guest VM boots with a compiled Linux kernel. This lives outside the repo (it is ~1 GB on disk).

### 9a. Clone the kernel source

```bash
mkdir -p ~/VirtualMachines
git clone --depth=1 --branch v6.6 \
    https://github.com/torvalds/linux \
    ~/VirtualMachines/linux

cd ~/VirtualMachines/linux
```

> `--depth=1` gets only the latest commit (saves ~4 GB of history). Takes ~5 minutes.

### 9b. Configure the kernel

```bash
cd ~/VirtualMachines/linux

# Start with the default x86-64 config
make defconfig

# Apply KVM guest optimisations
make kvm_guest.config

# Enable 9p virtfs (needed for host-to-guest file sharing)
scripts/config --enable CONFIG_NET_9P
scripts/config --enable CONFIG_NET_9P_VIRTIO
scripts/config --enable CONFIG_9P_FS
scripts/config --enable CONFIG_9P_FS_POSIX_ACL
scripts/config --enable CONFIG_VIRTIO_PCI
scripts/config --enable CONFIG_VIRTIO_BLK
scripts/config --enable CONFIG_VIRTIO_NET

# Resolve any new config dependencies
make olddefconfig
```

### 9c. Compile

```bash
# This takes 10-25 minutes depending on CPU count
make -j$(nproc) bzImage
```

### 9d. Verify

```bash
ls -lh ~/VirtualMachines/linux/arch/x86/boot/bzImage
# Should be ~10-14 MB
```

Go back to the repo root:

```bash
cd ~/driver-validation-suite
```

---

## 10. Build the Guest Root Filesystem

The guest VM needs a disk image (rootfs) with Alpine Linux + the DVF Python agent pre-installed. This is built using HashiCorp Packer.

### 10a. Install Packer

**Fedora/RHEL:**
```bash
# Check if already installed
packer version

# If not installed, add HashiCorp repo:
sudo dnf install -y dnf-plugins-core
sudo dnf config-manager --add-repo https://rpm.releases.hashicorp.com/fedora/hashicorp.repo
sudo dnf install -y packer
```

**Ubuntu:**
```bash
wget -O- https://apt.releases.hashicorp.com/gpg | sudo gpg --dearmor -o /usr/share/keyrings/hashicorp.gpg
echo "deb [signed-by=/usr/share/keyrings/hashicorp.gpg] https://apt.releases.hashicorp.com $(lsb_release -cs) main" \
    | sudo tee /etc/apt/sources.list.d/hashicorp.list
sudo apt-get update && sudo apt-get install -y packer
```

### 10b. Build the image

```bash
cd ~/driver-validation-suite/guest-os

make build
# Downloads Alpine Linux ISO, boots a temp QEMU VM, installs the DVF agent.
# Takes 10-15 minutes.
```

### 10c. Install to the expected path

```bash
make install INSTALL_TARGET=$HOME/qemu-rootfs

ls -lh ~/qemu-rootfs/rootfs.ext4
# Should be 200-500 MB
```

> **You only need to rebuild this if:** the Python agent code (`python-agent/`) changes significantly. Normal test additions never require a rootfs rebuild — the agent reads test binaries from the 9p share at runtime.

---

## 11. Build the Go Orchestrator

The orchestrator is the control plane that manages VMs and test execution.

```bash
cd ~/driver-validation-suite/go-orchestrator

go build -v -o orchestrator ./cmd/orchestrator/

# Verify
ls -lh orchestrator
# Should show a binary, ~15-30 MB
```

Also run the unit tests to confirm everything compiles correctly:

```bash
go test -v -count=1 ./...
# All tests should pass
```

Go back to repo root:

```bash
cd ~/driver-validation-suite
```

---

## 12. Build the C Test Binaries

The C test binaries are the actual validation programs that run inside the guest VM.

```bash
cd ~/driver-validation-suite/c-test-binaries

make -j$(nproc)

# Verify
ls -lh */
# Should show compiled ELF binaries like test_register_rw, etc.
```

Go back to repo root:

```bash
cd ~/driver-validation-suite
```

---

## 13. Deploy Artifacts to the 9p Share

The 9p share is a directory on the host that QEMU mounts inside the guest VM. The guest agent reads all drivers and test binaries from here.

```bash
cd ~/driver-validation-suite

# Create the share directory
mkdir -p ~/qemu-rootfs/share

# Run the deploy script
# --skip-vishwa-build skips the optional CDAC proprietary source (safe to use if you don't have it)
bash scripts/deploy_share.sh --skip-vishwa-build
```

Verify the share layout:

```bash
ls ~/qemu-rootfs/share/
# Expected output:
# gpgpu_pcie_ep_driver.ko   python-agent/   start_agent.sh   dvf_tests/
```

---

## 14. Register the GitLab Runner

This registers the machine as a CI runner so GitLab routes pipeline jobs to it.

### 14a. Install the GitLab Runner binary

**Option 1 — Use the RPM bundled in this repo:**
```bash
sudo rpm -ivh ~/driver-validation-suite/gitlab-runner_amd64.rpm
```

**Option 2 — Install from GitLab's official repository:**

*Fedora/RHEL:*
```bash
curl -L "https://packages.gitlab.com/install/repositories/runner/gitlab-runner/script.rpm.sh" | sudo bash
sudo dnf install -y gitlab-runner
```

*Ubuntu:*
```bash
curl -L "https://packages.gitlab.com/install/repositories/runner/gitlab-runner/script.deb.sh" | sudo bash
sudo apt-get install -y gitlab-runner
```

### 14b. Register with your GitLab instance

```bash
sudo gitlab-runner register
```

Answer the prompts:

| Prompt | What to enter |
|---|---|
| GitLab instance URL | `https://your-gitlab.example.com/` |
| Registration token | Go to GitLab → Your Project → Settings → CI/CD → Runners → Registration token |
| Description | `dvf-runner-<hostname>` (e.g. `dvf-runner-workstation1`) |
| Tags | **`dvf-runner`** ← this is critical, must match `.gitlab-ci.yml` exactly |
| Executor | `shell` |

> **Why `shell` executor?** The DVF pipeline needs direct access to `/dev/kvm`, the QEMU binary, and the rootfs on the host filesystem. The `shell` executor runs jobs directly on the host without any container isolation, which is what we need.

### 14c. Grant KVM access to the runner

The GitLab runner runs jobs as the `gitlab-runner` user. That user needs to be in the `kvm` group:

```bash
sudo usermod -aG kvm gitlab-runner

# Restart the runner service so the group change takes effect
sudo systemctl restart gitlab-runner
```

### 14d. Enable and start the runner service

```bash
sudo systemctl enable --now gitlab-runner
sudo systemctl status gitlab-runner
# Should show: Active: active (running)
```

### 14e. Verify the runner appears in GitLab

Go to your GitLab project → Settings → CI/CD → Runners. You should see your runner listed with the `dvf-runner` tag and a green dot indicating it is online.

---

## 15. Smoke Test — Verify Everything Works

### 15a. Manual QEMU boot test

This confirms QEMU, the kernel, and the rootfs all work together:

```bash
~/driver-validation-suite/builds/qemu/qemu-system-x86_64 \
    -kernel ~/VirtualMachines/linux/arch/x86/boot/bzImage \
    -drive file=~/qemu-rootfs/rootfs.ext4,format=raw,if=virtio \
    -append "root=/dev/vda console=ttyS0 rw init=/bin/bash" \
    -m 1024 -smp 2 -nographic \
    -device gp_gpu \
    -virtfs local,path=~/qemu-rootfs/share,mount_tag=hostshare,security_model=mapped,id=hostshare
```

You should see kernel boot messages and drop into a root shell:
```
root@(none):/#
```

Inside the guest, verify the share is accessible:
```bash
mount -t 9p -o trans=virtio,version=9p2000.L hostshare /mnt/share
ls /mnt/share
# Should show: gpgpu_pcie_ep_driver.ko  python-agent/  ...
```

Exit the guest: press **Ctrl-A**, then **X**.

### 15b. Run the CI pipeline locally

This simulates exactly what happens when you `git push`:

```bash
cd ~/driver-validation-suite

# Start the orchestrator (uses in-memory storage, no Docker needed for this test)
./go-orchestrator/orchestrator --config go-orchestrator/configs --storage memory \
    > /tmp/orchestrator-test.log 2>&1 &
ORC_PID=$!

# Wait for it to be ready
sleep 3
curl -sf http://localhost:9080/healthz && echo "Orchestrator is healthy!"

# Run the CI impact analyzer
python3 scripts/ci_impact_analyzer.py

# Clean up
kill $ORC_PID
```

### 15c. Trigger a real pipeline

Make a trivial change and push it:

```bash
echo "# test" >> README.md
git add README.md
git commit -m "chore: trigger CI smoke test"
git push
```

Go to GitLab → Your Project → CI/CD → Pipelines. You should see all four stages (`smoke` → `build` → `deploy` → `test`) go green.

---

## 16. Final Checklist

Before declaring the runner ready, tick every box:

```
[ ] git clone completed — repo is on the machine
[ ] .env created from .env.example and sourced
[ ] /dev/kvm exists and current user is in kvm group
[ ] Docker running — docker-compose ps shows postgres + redis healthy
[ ] Custom QEMU built:
      builds/qemu/qemu-system-x86_64 -device gp_gpu,help  ← prints properties, not "unknown device"
[ ] Kernel bzImage present:
      ~/VirtualMachines/linux/arch/x86/boot/bzImage
[ ] Guest rootfs present:
      ~/qemu-rootfs/rootfs.ext4  (200-500 MB)
[ ] 9p share populated:
      ~/qemu-rootfs/share/ contains gpgpu_pcie_ep_driver.ko and python-agent/
[ ] Go orchestrator binary built:
      go-orchestrator/orchestrator
[ ] C test binaries built:
      c-test-binaries/*/test_*  (ELF binaries)
[ ] GitLab runner registered with tag: dvf-runner
[ ] gitlab-runner user is in the kvm group
[ ] Manual QEMU boot test passed (guest drops into shell)
[ ] Local CI test passed (orchestrator starts, analyzer runs)
[ ] Real git push triggers green pipeline in GitLab
```

---

## 17. Troubleshooting

### "unknown device 'gp_gpu'"

The QEMU binary was not built with the device model. Re-run Step 8. Make sure the build script runs without errors.

```bash
# Check if the build completed
ls -lh builds/qemu/qemu-system-x86_64
# Re-run the build:
bash qemu-accelerator-models/scripts/build_qemu_with_models.sh \
    "$(pwd)/qemu-accelerator-models" "$(pwd)/builds/qemu-build" "v8.2.0" ""
```

### KVM permission denied

```bash
# Check your groups
groups
# If kvm is missing:
sudo usermod -aG kvm $USER && newgrp kvm
# For the runner:
sudo usermod -aG kvm gitlab-runner && sudo systemctl restart gitlab-runner
```

### QEMU: No accelerator found

KVM module not loaded. Run:
```bash
sudo modprobe kvm_intel   # or kvm_amd
lsmod | grep kvm
```

### Orchestrator fails to start — port already in use

```bash
# Kill anything on ports 9080 or 50051
for PORT in 9080 50051; do
    PID=$(ss -tlnp "sport = :$PORT" | grep -oP 'pid=\K[0-9]+' | head -1)
    [ -n "$PID" ] && sudo kill -9 $PID && echo "Killed $PID on $PORT"
done
```

### Go build fails — version too old

```bash
go version
# If < 1.22, install a newer version (see Step 5)
```

### 9p share not mounting in guest

Make sure the kernel was compiled with 9p support (Step 9b). Also check that the `security_model=mapped` option is correct for your setup — try `security_model=none` if you get permission errors.

### GitLab runner not picking up jobs

1. Check the runner is online: GitLab → Settings → CI/CD → Runners
2. Verify the tag matches exactly: must be `dvf-runner` (no spaces, lowercase)
3. Check service logs: `sudo journalctl -u gitlab-runner -f`

---

## 18. Updating Infrastructure Later

| What changed | What to redo |
|---|---|
| `python-agent/` code changed significantly | `cd guest-os && make build && make install` → `cp output/dvf-guest.ext4 ~/qemu-rootfs/rootfs.ext4` |
| `qemu-accelerator-models/` device model `.c` changed | Re-run Step 8 (rebuild QEMU) |
| New device model added to `qemu-accelerator-models/hw/misc/` | Add `files('<name>.c')` to `hw/misc/meson.build`, then re-run Step 8 |
| Kernel config change needed | Re-run Steps 9b & 9c |
| New test binary added to `c-test-binaries/` | Nothing — CI `deploy-share` stage handles it automatically |
| New `.so` dependency in a test | Nothing — `bundle_libs.sh` in deploy-share handles it |
| GitLab runner token rotated | Re-run Step 14b (`sudo gitlab-runner register`) |

---

Once all boxes in the checklist are ticked, **every `git push` automatically builds, deploys, and validates** changes through the full QEMU-backed pipeline — no further setup required.
