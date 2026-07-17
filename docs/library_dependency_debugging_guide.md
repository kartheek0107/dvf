# DVF Library Dependency & Debugging Guide

This guide describes how to add, bundle, and troubleshoot shared library dependencies (`.so` files) in the Driver Validation Framework (DVF). It captures lessons learned from real-world compiler/runtime mismatches, 9p mount behaviors, and differences between Fedora and Ubuntu hosts.

---

## 1. Key Core Concepts

To successfully run binaries inside the QEMU VM without mutating the root filesystem, we use **Strategy 1 (Self-Contained Bundles)**. You must understand how these components interact:

### A. Host-Installed vs. Vishwa-Internal Libraries
* **Host-Installed Libraries:** Libraries installed via the host OS package manager (e.g., `libOpenCL.so`, `libjpeg.so`). These are easily resolvable on your machine.
* **Vishwa-Internal Libraries:** Custom libraries built from the Vishwa source tree (e.g., `libgpurt.so`, `libvishwa.so`). These are **not** installed in host system paths (`/usr/lib64`), meaning the host linker `ldd` cannot locate them automatically.

### B. Compile-Time vs. Runtime Libraries
* **Devel/Dev Packages (Compile-time):** Contains C header files (`.h`) and unversioned symlinks (e.g., `libfoo.so`). Only needed on the host to compile the test.
* **Runtime Packages (Runtime):** Contains the versioned shared object files (e.g., `libfoo.so.0`, `libfoo.so.0.3.29`). These are what you must bundle and ship to the VM.

### C. RPATH / RUNPATH (`$ORIGIN/lib`)
When compiling, we bake the relative path `$ORIGIN/lib` into the ELF binary's headers. 
* `$ORIGIN` is a special variable that tells the dynamic loader to find libraries relative to the directory containing the binary itself at runtime.

### D. The Dynamic Linker & Glibc Mismatch
* A compiled binary depends on `libc.so.6`.
* If your host system has a newer GLIBC version than the guest VM, loading the host's `libc.so.6` inside the VM using the guest's default dynamic linker (`/lib64/ld-linux-x86-64.so.2`) will trigger symbol errors like:
  `symbol lookup error: .../lib/libc.so.6: undefined symbol: __tunables_init`
* **Solution:** Run the test binary by invoking the copied host dynamic linker explicitly, passing the exact library search paths.

---

## 2. Step-by-Step Dependency Workflow

Follow this procedure when introducing any new library to your C/C++ or OpenCL tests.

### Phase 1 — Install Dependencies on the Host

You must install both the runtime library (for bundling) and the development headers (for compiling) on your host.

| Host OS | Install Runtime Libs | Install Devel Headers | Search Package Ownership |
|---|---|---|---|
| **Fedora** | `sudo dnf install -y <pkg>` | `sudo dnf install -y <pkg>-devel` | `dnf provides "*/libfoo.so*"` |
| **Ubuntu** | `sudo apt install -y <pkg>` | `sudo apt install -y <pkg>-dev` | `apt-file search "libfoo.so"` |

*Example (OpenBLAS):*
* **Fedora:** `sudo dnf install -y openblas-serial openblas-devel`
* **Ubuntu:** `sudo apt install -y libopenblas-serial-dev`

---

### Phase 2 — Verify Library Path & Version

Find the exact filenames and paths of the installed `.so` files.

* **Fedora:**
  ```bash
  rpm -ql openblas-serial | grep "\.so"
  find /usr/lib64 -name "libopenblas*"
  ```
* **Ubuntu:**
  ```bash
  dpkg -L libopenblas0 | grep "\.so"
  find /usr/lib/x86_64-linux-gnu -name "libopenblas*"
  ```

---

### Phase 3 — Compile the Binary with RUNPATH

When compiling your test binary, you must include the linker options to bake `$ORIGIN/lib` into the RUNPATH header.

```bash
gcc test.c -o my_test \
    -I/usr/include/openblas \
    -lopenblas \
    -Wl,-rpath,'$ORIGIN/lib'
```

Verify that the RUNPATH header was correctly embedded:
```bash
readelf -d my_test | grep -iE "needed|rpath|runpath"
# Expected output should contain: (RUNPATH) Library runpath: [$ORIGIN/lib]
```

