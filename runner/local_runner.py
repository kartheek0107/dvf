#!/usr/bin/env python3
"""
DVF Local Runner — Orchestrates a full driver validation test run.

Supports three target modes:
  - qemu   : Boot QEMU with a custom emulated device (default)
  - fpga   : Run tests natively on the host against a physical FPGA
  - hybrid : Boot QEMU with the FPGA passed through via VFIO-PCI

Usage:
  python3 runner/local_runner.py                  # run all suites (qemu mode)
  python3 runner/local_runner.py --target fpga    # run against FPGA hardware
  python3 runner/local_runner.py --suite smoke     # run only smoke tests
  python3 runner/local_runner.py --build-only      # just build, don't run
  python3 runner/local_runner.py --skip-build      # skip build step
"""

import argparse
import json
import os
import signal
import subprocess
import sys
import time
import yaml
from datetime import datetime
from pathlib import Path

# ---------------------------------------------------------------------------
# Paths
# ---------------------------------------------------------------------------

SCRIPT_DIR = Path(__file__).resolve().parent
PROJECT_DIR = SCRIPT_DIR.parent
CONFIG_PATH = SCRIPT_DIR / "config.yaml"


def load_config(path: Path) -> dict:
    with open(path) as f:
        return yaml.safe_load(f)


# ---------------------------------------------------------------------------
# ANSI Colors
# ---------------------------------------------------------------------------

class C:
    RESET  = "\033[0m"
    BOLD   = "\033[1m"
    GREEN  = "\033[32m"
    RED    = "\033[31m"
    YELLOW = "\033[33m"
    CYAN   = "\033[36m"
    DIM    = "\033[2m"


def info(msg):
    print(f"{C.CYAN}[DVF]{C.RESET} {msg}")

def success(msg):
    print(f"{C.GREEN}  ✓{C.RESET} {msg}")

def fail(msg):
    print(f"{C.RED}  ✗{C.RESET} {msg}")

def warn(msg):
    print(f"{C.YELLOW}[WARN]{C.RESET} {msg}")

def header(msg):
    print(f"\n{C.BOLD}{C.CYAN}{'=' * 60}{C.RESET}")
    print(f"{C.BOLD}{C.CYAN}  {msg}{C.RESET}")
    print(f"{C.BOLD}{C.CYAN}{'=' * 60}{C.RESET}")


# ---------------------------------------------------------------------------
# Step 1: Build test binaries
# ---------------------------------------------------------------------------

def build_tests(cfg: dict) -> bool:
    info("Building C test binaries...")
    test_bin_dir = PROJECT_DIR / "c-test-binaries"

    result = subprocess.run(
        ["make", "-j4"],
        cwd=str(test_bin_dir),
        capture_output=True,
        text=True,
    )

    if result.returncode != 0:
        fail(f"Build failed:\n{result.stderr}")
        return False

    success("Test binaries built successfully")

    # Install to share directory
    share_dir = cfg["qemu"]["share_dir"]
    info(f"Installing binaries to {share_dir}/dvf_tests/")

    result = subprocess.run(
        ["make", "install", f"SHARE_DIR={share_dir}"],
        cwd=str(test_bin_dir),
        capture_output=True,
        text=True,
    )

    if result.returncode != 0:
        fail(f"Install failed:\n{result.stderr}")
        return False

    success("Binaries installed to 9p share")
    return True


# ---------------------------------------------------------------------------
# Step 2: Build QEMU command line
# ---------------------------------------------------------------------------

def get_device_env(cfg: dict) -> dict:
    """Build DVF_* environment variables for test binaries based on target mode."""
    target = cfg.get("target_mode", "qemu")
    env = {}

    if target in ("fpga", "hybrid"):
        fpga = cfg.get("fpga", {})
        fpga_drv = cfg.get("fpga_driver", cfg["driver"])
        env["DVF_DEVICE_PATH"] = fpga_drv.get("device_node", fpga.get("device_node", "/dev/fpga0"))
        env["DVF_REG_COUNT"] = str(fpga.get("reg_count", 256))
        env["DVF_REG_SIZE"] = str(fpga.get("reg_size", 4))
        env["DVF_BAR_SIZE"] = str(fpga.get("bar_size", 1024))
    else:
        env["DVF_DEVICE_PATH"] = cfg["driver"]["device_node"]
        env["DVF_REG_COUNT"] = "256"
        env["DVF_REG_SIZE"] = "4"
        env["DVF_BAR_SIZE"] = "1024"

    return env


