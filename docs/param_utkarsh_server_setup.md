# DVF Setup on PARAM Utkarsh Server — Absolute Beginner Guide

> **Who is this for?** Anyone — even if you have never used a Linux terminal before.
> **What will you achieve?** A fully working DVF (Device Validation Framework) pipeline running on the PARAM Utkarsh server.
> **Time needed:** About 60–90 minutes (most of it is waiting for things to compile).
> **Do I need admin/root access?** No. Everything here works as a normal user.

---

## Before You Start — Quick Linux Survival Guide

If you already know basic Linux commands, skip to [Step 1](#step-1-log-into-the-server).

A **terminal** (also called "shell" or "command line") is a text window where you type commands.
Here are the only commands you need to know:

| Command | What it does | Example |
|---|---|---|
| `pwd` | **P**rint **W**orking **D**irectory — shows where you are right now | `pwd` → `/home/john` |
| `cd <folder>` | **C**hange **D**irectory — move into a folder | `cd Documents` |
| `cd ~` | Go back to your home folder (shortcut) | `cd ~` |
| `cd ..` | Go up one folder | `cd ..` |
| `ls` | **L**i**s**t files in the current folder | `ls` |
| `ls -la` | List ALL files (including hidden ones) with details | `ls -la` |
| `mkdir -p <name>` | **M**a**k**e a new **dir**ectory (folder). `-p` means create parent folders too | `mkdir -p a/b/c` |
| `cp <from> <to>` | **C**o**p**y a file | `cp file.txt backup.txt` |
| `cat <file>` | Print file contents to screen | `cat README.md` |
| `echo "hi"` | Print text to screen | `echo "hello world"` |
| `Ctrl+C` | STOP whatever is currently running | — |

> **Tip:** You can copy any command from this guide and paste it into your terminal. On most terminals, paste is `Ctrl+Shift+V` (not `Ctrl+V`).

---

## Why PARAM Utkarsh is Different from Your Laptop

On your laptop you can install anything with `sudo apt install ...`. On PARAM Utkarsh:

- ❌ **No `sudo`** — you cannot install system packages
- ❌ **No Docker** — the Docker daemon is not available to regular users
- ✅ **Environment Modules** — software is loaded with `module load gcc` instead of installing it
- ✅ **Two storage areas** — a small `/home` (limited space) and a big `/scratch` (lots of space)
- ✅ **Everything compiles from source** — inside your own directories, no admin needed

---

## Step 1: Log Into the Server

Open a terminal on your laptop (on Windows use PowerShell, PuTTY, or WSL; on Mac/Linux use Terminal).

Type this command and press Enter. Replace `your_username` with the username CDAC gave you:

```bash
ssh your_username@paramutkarsh.cdac.in
```

It will ask for your password. Type it and press Enter (the password will NOT show on screen — that is normal, just type it blind and press Enter).

**Check that you are logged in.** Run:

```bash
whoami
```

This should print your username. If it does, you are on the server. 🎉

**Check where you are right now:**

```bash
pwd
```

This should print something like `/home/your_username`. This is your **home directory**.

---

## Step 2: Create Your Workspace Folders

The server has two storage areas:

1. **`/home/your_username`** — small, limited to ~10-20 GB. Good for code.
2. **`/scratch/your_username`** — very large, fast storage. Good for big build files.

We will keep our code in `/home` and put all the big compiled files in `/scratch`.

**Run these commands one by one** (copy-paste each line, press Enter after each):

```bash
mkdir -p /scratch/$USER/dvf-workspace/kernel
```

```bash
mkdir -p /scratch/$USER/dvf-workspace/qemu-rootfs/share
```

```bash
mkdir -p /scratch/$USER/dvf-workspace/builds
```

**Check that the folders were created:**

```bash
ls /scratch/$USER/dvf-workspace
```

You should see: `builds  kernel  qemu-rootfs`

> **What is `$USER`?** It is a shortcut that automatically becomes your username. So if your username is `john`, then `/scratch/$USER` becomes `/scratch/john`.

---

## Step 3: Load the Software Tools

On your laptop you install software with `apt install` or `dnf install`. On PARAM Utkarsh, software is already installed but "hidden" — you need to **load** it.

**First, see what software is available:**

```bash
module avail
```

This will print a long list. Don't worry about reading it all.

**Now load the tools we need.** Run each line one by one:

```bash
module load gcc/12.2.0
```

```bash
module load go/1.22.0
```

```bash
module load python/3.10.8
```

```bash
module load meson/1.1.1
```

```bash
module load ninja/1.11.1
```

> **Note:** The exact version numbers (like `12.2.0`) might be different on your server. If a command fails with "module not found", run `module avail gcc` to see what versions are available, and use that version number instead.

**Check that everything loaded correctly:**

```bash
gcc --version
```

```bash
go version
```

```bash
python3 --version
```

Each command should print a version number (not an error).

**Make these load automatically every time you log in** (so you don't have to type them every time):

```bash
cat << 'EOF' >> ~/.bashrc

# DVF tools — loaded automatically on login
module load gcc/12.2.0 2>/dev/null
module load go/1.22.0 2>/dev/null
module load python/3.10.8 2>/dev/null
module load meson/1.1.1 2>/dev/null
module load ninja/1.11.1 2>/dev/null
EOF
```

> **What is `~/.bashrc`?** It is a hidden file that runs automatically every time you open a terminal. The command above adds our `module load` lines to the end of that file.

---

## Step 4: Set Up Internet Proxy (if needed)

PARAM Utkarsh may sit behind a firewall. If `git clone` or `curl` commands fail later, you need to set up a proxy. **Try skipping this step first** — come back here only if downloads fail.

Ask your server administrator for the proxy address. Then run:

```bash
export http_proxy="http://proxy.paramutkarsh.cdac.in:8080"
export https_proxy="http://proxy.paramutkarsh.cdac.in:8080"
export no_proxy="localhost,127.0.0.1"
```

```bash
git config --global http.proxy $http_proxy
git config --global https.proxy $https_proxy
export GOPROXY="https://goproxy.io,direct"
```

> **Replace** `proxy.paramutkarsh.cdac.in:8080` with the actual proxy address your admin gives you.

---

## Step 5: Download the DVF Project Code

**Go to your home folder:**

```bash
cd ~
```

**Confirm you are in the home folder:**

```bash
pwd
```

Should print `/home/your_username`.

**Download (clone) the project:**

```bash
git clone https://github.com/kartheek0107/dvf.git driver-validation-suite
```

This downloads the project into a folder called `driver-validation-suite`.

**Go inside the project folder:**

```bash
cd driver-validation-suite
```

**Confirm you are in the right place:**

```bash
pwd
```

Should print `/home/your_username/driver-validation-suite`.

**Check what files are here:**

```bash
ls
```

You should see files like `README.md`, `scripts/`, `go-orchestrator/`, etc.

---

## Step 6: Create the Configuration File

DVF needs to know where to put files on this server. We create a config file called `.env`.

**Run this entire block as one command** (copy ALL of it and paste):

```bash
cat << 'EOF' > .env
# DVF Configuration for PARAM Utkarsh Server
export DVF_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export KERNEL_BUILD_DIR="/scratch/$USER/dvf-workspace/kernel/linux"
export QEMU_VERSION="v8.2.0"
export ROOTFS_PATH="/scratch/$USER/dvf-workspace/qemu-rootfs/rootfs.ext4"
export SHARE_DIR="/scratch/$USER/dvf-workspace/qemu-rootfs/share"
export DVF_STORAGE="memory"
EOF
```

**Load the config file:**

```bash
source .env
```

**Check it worked:**

```bash
echo $DVF_ROOT
```

This should print the full path to `driver-validation-suite` (e.g., `/home/john/driver-validation-suite`).

---

## Step 7: Create Shortcut Links (Symlinks)

The DVF orchestrator program looks for files in specific locations inside your home folder. But we put the actual files on `/scratch` (because `/home` is too small). We solve this by creating **symlinks** — think of them as shortcuts that point from one location to another.

**Create the shortcuts:**

```bash
mkdir -p ~/VirtualMachines
```

```bash
ln -sfn /scratch/$USER/dvf-workspace/kernel/linux ~/VirtualMachines/linux
```

```bash
ln -sfn /scratch/$USER/dvf-workspace/qemu-rootfs ~/qemu-rootfs
```

**Check that the shortcuts exist:**

```bash
ls -la ~/VirtualMachines/
```

You should see: `linux -> /scratch/your_username/dvf-workspace/kernel/linux`

```bash
ls -la ~/qemu-rootfs
```

You should see: `/home/your_username/qemu-rootfs -> /scratch/your_username/dvf-workspace/qemu-rootfs`

> **Why do we need this?** The DVF program has a config file (`global_config.json`) that says "look for the kernel at `$HOME/VirtualMachines/linux`". Instead of editing that file, we just create a shortcut from that location to where we actually put the kernel. Smart, right?

---

## Step 8: Build the Custom QEMU (15–30 minutes)

QEMU is a virtual machine program. DVF uses a custom version of QEMU that includes our GPU device simulator.

**Make sure you are in the project folder:**

```bash
cd ~/driver-validation-suite
```

**Confirm:**

```bash
pwd
```

Should print `/home/your_username/driver-validation-suite`.

**Load the config (if you haven't already):**

```bash
source .env
```

**Start the QEMU build** (this takes 15–30 minutes — go get a coffee ☕):

```bash
bash qemu-accelerator-models/scripts/build_qemu_with_models.sh \
    "$DVF_ROOT/qemu-accelerator-models" \
    "$DVF_ROOT/builds/qemu-build" \
    "v8.2.0" \
    ""
```

> **The `\` at the end of a line** means "this command continues on the next line". Copy-paste the entire block including all four lines.

**When it finishes, copy the built file to the right location:**

```bash
mkdir -p builds/qemu
```

```bash
cp builds/qemu-build/qemu-system-x86_64 builds/qemu/qemu-system-x86_64
```

```bash
chmod +x builds/qemu/qemu-system-x86_64
```

**Verify the build worked:**

```bash
builds/qemu/qemu-system-x86_64 -device gp_gpu,help 2>&1 | head -5
```

✅ **Success** = you see output mentioning `gp_gpu` properties.
❌ **Failure** = you see `unknown device 'gp_gpu'` — the build went wrong, run the build command again.

> **If your `/home` folder runs out of space** during this step (you see "disk quota exceeded"), use this alternative approach that builds on `/scratch` instead:
> ```bash
> bash qemu-accelerator-models/scripts/build_qemu_with_models.sh \
>     "$DVF_ROOT/qemu-accelerator-models" \
>     "/scratch/$USER/dvf-workspace/qemu-build" \
>     "v8.2.0" \
>     ""
> ln -sfn /scratch/$USER/dvf-workspace/qemu-build $DVF_ROOT/builds/qemu-build
> mkdir -p builds/qemu
> cp builds/qemu-build/qemu-system-x86_64 builds/qemu/qemu-system-x86_64
> chmod +x builds/qemu/qemu-system-x86_64
> ```

---

## Step 9: Build the Linux Kernel (10–25 minutes)

The virtual machines we run need a Linux kernel. We download and compile one.

**Go to the scratch workspace:**

```bash
cd /scratch/$USER/dvf-workspace/kernel
```

**Confirm you are there:**

```bash
pwd
```

Should print `/scratch/your_username/dvf-workspace/kernel`.

**Download the Linux kernel source code** (takes ~5 minutes):

```bash
git clone --depth=1 --branch v6.6 https://github.com/torvalds/linux linux
```

**Go inside the kernel folder:**

```bash
cd linux
```

**Configure the kernel** (run each line one by one):

```bash
make defconfig
```

```bash
make kvm_guest.config
```

```bash
scripts/config --enable CONFIG_NET_9P
```

```bash
scripts/config --enable CONFIG_NET_9P_VIRTIO
```

```bash
scripts/config --enable CONFIG_9P_FS
```

```bash
scripts/config --enable CONFIG_9P_FS_POSIX_ACL
```

```bash
scripts/config --enable CONFIG_VIRTIO_PCI
```

```bash
scripts/config --enable CONFIG_VIRTIO_BLK
```

```bash
scripts/config --enable CONFIG_VIRTIO_NET
```

```bash
make olddefconfig
```

**Compile the kernel** (takes 10–25 minutes ☕):

```bash
make -j$(nproc) bzImage
```

> **What is `-j$(nproc)`?** It tells the compiler to use all available CPU cores to go faster. `nproc` prints how many cores the machine has.

**Check the kernel was built:**

```bash
ls -lh arch/x86/boot/bzImage
```

You should see a file that is about 10–14 MB in size. If you see it, the kernel built successfully! ✅

---

## Step 10: Set Up the Guest VM Disk Image

The virtual machine needs a disk image (like a virtual hard drive).

**Option A — Copy a pre-built image (Recommended, ask your team lead):**

If someone on your team already has a `rootfs.ext4` file, copy it:

```bash
cp /path/to/prebuilt/rootfs.ext4 /scratch/$USER/dvf-workspace/qemu-rootfs/rootfs.ext4
```

> Replace `/path/to/prebuilt/rootfs.ext4` with the actual path your team lead gives you.

**Option B — Build it yourself (requires Packer):**

```bash
cd ~/driver-validation-suite/guest-os
```

```bash
make build
```

```bash
make install INSTALL_TARGET=/scratch/$USER/dvf-workspace/qemu-rootfs
```

**Confirm the image exists:**

```bash
ls -lh /scratch/$USER/dvf-workspace/qemu-rootfs/rootfs.ext4
```

You should see a file that is 200–500 MB. ✅

---

## Step 11: Build the DVF Programs

**Go back to the project folder:**

```bash
cd ~/driver-validation-suite
```

**Load the config:**

```bash
source .env
```

**Build the Go orchestrator** (the "brain" of DVF):

```bash
cd go-orchestrator
```

```bash
go build -v -o orchestrator ./cmd/orchestrator/
```

**Check it built correctly:**

```bash
ls -lh orchestrator
```

You should see a file that is about 15–30 MB. ✅

**Build the C test programs:**

```bash
cd ~/driver-validation-suite/c-test-binaries
```

```bash
make -j$(nproc)
```

---

## Step 12: Deploy Everything to the Shared Folder

The shared folder is where the virtual machine reads test files from. We need to copy all our built files there.

**Go back to the project folder and load the config:**

```bash
cd ~/driver-validation-suite
```

```bash
source .env
```

**Create the share folder (if not already existing):**

```bash
mkdir -p $SHARE_DIR
```

**Run the deploy script:**

```bash
bash scripts/deploy_share.sh --skip-vishwa-build
```

> **What is `--skip-vishwa-build`?** It skips building the CDAC Vishwa proprietary code. If you don't have the Vishwa source code, this flag is required — otherwise the script will error.

**Check the shared folder has the right files:**

```bash
ls $SHARE_DIR
```

You should see: `gpgpu_pcie_ep_driver.ko`, `dvf_tests/`, `python-agent/`, `start_agent.sh` ✅

---

## Step 13: Install GitLab Runner (No Admin Required)

The GitLab Runner is a program that listens for code pushes and automatically runs the test pipeline.

**Create a folder for local programs:**

```bash
mkdir -p ~/.local/bin
```

**Download the GitLab Runner binary:**

```bash
curl -L --output ~/.local/bin/gitlab-runner https://gitlab-runner-downloads.s3.amazonaws.com/latest/binaries/gitlab-runner-linux-amd64
```

**Make it executable:**

```bash
chmod +x ~/.local/bin/gitlab-runner
```

**Add it to your PATH** (so you can run it from anywhere):

```bash
export PATH="$HOME/.local/bin:$PATH"
```

**Also add this to your `.bashrc` so it persists:**

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
```

**Check it works:**

```bash
gitlab-runner --version
```

You should see a version number printed. ✅

**Register the runner with your GitLab project:**

```bash
gitlab-runner register
```

It will ask you several questions. Answer them like this:

| It asks... | You type... |
|---|---|
| GitLab instance URL | The URL of your GitLab server (ask your team lead) |
| Registration token | Go to GitLab → Your Project → Settings → CI/CD → Runners → copy the token |
| Description | `param-utkarsh-runner` |
| Tags | `dvf-runner` |
| Executor | `shell` |

---

## Step 14: Start Everything Up

### 14a. Check KVM Access

```bash
ls -l /dev/kvm
```

If this prints a line with `crw` at the start, you have access. ✅

If it says "Permission denied" or "No such file", email your server admin and ask:
> "Please grant me access to /dev/kvm. Command: `sudo setfacl -m u:MY_USERNAME:rw /dev/kvm`"

### 14b. Test QEMU Manually

**Go to the project folder and load config:**

```bash
cd ~/driver-validation-suite
```

```bash
source .env
```

**Boot a virtual machine:**

```bash
$DVF_ROOT/builds/qemu/qemu-system-x86_64 \
    -kernel /scratch/$USER/dvf-workspace/kernel/linux/arch/x86/boot/bzImage \
    -drive file=/scratch/$USER/dvf-workspace/qemu-rootfs/rootfs.ext4,format=raw,if=virtio \
    -append "root=/dev/vda console=ttyS0 rw init=/bin/bash" \
    -m 1024 -smp 2 -nographic \
    -device gp_gpu \
    -virtfs local,path=/scratch/$USER/dvf-workspace/qemu-rootfs/share,mount_tag=hostshare,security_model=mapped,id=hostshare
```

You should see a wall of text (kernel boot messages) and eventually a `root@(none):/#` prompt. That means the VM booted! ✅

**To exit the VM:** Press `Ctrl+A`, release both keys, then press `X`.

### 14c. Start the Orchestrator

We use `tmux` to keep programs running even after you close your terminal.

> **What is `tmux`?** It is a program that creates "virtual terminals" that stay alive on the server even when you disconnect. Think of it like leaving a program running in the background.

**Start a tmux session:**

```bash
tmux new -s dvf-orchestrator
```

You are now inside tmux (the bottom of your screen might turn green).

**Inside tmux, run the orchestrator:**

```bash
cd ~/driver-validation-suite
source .env
./go-orchestrator/orchestrator --config configs --storage memory
```

You should see log messages saying the orchestrator started. ✅

**Detach from tmux** (this leaves it running in the background):

Press `Ctrl+B`, release both keys, then press `D`.

You are now back in your normal terminal, and the orchestrator is still running in the background!

**To go back to the orchestrator later:**

```bash
tmux attach -t dvf-orchestrator
```

### 14d. Test the Health Endpoint

Open another terminal (or from the same one after detaching from tmux):

```bash
curl http://localhost:9080/healthz
```

You should see: `{"status":"OK"}` ✅

### 14e. Start the GitLab Runner

**Start another tmux session for the runner:**

```bash
tmux new -s dvf-runner
```

**Inside tmux, start the runner:**

```bash
export PATH="$HOME/.local/bin:$PATH"
gitlab-runner run
```

**Detach from tmux:** Press `Ctrl+B`, then `D`.

---

## Troubleshooting — When Things Go Wrong

### "Permission denied: /dev/kvm"

Email your server admin and ask for KVM access (see Step 14a).

### "Disk quota exceeded"

You ran out of space in `/home`. Check what is using space:

```bash
du -sh ~/driver-validation-suite/builds/*
```

Move the QEMU build to scratch:

```bash
mv ~/driver-validation-suite/builds/qemu-build /scratch/$USER/dvf-workspace/qemu-build
ln -sfn /scratch/$USER/dvf-workspace/qemu-build ~/driver-validation-suite/builds/qemu-build
```

### "module not found" when loading gcc/go/python

The exact module names differ between servers. Find what is available:

```bash
module avail gcc
module avail go
module avail python
```

Then use the version number that shows up.

### "Connection refused" when running `curl http://localhost:9080/healthz`

The orchestrator is not running. Reattach to tmux and check:

```bash
tmux attach -t dvf-orchestrator
```

If it crashed, read the error message and restart it.

### "unknown device gp_gpu"

The QEMU build did not include the custom device. Go back to [Step 8](#step-8-build-the-custom-qemu-1530-minutes) and rebuild.

### SSH disconnected and everything stopped

If you used `tmux` (as this guide says), your programs are still running! Just reconnect:

```bash
ssh your_username@paramutkarsh.cdac.in
tmux attach -t dvf-orchestrator
```

If you did NOT use tmux, you need to start the orchestrator and runner again.

### Git clone fails — "Could not resolve host"

You need the proxy settings from [Step 4](#step-4-set-up-internet-proxy-if-needed).

---

## Final Checklist — Tick Every Box

Go through this list and make sure each one works:

```
[ ] I can log into the server with SSH
[ ] pwd shows /home/my_username
[ ] /scratch/$USER/dvf-workspace exists (ls shows it)
[ ] gcc --version prints a version number
[ ] go version prints go1.22 or higher
[ ] python3 --version prints a version number
[ ] The project is cloned: ls ~/driver-validation-suite shows files
[ ] source .env works and echo $DVF_ROOT prints the project path
[ ] Symlinks exist:
      ls -la ~/VirtualMachines/linux  shows an arrow →
      ls -la ~/qemu-rootfs            shows an arrow →
[ ] QEMU built: builds/qemu/qemu-system-x86_64 -device gp_gpu,help shows properties
[ ] Kernel built: ls ~/VirtualMachines/linux/arch/x86/boot/bzImage shows a ~10MB file
[ ] Rootfs exists: ls ~/qemu-rootfs/rootfs.ext4 shows a ~200-500MB file
[ ] Orchestrator built: ls ~/driver-validation-suite/go-orchestrator/orchestrator shows a file
[ ] Share deployed: ls $SHARE_DIR shows gpgpu_pcie_ep_driver.ko and python-agent/
[ ] Orchestrator is running: curl http://localhost:9080/healthz returns OK
[ ] GitLab runner is running: tmux attach -t dvf-runner shows it listening
```

**Once everything is checked, every `git push` to your repository will automatically build and test your code through the full pipeline!** 🎉

---

## Quick Reference — Useful tmux Commands

| Keys | What it does |
|---|---|
| `tmux new -s name` | Create a new session called "name" |
| `tmux attach -t name` | Go back to session "name" |
| `tmux ls` | List all running sessions |
| `Ctrl+B`, then `D` | Detach (leave session running in background) |
| `Ctrl+B`, then `%` | Split screen vertically |
| `Ctrl+B`, then `"` | Split screen horizontally |
| `exit` | Close the current tmux pane/session |