---

### Phase 4 — Generate the Local Bundle

Run the DVF bundle script to extract all host-resolvable dependencies:

```bash
bash scripts/bundle_libs.sh path/to/my_test
```

This generates a `lib/` folder adjacent to `my_test` containing libraries like `libc.so.6`, `libstdc++.so.6`, and the target library (e.g. `libopenblas.so.0`).

> [!IMPORTANT]
> Since Vishwa-internal libraries (`libgpurt.so`, `libvishwa.so`) are not installed on the host, the bundle script will output warnings. You must copy these internal files manually or let `deploy_share.sh` handle them.

---

### Phase 5 — Deploy to the VM Share

Copy your compiled test folder to the host's QEMU share directory:

```bash
mkdir -p ~/qemu-rootfs/share/dvf_tests/my_test/lib
cp my_test ~/qemu-rootfs/share/dvf_tests/my_test/
cp -r lib/. ~/qemu-rootfs/share/dvf_tests/my_test/lib/
```

---

### Phase 6 — Mount and Execute inside Guest VM

Boot the VM:
```bash
bash scripts/manual_test_vm.sh
```

Inside the guest VM shell, perform the following steps:

1. **Mount the 9p Share:**
   ```sh
   mount -t 9p -o trans=virtio,version=9p2000.L hostshare /mnt/share
   ```
2. **Execute via the Copied Host Linker:**
   To prevent GLIBC/dynamic linker mismatch errors, run the test binary using the host's loader, referencing both the global and test-specific library directories:
   ```sh
   /mnt/share/vishwa_tests/lib/ld-linux-x86-64.so.2 \
     --library-path /mnt/share/vishwa_tests/lib:/mnt/share/dvf_tests/my_test/lib \
     /mnt/share/dvf_tests/my_test/my_test
   ```

---

## 3. Troubleshooting & Debugging Cheat Sheet

### Common Errors & How to Solve Them

#### Error A: `error while loading shared libraries: libfoo.so: cannot open shared object file`
* **Why:** The dynamic linker cannot find the library. This occurs if `$ORIGIN` fails to resolve over the 9p mount point, or the file is missing.
* **Fix:** Manually provide the path using `LD_LIBRARY_PATH` or use the host linker invocation with `--library-path`.

#### Error B: `symbol lookup error: .../libc.so.6: undefined symbol: __tunables_init`
* **Why:** You are attempting to run a binary against the host's `libc.so.6` using the guest VM's older `/lib64/ld-linux-x86-64.so.2` dynamic linker.
* **Fix:** Invoke the host dynamic linker explicitly: `/mnt/share/vishwa_tests/lib/ld-linux-x86-64.so.2 --library-path ...`

#### Error C: `9pnet_virtio: no channels available for device hostshare`
* **Why:** The hostshare device is already mounted or the mount tag was occupied.
* **Fix:** Check if it is already mounted via `mount` or `df -h`. If so, skip the mount command.

---

## 4. Host Verification & Diagnostic Commands

Use these commands to inspect binaries, libraries, and package versions on your host.

| Goal | Fedora Host Command | Ubuntu Host Command |
|---|---|---|
| **Identify package owning `.so`** | `rpm -qf /path/to/lib.so` | `dpkg -S /path/to/lib.so` |
| **List package contents** | `rpm -ql <pkg>` | `dpkg -L <pkg>` |
| **Verify binary dependencies** | `ldd <binary>` | `ldd <binary>` |
| **Show embedded RUNPATH** | `readelf -d <binary> \| grep RUNPATH` | `readelf -d <binary> \| grep RUNPATH` |
| **Check package installation** | `rpm -q <pkg>` | `dpkg -l \| grep <pkg>` |
| **Clean PyCache / root directories** | `find <dir> -maxdepth 5 -name '__pycache__' -user "$USER" -exec rm -rf {} +` | `find <dir> -maxdepth 5 -name '__pycache__' -user "$USER" -exec rm -rf {} +` |

---

## 4.5. CI/CD Pipeline-Only Users (Git Push Flow)