def build_qemu_cmd(cfg: dict) -> list:
    q = cfg["qemu"]
    share_dir = q["share_dir"]
    target = cfg.get("target_mode", "qemu")

    cmd = [
        q["binary"],
        "-kernel", q["kernel"],
        "-drive", f"file={q['rootfs']},format=raw,if=virtio",
        # Use init=/bin/bash to drop directly into a root shell (no login needed)
        "-append", "root=/dev/vda console=ttyS0 rw init=/bin/bash",
        "-m", str(q["memory_mb"]),
        "-smp", str(q["cpus"]),
        # -nographic already redirects serial to stdio; do NOT add -serial mon:stdio
        "-nographic",
        # 9p virtio share
        "-virtfs", f"local,path={share_dir},mount_tag=hostshare,security_model=mapped,id=hostshare",
    ]

    # Device attachment depends on target mode
    if target == "hybrid":
        fpga = cfg.get("fpga", {})
        pci_addr = fpga.get("pci_address", "")
        if not pci_addr:
            fail("hybrid mode requires fpga.pci_address in config")
            sys.exit(1)
        cmd.extend(["-device", f"vfio-pci,host={pci_addr},id=fpga0"])
    else:
        cmd.extend(["-device", q["device_name"]])

    if q.get("extra_flags"):
        cmd.extend(q["extra_flags"].split())

    return cmd


# ---------------------------------------------------------------------------
# Step 2b: Pre-flight — kill any stale DVF QEMU holding the rootfs lock
# ---------------------------------------------------------------------------

def kill_stale_qemu(rootfs_path: str):
    """
    Kill any lingering qemu-system-x86_64 process that is using our rootfs.
    This prevents the 'Failed to get write lock' error on successive runs.
    """
    import subprocess
    try:
        result = subprocess.run(
            ["pgrep", "-a", "-f", "qemu-system-x86_64"],
            capture_output=True, text=True
        )
        for line in result.stdout.strip().splitlines():
            parts = line.split(None, 1)
            if len(parts) < 2:
                continue
            pid_str, cmdline = parts[0], parts[1]
            # Only kill DVF-launched instances (those using our specific rootfs)
            if rootfs_path in cmdline and "-nographic" in cmdline:
                try:
                    pid = int(pid_str)
                    warn(f"Killing stale DVF QEMU process (PID {pid}) holding rootfs lock...")
                    import os
                    os.kill(pid, signal.SIGKILL)
                    time.sleep(0.5)
                    success(f"Stale QEMU (PID {pid}) killed")
                except (ValueError, ProcessLookupError, PermissionError) as e:
                    warn(f"Could not kill PID {pid_str}: {e}")
    except FileNotFoundError:
        pass  # pgrep not available — skip


# ---------------------------------------------------------------------------
# Step 3: Launch QEMU & interact via serial
# ---------------------------------------------------------------------------