For validation engineers who do not compile code locally or interact with VMs, their sole interface is `git push`. If a test suite fails after pushing, follow this triage flow:

### 1. Verification of the Pushed Files
* **Missing `.so` Files in Git:** Often, `.gitignore` files are pre-configured to ignore `.so`, `.a`, or `.o` files. If you run `bundle_libs.sh` locally but commit without verifying, the `lib/` directory will not be uploaded.
  * **Fix:** Explicitly force-add the bundled libraries:
    ```bash
    git add -f c-test-binaries/my_category/lib/
    ```
* **Large File Push Blocked:** Committing large libraries (like the 42MB `libopenblas.so.0`) can cause the push to fail or hang on standard Git connections.
  * **Fix:** Ensure Git LFS (Large File Storage) is enabled for the repository, or track shared objects via LFS:
    ```bash
    git lfs track "*.so*"
    ```

### 2. Architecture & Toolchain Mismatch
* **Exec Format Error:** If you compile the binary on your host machine (e.g. an ARM64 Apple Silicon MacBook or an WSL2 environment) and commit it, the guest x86-64 QEMU VM will fail with `Exec format error`.
  * **Fix:** Only commit pre-compiled binaries compiled on an x86-64 Linux environment (Ubuntu/Fedora matching DVF specifications), or let the GitLab CI runner compile it automatically using the DVF pipeline Makefile.