class QEMUInstance:
    """Manages a QEMU process and serial console interaction."""

    def __init__(self, cmd: list, boot_timeout: int = 120):
        self.cmd = cmd
        self.boot_timeout = boot_timeout
        self.proc = None
        self.boot_log = []

    def start(self) -> bool:
        info("Launching QEMU...")
        info(f"  CMD: {' '.join(self.cmd[:6])}...")

        self.proc = subprocess.Popen(
            self.cmd,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            bufsize=0,
        )

        info(f"  PID: {self.proc.pid}")
        boot_state = self._wait_for_boot()
        if boot_state == "error":
            return False
        self.boot_state = boot_state
        return True

    def _wait_for_boot(self) -> str:
        """Wait for the guest to boot — detects bash-as-init or login prompt.

        Also checks stderr early so that QEMU startup errors (like a disk
        write-lock failure) are reported immediately instead of timing out.

        With init=/bin/bash, the guest prints:
          bash: cannot set terminal process group (-1): ...
          bash: no job control in this shell
          root@(none):/#

        We detect any of these as a successful boot.
        """
        info(f"Waiting for guest boot (timeout: {self.boot_timeout}s)...")
        start = time.time()

        import select
        last_nl = 0
        # Don't send newlines too early — wait for first sign of life
        first_output_seen = False

        # Check stderr immediately — QEMU startup errors (e.g. disk lock)
        # appear on stderr before any stdout output.
        time.sleep(0.3)
        ready_err, _, _ = select.select([self.proc.stderr], [], [], 0.5)
        if ready_err:
            stderr_peek = self.proc.stderr.read1(4096).decode("utf-8", errors="replace").strip()
            if stderr_peek:
                fail(f"QEMU startup error: {stderr_peek}")
                # Let the process exit naturally then return error
                self.proc.wait(timeout=3)
                return "error"

        while time.time() - start < self.boot_timeout:
            now = time.time()
            # After we've seen some output, periodically nudge with Enter
            if first_output_seen and now - last_nl > 3:
                try:
                    self.proc.stdin.write(b"\n")
                    self.proc.stdin.flush()
                except BrokenPipeError:
                    pass
                last_nl = now

            ready, _, _ = select.select([self.proc.stdout], [], [], 0.5)
            if ready:
                try:
                    line = self.proc.stdout.readline()
                    if not line:
                        continue
                    decoded = line.decode("utf-8", errors="replace").strip()
                    self.boot_log.append(decoded)
                    first_output_seen = True

                    lower_line = decoded.lower()

                    # Detect init=/bin/bash boot completion:
                    # "Run /bin/bash as init process" from kernel
                    if "run /bin/bash as init process" in lower_line:
                        info("  Kernel starting bash as init...")
                        continue

                    # "bash: cannot set terminal process group" — bash started
                    if "cannot set terminal process group" in lower_line:
                        info("  bash-as-init started (expected warning)")
                        continue

                    # "bash: no job control in this shell" — bash is ready
                    if "no job control in this shell" in lower_line:
                        success(f"Guest booted in {time.time() - start:.1f}s (bash-as-init)")
                        # Give bash a moment to print its prompt, then send Enter
                        time.sleep(0.5)
                        try:
                            self.proc.stdin.write(b"\n")
                            self.proc.stdin.flush()
                        except BrokenPipeError:
                            pass
                        return "shell"

                    # Fallback: detect a login prompt (if init= is not /bin/bash)
                    if "login:" in lower_line:
                        success(f"Guest booted in {time.time() - start:.1f}s (login prompt)")
                        return "login"

                    # Fallback: detect a root shell prompt
                    if any(m in decoded for m in [
                        "root@", ":/#", ":/# ",
                        "# ", "$ ",
                    ]):
                        # Make sure it's not a kernel log line
                        if not decoded.startswith("["):
                            success(f"Guest booted in {time.time() - start:.1f}s (shell prompt)")
                            return "shell"

                except Exception:
                    continue

            # Check if process died
            if self.proc.poll() is not None:
                stderr_out = self.proc.stderr.read().decode("utf-8", errors="replace").strip()
                fail(f"QEMU exited prematurely (code: {self.proc.returncode})")
                if stderr_out:
                    fail(f"QEMU Error: {stderr_out}")
                return "error"

        fail(f"Boot timeout ({self.boot_timeout}s) — no prompt detected")
        # Dump last few boot log lines for debugging
        if self.boot_log:
            warn("Last 10 lines of boot log:")
            for line in self.boot_log[-10:]:
                print(f"    {line}")
        return "error"

    def run_command(self, cmd: str, timeout: int = 30) -> tuple:
        """
        Send a command via serial console, capture output.
        Returns (stdout_text, return_code).

        Protocol: We wrap the command with echo markers and disable
        bash echo to avoid parsing our own command back from serial.
        """
        import select

        # Unique markers to delimit our command's output
        marker = f"DVF{int(time.time() * 1000) % 100000}"
        # Wrap: disable echo, run command, capture $?, print marker, re-enable echo
        # Using printf ensures the marker is on its own line with no ambiguity
        full_cmd = (
            f"stty -echo 2>/dev/null; "
            f"{cmd} 2>&1; "
            f"printf '\\n{marker}RC%d\\n' $?; "
            f"stty echo 2>/dev/null\n"
        )

        self.proc.stdin.write(full_cmd.encode())
        self.proc.stdin.flush()

        output_lines = []
        start = time.time()
        rc = -1

        while time.time() - start < timeout:
            ready, _, _ = select.select([self.proc.stdout], [], [], 0.5)
            if ready:
                try:
                    line = self.proc.stdout.readline()
                    if not line:
                        continue
                    decoded = line.decode("utf-8", errors="replace").strip()

                    if not decoded:
                        continue

                    # Check for our return code marker
                    if decoded.startswith(f"{marker}RC"):
                        try:
                            rc = int(decoded[len(f"{marker}RC"):])
                        except (ValueError, IndexError):
                            rc = -1
                        break

                    # Skip kernel dmesg lines (e.g., [  123.456] ...)
                    if decoded.startswith("[") and "]" in decoded[:20]:
                        continue

                    # Skip echoed command fragments
                    if "stty" in decoded or marker in decoded:
                        continue

                    output_lines.append(decoded)
                except Exception:
                    continue

        return "\n".join(output_lines), rc

    def send_login(self, username: str = "root", password: str = ""):
        """Attempt to log in via serial console."""
        time.sleep(0.5)
        self.proc.stdin.write(f"{username}\n".encode())
        self.proc.stdin.flush()
        time.sleep(1.0)
        if password:
            self.proc.stdin.write(f"{password}\n".encode())
            self.proc.stdin.flush()
            time.sleep(0.5)

    def shutdown(self):
        """Shut down the VM.

        With init=/bin/bash there is no init system to handle 'poweroff',
        so we send SIGTERM to QEMU which triggers a clean shutdown.
        """
        if self.proc and self.proc.poll() is None:
            info("Shutting down QEMU...")
            try:
                # Try sending 'quit' via stdin (works if QEMU monitor is on stdio)
                # With -nographic, Ctrl-a x also works, but SIGTERM is most reliable
                self.proc.terminate()  # SIGTERM → QEMU does clean shutdown
                self.proc.wait(timeout=10)
                success("QEMU shut down gracefully")
            except subprocess.TimeoutExpired:
                warn("Force-killing QEMU")
                self.proc.kill()
                self.proc.wait()


# ---------------------------------------------------------------------------
# Step 4: Run tests
# ---------------------------------------------------------------------------

def run_tests(vm: QEMUInstance, cfg: dict) -> list:
    """Run all test binaries and collect results."""
    driver_cfg = cfg["driver"]
    test_cfg = cfg["tests"]
    guest_mount = cfg["qemu"]["guest_mount"]
    test_dir = f"{guest_mount}/{test_cfg['test_dir']}"
    results = []

    # Login if needed
    if getattr(vm, "boot_state", "") == "login":
        info("Logging in as root...")
        vm.send_login("root")
    else:
        info("Direct shell detected (init=/bin/bash), no login needed.")
    time.sleep(1.0)

    # Drain any leftover boot output before sending commands
    import select
    while True:
        ready, _, _ = select.select([vm.proc.stdout], [], [], 0.3)
        if ready:
            try:
                vm.proc.stdout.readline()
            except Exception:
                break
        else:
            break

    # Mount 9p share if not already mounted
    info(f"Mounting 9p share at {guest_mount}...")
    vm.run_command(f"mkdir -p {guest_mount}")
    mount_out, mount_rc = vm.run_command(
        f"mount -t 9p -o trans=virtio,version=9p2000.L hostshare {guest_mount}"
    )
    if mount_rc != 0:
        warn(f"9p mount returned rc={mount_rc}: {mount_out}")
        # Try alternate mount options
        mount_out2, mount_rc2 = vm.run_command(
            f"mount -t 9p -o trans=virtio hostshare {guest_mount}"
        )
        if mount_rc2 != 0:
            fail(f"9p mount failed: {mount_out2}")
            return results
    success(f"9p share mounted at {guest_mount}")

    # Verify mount worked by listing contents
    ls_out, ls_rc = vm.run_command(f"ls {guest_mount}/")
    info(f"  Share contents: {ls_out}")

    # Mount essential virtual filesystems (init=/bin/bash skips normal init)
    # These are required for PCI driver-device binding to work
    info("Mounting essential virtual filesystems...")
    vm.run_command("mount -t proc proc /proc 2>/dev/null")
    vm.run_command("mount -t sysfs sysfs /sys 2>/dev/null")
    vm.run_command("mount -t devtmpfs devtmpfs /dev 2>/dev/null")
    success("Virtual filesystems mounted (/proc, /sys, /dev)")

    # Load driver
    ko_path = f"{guest_mount}/{driver_cfg['ko_file']}"
    info(f"Loading driver: insmod {ko_path}")

    # First verify the .ko file is accessible
    file_out, file_rc = vm.run_command(f"ls -la {ko_path}")
    if file_rc != 0:
        fail(f"Driver .ko not found at {ko_path}: {file_out}")
        return results
    info(f"  Driver file: {file_out}")

    output, rc = vm.run_command(f"insmod {ko_path}")
    if rc != 0:
        # Capture dmesg for more context on why insmod failed
        dmesg_out, _ = vm.run_command("dmesg | tail -5")
        fail(f"insmod failed (rc={rc}): {output}")
        if dmesg_out:
            fail(f"  dmesg: {dmesg_out}")
        return results
    success("Driver loaded successfully")

    # Verify device node — give the PCI probe a moment to complete
    time.sleep(1.0)
    output, rc = vm.run_command(f"ls -la {driver_cfg['device_node']}")
    if rc != 0:
        # Dump diagnostic info so we can see what happened during probe
        dmesg_out, _ = vm.run_command("dmesg | tail -20")
        info(f"  dmesg after insmod:\n{dmesg_out}")
        lspci_out, _ = vm.run_command("lspci -nn 2>/dev/null || cat /proc/bus/pci/devices 2>/dev/null || echo 'no lspci'")
        info(f"  PCI devices:\n{lspci_out}")
        lsmod_out, _ = vm.run_command("lsmod")
        info(f"  Loaded modules:\n{lsmod_out}")
        fail(f"Device node {driver_cfg['device_node']} not found — PCI probe likely failed (check dmesg above)")
        return results
    success(f"Device node {driver_cfg['device_node']} present")

    # Diagnostic: run ldd-equivalent on libpocl.so.2 inside guest
    lib_dir = f"{guest_mount}/vishwa_tests/lib"
    loader = f"{lib_dir}/ld-linux-x86-64.so.2"
    ldd_out, ldd_rc = vm.run_command(f"LD_TRACE_LOADED_OBJECTS=1 {loader} --library-path {lib_dir} {lib_dir}/libpocl.so.2")
    info(f"Diagnostics: libpocl.so.2 dependencies in guest:\n{ldd_out}")

    ld_check, _ = vm.run_command("which ld || find /usr /bin /sbin -name ld -type f 2>/dev/null || echo 'ld not found'")
    info(f"Diagnostics: ld check in guest: {ld_check.strip()}")

    # Run each test suite
    for suite_path in test_cfg["suites"]:
        binary = f"{test_dir}/{suite_path}"
        suite_name = suite_path.replace("/", "::")

        info(f"\n[SUITE] {suite_name}")
        info(f"  binary: {binary}")

        # Make binary executable
        vm.run_command(f"chmod +x {binary}")

        # cd into the test's own directory before running so that relative asset
        # paths (input.jpg, input.png, bias.txt, kernel.cl, etc.) resolve correctly
        binary_dir = binary.rsplit("/", 1)[0]

        # Run the test binary with the required env variables for the Vishwa runtime
        lib_dir = f"{guest_mount}/vishwa_tests/lib"

        # ── Strategy 1: Self-Contained Bundles ────────────────────────────────
        # If the test author placed a lib/ directory next to their binary, prepend
        # it to LD_LIBRARY_PATH and the Vishwa loader path automatically.
        bundled_lib = f"{binary_dir}/lib"
        bundled_check, _ = vm.run_command(f"test -d {bundled_lib} && echo yes || echo no")
        if bundled_check.strip() == "yes":
            info(f"  Found bundled lib/ at {bundled_lib} — prepending to library path")
            lib_dir = f"{bundled_lib}:{lib_dir}"
        # ──────────────────────────────────────────────────────────────────

        icd_dir = f"{guest_mount}/vishwa_tests/lib/OpenCL/vendors"
        env_prefix = (
            "EMCONFIG_PATH=/mnt/hw_bit_file "
            "XRT_DEVICE_INDEX=0 "
            "XRT_XCLBIN_PATH=/mnt/hw_bit_file/tbs_2d.xclbin "
            f"LD_LIBRARY_PATH={lib_dir}:/tmp/toolchain/llvm-vishwa/lib:/tmp/toolchain/pocl/lib64:/tmp/toolchain/gcc11/lib64 "
            f"OCL_ICD_VENDORS={icd_dir} "
            "POCL_VISHWA_XLEN=32 "
            "POCL_CACHE_DIR=/tmp/pocl_cache "
            "POCL_DEVICES=basic "
            "PATH=/mnt/share/vishwa_tests/bin:$PATH "
            "LLVM_PREFIX=/tmp/toolchain/llvm-vishwa"
        )
        # Use the host ld-linux to bypass the guest's old glibc
        loader = f"{guest_mount}/vishwa_tests/lib/ld-linux-x86-64.so.2"
        t_start = time.time()
        output, rc = vm.run_command(
            f"cd {binary_dir} && {env_prefix} {loader} --library-path {lib_dir} {binary}",
            timeout=test_cfg["test_timeout"]
        )
        elapsed_ms = (time.time() - t_start) * 1000.0

        # Try to parse JSON from output
        suite_result = {
            "suite": suite_name,
            "binary": suite_path,
            "exit_code": rc,
            "raw_output": output,
            "results": [],
            "summary": {"total": 0, "passed": 0, "failed": 0, "duration_ms": round(elapsed_ms, 1)},
        }

        # The test framework outputs multi-line JSON to stdout, mixed with
        # human-readable stderr on serial. Strategy:
        #   1. Try to extract and reassemble the full JSON object
        #   2. Fall back to parsing individual test result lines
        json_found = False
        lines = output.split("\n")

        # Strategy 1: Find {"suite": line, collect until closing }}
        json_lines = []
        collecting = False
        for line in lines:
            stripped = line.strip()
            if stripped.startswith('{"suite":'):
                collecting = True
                json_lines = [stripped]
            elif collecting:
                json_lines.append(stripped)
                # Try to parse what we have so far
                candidate = " ".join(json_lines)
                try:
                    parsed = json.loads(candidate)
                    suite_result["results"] = parsed.get("results", [])
                    suite_result["summary"] = parsed.get("summary", suite_result["summary"])
                    json_found = True
                    break
                except json.JSONDecodeError:
                    continue

        # Strategy 2: If JSON assembly failed, try to find the summary line
        if not json_found:
            for line in lines:
                stripped = line.strip()
                # The summary line looks like: ], "summary": {"total": N, ...}}
                if '"summary":' in stripped and '"total":' in stripped:
                    # Try to extract just the summary JSON
                    try:
                        # Find the summary object
                        idx = stripped.index('"summary":')
                        summary_str = "{" + stripped[idx:].rstrip("}")
                        # It might end with }}
                        if summary_str.endswith("}"):
                            summary_obj = json.loads("{" + stripped[idx:])
                            suite_result["summary"] = summary_obj.get("summary", suite_result["summary"])
                            json_found = True
                    except (json.JSONDecodeError, ValueError):
                        pass

        # Strategy 3: Count PASS/FAIL from human-readable stderr lines
        if not json_found:
            pass_count = 0
            fail_count = 0
            test_results_parsed = []
            for line in lines:
                stripped = line.strip()
                # Human output: "  running: test_name ... PASS (0.1ms)"
                # or individual JSON result lines
                if '"test":' in stripped and '"status":' in stripped:
                    try:
                        result_obj = json.loads(stripped.rstrip(","))
                        test_results_parsed.append(result_obj)
                        if result_obj.get("status") == "PASS":
                            pass_count += 1
                        else:
                            fail_count += 1
                    except json.JSONDecodeError:
                        pass
                elif "PASS" in stripped and "running:" in stripped:
                    pass_count += 1
                elif "FAIL" in stripped and "running:" in stripped:
                    fail_count += 1

            if test_results_parsed:
                suite_result["results"] = test_results_parsed
                json_found = True
            if pass_count + fail_count > 0:
                suite_result["summary"] = {
                    "total": pass_count + fail_count,
                    "passed": pass_count,
                    "failed": fail_count,
                    "duration_ms": round(elapsed_ms, 1),
                }
                json_found = True

        # Strategy 4: Plain-text "TEST PASSED!" / "FAILED!" sentinel lines.
        # Tests like vecaddx don't output JSON — they just print a final verdict.
        if not json_found:
            full_output_upper = output.upper()
            if ("TEST PASSED" in full_output_upper or (rc == 0 and "PASSED" in full_output_upper)) and not ("GPURT ERROR" in full_output_upper or "DEVICE OPEN FAILED" in full_output_upper or "IOCTL FAILED" in full_output_upper):
                suite_result["results"] = [{"test": suite_name, "status": "PASS", "duration_ms": round(elapsed_ms, 1)}]
                suite_result["summary"] = {"total": 1, "passed": 1, "failed": 0, "duration_ms": round(elapsed_ms, 1)}
                json_found = True
            elif "TEST FAILED" in full_output_upper or "FAILED!" in full_output_upper or rc != 0 or ("GPURT ERROR" in full_output_upper or "DEVICE OPEN FAILED" in full_output_upper or "IOCTL FAILED" in full_output_upper):
                # Extract a short error reason if present
                err_hint = ""
                for ln in lines:
                    if "error" in ln.lower() or "failed" in ln.lower():
                        err_hint = ln.strip()[:120]
                        break
                suite_result["results"] = [{"test": suite_name, "status": "FAIL", "duration_ms": round(elapsed_ms, 1),
                                            "message": err_hint or "hardware/driver initialization failed"}]
                suite_result["summary"] = {"total": 1, "passed": 0, "failed": 1, "duration_ms": round(elapsed_ms, 1)}
                json_found = True

        # Print results
        if json_found and suite_result["results"]:
            for t in suite_result["results"]:
                if t["status"] == "PASS":
                    success(f"{t['test']:45s} ({t.get('duration_ms', 0):.1f}ms)")
                else:
                    fail(f"{t['test']:45s} ({t.get('duration_ms', 0):.1f}ms): {t.get('message', '')}")
        elif json_found:
            s = suite_result["summary"]
            if s["failed"] > 0:
                fail(f"Suite: {s['passed']}/{s['total']} passed, {s['failed']} failed")
            else:
                success(f"Suite: {s['passed']}/{s['total']} passed")
        else:
            # No JSON at all — show raw output for debugging
            if rc == 0:
                success(f"Suite completed (exit 0)")
            else:
                fail(f"Suite failed with exit code {rc}")
            if output.strip():
                for out_line in output.strip().split("\n")[:15]:
                    print(f"    {C.DIM}{out_line}{C.RESET}")

        results.append(suite_result)

    return results