### 3. Triage via GitLab CI Job Logs
When a pipeline fails, open the failed `test` stage job log in GitLab:
1. Locate the **Orchestrator Execution Block**.
2. Look for the workflow summary: `workflow completed with test failures`.
3. Read the stderr output captured by the agent. If you see:
   `error while loading shared libraries: libfoo.so.X: cannot open shared object file`
   This indicates that either:
   * The `lib/` folder was not successfully pushed to Git (Check Step 1 above).
   * The binary was compiled without the `-Wl,-rpath,'$ORIGIN/lib'` compiler flag (meaning the VM couldn't locate the adjacent folder).
---

## 5. Practical Pitfalls & Minor Gotchas (Lessons Learned)


During real-world setup and validation, several minor and transient issues frequently occur. Refer to this list to quickly resolve them:

### A. Permission Denied Errors During Deploy (`__pycache__` Blockers)
* **Problem:** Running the python-agent inside the VM writes bytecode (`.pyc` files and `__pycache__` folders) as the root user. When you return to the host and run the deploy script or attempt to clean/remove the directory, you receive `Permission denied` errors because your host user cannot delete root-owned folders.
* **Fix:** Do not use `rm -rf` directly on the entire parent directory. In scripts (e.g., `deploy_share.sh`), clean up by filtering for files owned by the current host user, or copy new code without overwriting root-owned subdirectories:
  ```bash
  find "${SHARE_DIR}/python-agent" -maxdepth 5 \
    \( -name '*.pyc' -o -name '__pycache__' \) \
    -user "$USER" -exec rm -rf {} + 2>/dev/null || true
  ```

### B. Installing a "Meta-Package" instead of the Library Package
* **Problem:** Installing a package name (e.g., `openblas` on Fedora) installs only documentation, licenses, or meta-configs, but no actual shared object (`.so`) libraries. As a result, compiling or running `ldd` continues to fail.
* **Fix:** Use query commands to see what packages are available and what files they contain. On Fedora, install the specific flavor (e.g., `openblas-serial`). On Ubuntu, install the concrete runtime package (e.g., `libopenblas0`).
  * Check package files on host: `rpm -ql <package>` or `dpkg -L <package>`.

### C. Compilation Error: `fatal error: foo.h: No such file or directory`
* **Problem:** Attempting to compile a C test binary fails during the preprocessing phase because header files are missing, even though you just installed the library.
* **Fix:** The base package only contains the compiled `.so` files needed at runtime. You must install the corresponding development package containing headers:
  * **Fedora:** `sudo dnf install <library-name>-devel` (e.g., `openblas-devel`)
  * **Ubuntu:** `sudo apt install <library-name>-dev` (e.g., `libopenblas-dev`)

### D. Shell Typo & Serial Copy-Paste Concat Blocks
* **Problem:** Executing multi-line commands using backslash escapes (`\`) inside the guest QEMU interactive serial console (`scripts/manual_test_vm.sh`) fails with `/bin/sh: : not found`. 
* **Fix:** Interactive raw serial shells do not parse multi-line inputs reliably. Always format and paste commands onto **a single line** when running them directly inside the QEMU shell:
  * *Incorrect:* 
    ```sh
    LD_LIBRARY_PATH=/lib \
      ./my_binary
    ```
  * *Correct:*
    ```sh
    LD_LIBRARY_PATH=/lib ./my_binary
    ```
  * Also watch out for typos in basic commands (like typing `rmp` instead of `rpm`).

### E. Forgotten VirtIO-9p Mounts
* **Problem:** Booting the VM with `scripts/manual_test_vm.sh` drops you directly into `/bin/sh` as root, but searching `/mnt/share` returns `No such file or directory` or is empty.
* **Fix:** The manual test VM is intentionally kept minimal; it does not auto-mount shares on boot. You must run the mount command manually every time you boot:
  ```sh
  mount -t 9p -o trans=virtio,version=9p2000.L hostshare /mnt/share
  ```

### F. Broken Symlinks During Copy (`cp` vs. `cp -L`)
* **Problem:** You copy libraries to the share or the `lib/` directory using simple `cp`, but the binary fails to run inside the guest because it cannot find the library or resolves a broken symbolic link.
* **Fix:** Always copy files using the `-L` (dereference/follow symlinks) flag or use `rsync -a` to copy the actual files instead of copying unresolved links.
  ```bash
  cp -L /usr/lib64/libfoo.so.1 /path/to/share/lib/
  ```

### G. `$ORIGIN` Path Resolution Failure over 9p Filesystems
* **Problem:** The binary compiles fine with `-Wl,-rpath,'$ORIGIN/lib'` and runs locally, but running `/mnt/share/test/binary` inside the VM throws `cannot open shared object file: No such file or directory` even though `lib/` is present.
* **Fix:** The guest kernel's dynamic linker resolves `$ORIGIN` relative to `/proc/self/exe`. On some 9p filesystem implementations, this resolution fails. The DVF python-agent solves this at runtime by explicitly setting the absolute guest directory using `LD_LIBRARY_PATH` (or explicitly passing it to the host linker).

---

## 6. Exhaustive Minor Gotchas Catalog

This section captures every small, tricky, or easy-to-miss issue encountered during real integration and testing — including issues with zero tolerance for confusion.

---

### H. `find` Reporting "No Files Found" When Files Actually Exist

* **Problem:** Running `find /usr/lib64 /usr/lib -name "libfoo*"` prints matching results from `/usr/lib64` but ALSO echoes a fallback message like `"no .so files found"` at the end. This looks contradictory.
* **Why:** When you run `find` with multiple paths and ONE of them does not exist (e.g., `/usr/lib` is absent on Fedora — it is a symlink that resolves differently), `find` returns exit code `1`. The `||` operator then triggers your fallback echo even though real results were printed.
* **Fix:** Search only the paths that actually exist, or check the output visually rather than trusting the exit code:
  ```bash
  # Safer approach — search both but suppress directory-not-found errors
  find /usr/lib64 /usr/lib -name "libfoo*" 2>/dev/null
  # Or check each path explicitly
  ls /usr/lib64/libfoo* 2>/dev/null || echo "NOT in lib64"
  ls /usr/lib/libfoo* 2>/dev/null || echo "NOT in lib"
  ```

---

### I. `rpm -q` Command Typo Returns "NOT INSTALLED" for a Library That IS Installed

* **Problem:** Typing `rmp -q libjpeg-turbo` (a typo of `rpm`) reports `NOT INSTALLED`, causing you to conclude the library isn't there. But `find` shows the `.so` files clearly exist.
* **Why:** `rmp` is not a valid command. The shell returns exit code `127` (command not found), which satisfies the `|| echo "NOT INSTALLED"` fallback.
* **Fix:** Double-check your command spelling before drawing conclusions. Always verify with two independent methods:
  ```bash
  rpm -q libjpeg-turbo                            # correct spelling
  rpm -qf /usr/lib64/libjpeg.so.62               # find package owning a file
  find /usr/lib64 -name "libjpeg*"               # find the .so directly
  ```

---

### J. `rpm -ql` Returns No `.so` Files Because the Wrong Sub-Package Was Installed

* **Problem:** You install `openblas` and run `rpm -ql openblas | grep ".so"` — no output. But the library IS in the Fedora repos.
* **Why:** On Fedora, many libraries are split into multiple sub-packages:
  - `openblas` → documentation and license files ONLY (no `.so`)
  - `openblas-serial` → the actual `libopenblas.so` runtime files
  - `openblas-devel` → header `.h` files for compiling
* **Checklist:** Always verify the installed package actually contains runtime files:
  ```bash
  # After installing, do this BEFORE trying to use it
  rpm -ql <package-name> | grep -E "\.so|\.h"
  ```
  If the grep returns nothing, you likely installed a meta-package. Search for the concrete package:
  ```bash
  dnf list available 'openblas*'     # Fedora
  apt-cache search libopenblas       # Ubuntu
  ```

---

### K. `openblas-devel` Pulling in 300MB of Unexpected Sub-Packages

* **Problem:** Running `sudo dnf install -y openblas-devel` installs 9 packages totalling 348MB when you only needed headers to compile. The dependencies include `openblas-openmp`, `openblas-threads`, `openblas-serial64`, etc.
* **Why:** The `-devel` package on Fedora declares all OpenBLAS ABI variants as dependencies so that the `pkg-config` linker flags work across all flavors. This is a packaging design decision, not a bug.
* **Fix:** This is expected behavior — just wait for the install. For CI/CD pipelines, cache the dnf package cache between runs to avoid repeated large downloads. For Ubuntu, `libopenblas-dev` pulls significantly fewer packages.

---

### L. `readelf -d` RUNPATH Grep Returning Nothing (Case Sensitivity)

* **Problem:** Running `readelf -d my_binary | grep "rpath"` returns empty output, making you think the RUNPATH was not embedded — even though the compilation command had `-Wl,-rpath,'$ORIGIN/lib'`.
* **Why:** Modern GNU `ld` (linkers version ≥ 2.26) use the `RUNPATH` ELF tag instead of the legacy `RPATH` tag. These are different tag names. A lowercase grep for `"rpath"` misses `RUNPATH`.
* **Fix:** Always use case-insensitive search or match both tags:
  ```bash
  readelf -d my_binary | grep -iE "needed|rpath|runpath"
  # Or equivalently:
  objdump -p my_binary | grep -i path
  ```

---

### M. Terminal Commands Getting Concatenated on the Host (Paste-Mode Issue)

* **Problem:** Pasting multi-line commands like:
  ```
  mkdir -p ~/share/blas/lib
  cp blas_test ~/share/blas/
  ```
  into a terminal results in them being executed as a single command: `mkdir -p ~/share/blas/libcp blas_test ...`, which creates folders with wrong names and skips the actual copies silently.
* **Why:** Some terminal emulators (especially when running over SSH or tmux) do not properly handle newlines in pasted text and join lines. The first command's end merges with the second command's beginning.
* **Fix:** Run each command separately (press Enter after each one). Alternatively, place all commands into a temporary script and run that instead:
  ```bash
  # Write to a script
  cat > /tmp/setup.sh << 'EOF'
  mkdir -p ~/share/blas/lib
  cp blas_test ~/share/blas/
  cp -r lib/. ~/share/blas/lib/
  EOF
  bash /tmp/setup.sh
  ```

---

### N. Copying Files to the Share While VM Is Already Booted — Changes Not Visible

* **Problem:** You copied a binary to `~/qemu-rootfs/share/dvf_tests/blas/` while the VM was already running. Inside the VM, the `ls /mnt/share/dvf_tests/blas/` still shows "No such file or directory" or old contents.
* **Why:** This is NOT a caching issue — the 9p virtio filesystem IS a live passthrough. The actual cause in our case was that the copy commands **failed silently** due to terminal concatenation (Issue M above), so the files were never actually copied. The directory appeared empty because it was empty.
* **Fix:** Always verify from the host that files were successfully copied BEFORE booting the VM:
  ```bash
  ls -lh ~/qemu-rootfs/share/dvf_tests/blas/
  ls -lh ~/qemu-rootfs/share/dvf_tests/blas/lib/
  ```
  Only boot the VM once you can confirm the files exist with the correct sizes.

---

### O. Running VM Commands on the Host by Accident

* **Problem:** After exiting the VM with `Ctrl+A` then `X`, you run a command intended for the VM guest (e.g., `ls /mnt/share/`) on the host terminal instead. The error `ls: cannot access '/mnt/share/': No such file or directory` confuses you because the share was mounted in the guest, not the host.
* **Why:** `/mnt/share` only exists inside the QEMU guest's filesystem. The host has no `/mnt/share` unless you manually created and mounted something there.
* **Fix:** Check your terminal prompt before running commands:
  - **Host prompt:** `kartheekbudime@fedora:~$`
  - **Guest prompt:** `#` (root with no hostname, running `/bin/sh`)

---

### P. `LD_LIBRARY_PATH` Pointing to Bundled Libs Breaks Basic System Commands

* **Problem:** After setting `export LD_LIBRARY_PATH=/mnt/share/vishwa_tests/lib`, running something as simple as `cat /tmp/stdout.txt` inside the VM fails with a library error, a segfault, or just hangs.
* **Why:** The bundled `libc.so.6` (from your Fedora host, e.g., glibc 2.41) is newer than the guest VM's glibc version. When you set `LD_LIBRARY_PATH`, ALL commands including `cat`, `ls`, `echo` will try to use the bundled (newer) `libc.so.6` via the guest's (older) dynamic linker — causing symbol mismatches (`undefined symbol: __tunables_init`).
* **Fix:** After running the test binary, immediately `unset LD_LIBRARY_PATH` before running any other commands:
  ```sh
  /mnt/share/vishwa_tests/lib/ld-linux-x86-64.so.2 ... ./my_test 1>/tmp/out.txt 2>/tmp/err.txt
  echo "Exit: $?"
  unset LD_LIBRARY_PATH    # ← DO THIS BEFORE cat, ls, etc.
  cat /tmp/out.txt
  cat /tmp/err.txt
  ```

---

### Q. `9pnet_virtio: no channels available` on Second Mount Attempt

* **Problem:** Running `mount -t 9p ... hostshare /mnt/share` a second time gives `9pnet_virtio: no channels available for device hostshare` and `mount point busy`.
* **Why:** The share was already successfully mounted on the first attempt. The 9p virtio channel is single-use — once connected, there are no remaining channels to service a second mount.
* **Fix:** This error means the mount **already worked**. Just run `ls /mnt/share/` and continue. You can also verify with:
  ```sh
  mount | grep hostshare
  df -h /mnt/share
  ```

---

### R. Driver Probe Fails Silently — `/dev/gp_gpu` Not Created After `insmod`

* **Problem:** `insmod /mnt/share/gpgpu_pcie_ep_driver.ko` succeeds (exit code 0) but `/dev/gp_gpu` is not created. `dmesg` shows the module loaded but nothing about device creation.
* **Why:** The real CDAC GPGPU driver uses `pci_enable_msix_exact()` which requires the **Q35** PCIe machine chipset. If QEMU was started with the default `i440fx` machine type (`-machine pc` or no `-machine` flag), MSI-X setup fails silently during the PCI probe, and the driver's `probe()` function returns an error without creating the device node.
* **Fix:** Always launch the manual test VM with `-machine q35`:
  ```bash
  # In manual_test_vm.sh — this line is mandatory:
  -machine q35 \
  ```
  Verify by checking `dmesg | grep -i "msix\|gp_gpu\|probe"` after loading the driver.

---

### S. Orchestrator Reports `TEST_RUN_STATUS_FAILED` Incorrectly Due to Agent Using Old Code

* **Problem:** You fix the python-agent (e.g., add status reconciliation logic), but the orchestrator still reports the wrong status. The `test_runs` DB table shows the old behavior.
* **Why:** The QEMU VM's python-agent code is served from `~/qemu-rootfs/share/python-agent/`. This is a separate copy from your source `python-agent/` directory. Editing the source does NOT automatically update what the VM reads.
* **Fix:** After editing agent source, always redeploy it to the share:
  ```bash
  # Quick single-file deploy:
  cp python-agent/agent/__main__.py ~/qemu-rootfs/share/python-agent/agent/__main__.py
  # Full redeploy (handles __pycache__ conflicts):
  bash scripts/deploy_share.sh --skip-vishwa-build --skip-driver-build
  ```

---

### T. OpenTelemetry Trace Export Error Flooding the Orchestrator Logs

* **Problem:** The orchestrator continuously prints errors like:
  ```
  traces export: exporter export timeout: rpc error: code = Unavailable
  desc = connection error: ... dial tcp [::1]:4317: connect: connection refused
  ```
* **Why:** The `global_config.json` has `"trace_endpoint": "localhost:4317"` pointing to a Jaeger/Tempo collector that is not running.
* **Fix:** Change the endpoint to `"none"` in both config files to disable tracing entirely in local environments:
  ```json
  "telemetry": {
    "trace_endpoint": "none"
  }
  ```
  The `InitTracer()` function checks for `"none"` and returns a no-op provider instead.

---

### U. `sudo` Facial Authentication Failure Blocking Package Installs

* **Problem:** Running `sudo dnf install` or any `sudo` command fails with:
  ```
  Attempting facial authentication
  ModuleNotFoundError: No module named 'dlib'
  ```
  Then falls through to a password prompt, but the password isn't accepted in a non-interactive context.
* **Why:** The system has `howdy` (facial recognition for sudo) installed, but its `dlib` Python module is broken/not installed. It errors out and falls back to password, which fails in piped/non-interactive contexts.
* **Fix:** This is a system configuration issue. Run `sudo` commands directly in an interactive terminal where you can type your password. Do not pipe sudo commands through scripts in this environment. Alternatively, fix the howdy installation:
  ```bash
  sudo dnf install python3-dlib   # or
  sudo pam-auth-update --remove howdy
  ```

---

### V. Real Driver Source vs. Placeholder — Wrong `.ko` in Share

* **Problem:** The GPGPU driver loads and `/dev/gp_gpu` is created, but `libgpurt.so` still fails with `device open failed`. Alternatively, `/dev/gp_gpu` is not created at all despite `insmod` succeeding.
* **Why:** There are TWO different driver versions:
  - **Placeholder** (`driver-source/gpgpu_driver/gpgpu_driver.c`): A simple PCI driver stub with no `ioctl` handler. Creates `/dev/gpgpu`.
  - **Real CDAC driver** (`gpgpu_pcie_ep_driver.ko`): Full driver with IOCTL handlers, DMA support, MSI-X interrupts. Creates `/dev/gp_gpu`.
* **Fix:** Verify which `.ko` is in the share:
  ```bash
  # Real driver is ~35KB, placeholder is ~13KB
  ls -lh ~/qemu-rootfs/share/gpgpu_pcie_ep_driver.ko
  # Check the device name the driver creates
  strings ~/qemu-rootfs/share/gpgpu_pcie_ep_driver.ko | grep "gp_gpu\|gpgpu"
  ```
  Ensure `device_registry.json` also uses the matching device node name (`/dev/gp_gpu` for the real driver).

---

### W. `deploy_share.sh` Driver Build Fails with Kernel Module Errors

* **Problem:** Running `bash scripts/deploy_share.sh` without `--skip-driver-build` fails:
  ```
  make[1]: *** [Makefile:248: __sub-make] Error 2
  ```
* **Why:** Building the kernel module requires the host kernel headers and the exact kernel version used to compile the kernel tree. If the kernel tree (`$HOME/VirtualMachines/linux`) doesn't match the current host kernel, or if it hasn't been configured+built, `make -C` will fail.
* **Fix:** Use `--skip-driver-build` when the driver `.ko` is already available pre-built. Only do a full driver build when the driver source has changed AND your kernel build tree is correctly configured:
  ```bash
  # Skip driver build (use pre-built .ko from driver-source/)
  bash scripts/deploy_share.sh --skip-driver-build --skip-vishwa-build

  # Full build (requires configured kernel tree at ~/VirtualMachines/linux)
  bash scripts/deploy_share.sh
  ```