# ---------------------------------------------------------------------------
# Step 5: Generate report
# ---------------------------------------------------------------------------

def generate_report(results: list, cfg: dict, elapsed: float):
    """Print summary and write JSON results file."""
    total_tests = 0
    total_passed = 0
    total_failed = 0

    for suite in results:
        s = suite["summary"]
        total_tests += s["total"]
        total_passed += s["passed"]
        total_failed += s["failed"]

    header("DVF Test Run Results")

    print(f"\n  Total Tests:  {total_tests}")
    if total_passed > 0:
        print(f"  {C.GREEN}Passed:      {total_passed}{C.RESET}")
    if total_failed > 0:
        print(f"  {C.RED}Failed:      {total_failed}{C.RESET}")
    print(f"  Duration:    {elapsed:.1f}s")

    status = "PASSED" if total_failed == 0 else "FAILED"
    color = C.GREEN if total_failed == 0 else C.RED
    print(f"\n  {C.BOLD}{color}=== {status} ==={C.RESET}\n")

    # Write JSON results
    results_path = cfg.get("output", {}).get("results_file", "results/run_results.json")
    results_full = PROJECT_DIR / results_path
    results_full.parent.mkdir(parents=True, exist_ok=True)

    report = {
        "timestamp": datetime.now(tz=None).astimezone().isoformat(),
        "device": "gpgpu",
        "driver": cfg["driver"]["module_name"],
        "status": status,
        "total_tests": total_tests,
        "passed": total_passed,
        "failed": total_failed,
        "duration_seconds": round(elapsed, 2),
        "suites": results,
    }

    with open(results_full, "w") as f:
        json.dump(report, f, indent=2)

    success(f"Results written to {results_full}")


# ---------------------------------------------------------------------------
# FPGA Native Mode: Run tests directly on the host (no QEMU)
# ---------------------------------------------------------------------------

def run_tests_native(cfg: dict) -> list:
    """Run test binaries natively on the host for FPGA mode."""
    test_cfg = cfg["tests"]
    fpga_drv = cfg.get("fpga_driver", cfg["driver"])
    test_bin_dir = PROJECT_DIR / "c-test-binaries"
    results = []

    # Set up DVF_* environment variables for the test binaries
    device_env = get_device_env(cfg)
    run_env = os.environ.copy()
    run_env.update(device_env)

    info(f"FPGA native mode — device: {device_env.get('DVF_DEVICE_PATH')}")
    info(f"  regs={device_env.get('DVF_REG_COUNT')}  "
         f"reg_size={device_env.get('DVF_REG_SIZE')}  "
         f"bar={device_env.get('DVF_BAR_SIZE')}")

    # Check if device node exists
    dev_node = fpga_drv.get("device_node", "/dev/fpga0")
    if not os.path.exists(dev_node):
        warn(f"Device node {dev_node} not found — attempting to load driver...")
        ko_path = fpga_drv.get("ko_file", "")
        if ko_path and os.path.exists(ko_path):
            result = subprocess.run(["insmod", ko_path], capture_output=True, text=True)
            if result.returncode != 0:
                fail(f"insmod {ko_path} failed: {result.stderr}")
                return results
            success("FPGA driver loaded")
        else:
            fail(f"Driver .ko not found at {ko_path} and {dev_node} does not exist")
            return results

    # Run each test suite natively
    for suite_path in test_cfg["suites"]:
        binary = str(test_bin_dir / suite_path)
        suite_name = suite_path.replace("/", "::")

        info(f"\n[SUITE] {suite_name}")
        info(f"  binary: {binary}")

        suite_result = {
            "suite": suite_name,
            "binary": suite_path,
            "exit_code": -1,
            "raw_output": "",
            "results": [],
            "summary": {"total": 0, "passed": 0, "failed": 0, "duration_ms": 0},
        }

        if not os.path.isfile(binary):
            fail(f"Binary not found: {binary}")
            results.append(suite_result)
            continue

        try:
            proc = subprocess.run(
                [binary],
                env=run_env,
                capture_output=True,
                text=True,
                timeout=test_cfg.get("test_timeout", 60),
            )
            suite_result["exit_code"] = proc.returncode
            suite_result["raw_output"] = proc.stderr + proc.stdout

            for line in proc.stderr.strip().split("\n"):
                if line.strip():
                    print(f"  {line}")

            if proc.stdout.strip():
                try:
                    parsed = json.loads(proc.stdout.strip())
                    suite_result["results"] = parsed.get("results", [])
                    suite_result["summary"] = parsed.get("summary", suite_result["summary"])
                except json.JSONDecodeError:
                    pass

        except subprocess.TimeoutExpired:
            fail(f"Suite timed out after {test_cfg.get('test_timeout', 60)}s")
        except Exception as e:
            fail(f"Error running {binary}: {e}")

        results.append(suite_result)

    return results


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(description="DVF Local Test Runner")
    parser.add_argument("--config", default=str(CONFIG_PATH),
                        help="Path to config.yaml")
    parser.add_argument("--suite", default="all",
                        help="Run specific suite category (smoke, stress, all)")
    parser.add_argument("--build-only", action="store_true",
                        help="Only build test binaries, don't run")
    parser.add_argument("--skip-build", action="store_true",
                        help="Skip the build step")
    parser.add_argument("--kvm", action="store_true",
                        help="Enable KVM acceleration (off by default)")
    parser.add_argument("--target", choices=["qemu", "fpga", "hybrid"],
                        default=None,
                        help="Override target_mode from config (qemu/fpga/hybrid)")
    args = parser.parse_args()

    cfg = load_config(Path(args.config))

    # CLI --target overrides config file
    target_mode = args.target or cfg.get("target_mode", "qemu")
    cfg["target_mode"] = target_mode

    header("DVF Driver Validation Suite")
    info(f"Target mode: {target_mode}")
    if target_mode in ("qemu", "hybrid"):
        info(f"Device: {cfg['qemu']['device_name']}")
        info(f"QEMU:   {cfg['qemu']['binary']}")
    if target_mode in ("fpga", "hybrid"):
        fpga_cfg = cfg.get("fpga", {})
        info(f"FPGA PCI: {fpga_cfg.get('pci_address', '(not set)')}")
        info(f"FPGA dev: {fpga_cfg.get('device_node', '/dev/fpga0')}")
    info(f"Driver: {cfg['driver']['module_name']}")

    # Step 1: Build
    if not args.skip_build:
        if not build_tests(cfg):
            sys.exit(1)

    if args.build_only:
        info("Build-only mode — exiting.")
        sys.exit(0)

    # Filter suites if requested
    if args.suite != "all":
        cfg["tests"]["suites"] = [
            s for s in cfg["tests"]["suites"]
            if args.suite in s
        ]
        if not cfg["tests"]["suites"]:
            fail(f"No suites matching '{args.suite}'")
            sys.exit(1)

    info(f"Suites to run: {len(cfg['tests']['suites'])}")

    # --- Dispatch by target mode ---
    if target_mode == "fpga":
        start_time = time.time()
        results = run_tests_native(cfg)
        elapsed = time.time() - start_time
        generate_report(results, cfg, elapsed)
        total_failed = sum(s["summary"]["failed"] for s in results)
        sys.exit(1 if total_failed > 0 else 0)

    # qemu or hybrid mode: boot QEMU
    # Kill any stale DVF QEMU that might be holding the rootfs write-lock
    kill_stale_qemu(cfg["qemu"]["rootfs"])

    cmd = build_qemu_cmd(cfg)
    if args.kvm:
        cmd.extend(["-enable-kvm"])

    vm = QEMUInstance(cmd, boot_timeout=cfg["qemu"]["boot_timeout"])
    start_time = time.time()

    # Handle Ctrl+C gracefully
    def signal_handler(sig, frame):
        warn("\nInterrupted — shutting down QEMU...")
        vm.shutdown()
        sys.exit(130)
    signal.signal(signal.SIGINT, signal_handler)

    if not vm.start():
        vm.shutdown()
        sys.exit(1)

    # Step 3: Run tests
    try:
        results = run_tests(vm, cfg)
    except Exception as e:
        fail(f"Test execution error: {e}")
        results = []
    finally:
        vm.shutdown()

    elapsed = time.time() - start_time

    # Step 4: Report
    generate_report(results, cfg, elapsed)

    # Exit with appropriate code
    total_failed = sum(s["summary"]["failed"] for s in results)
    sys.exit(1 if total_failed > 0 else 0)


if __name__ == "__main__":
    main()

