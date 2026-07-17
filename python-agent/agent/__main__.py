#!/usr/bin/env python3
"""
DVF Guest Agent — runs inside the QEMU VM.

Communication: reads/writes newline-delimited JSON over
/dev/virtio-ports/dvf.agent.0 (virtio-serial, no network needed).

Protocol (see VirtioSerialHub in go-orchestrator for host side):

  Guest → Host:
    {"msg":"register","vm_id":"...","hostname":"...","agent_version":"1.0"}
    {"msg":"heartbeat","vm_id":"...","state":"READY"}
    {"msg":"result","command_id":"...","status":"passed","output":"...","logs":"...","duration_ms":100}
    {"msg":"log","vm_id":"...","severity":"INFO","message":"..."}

  Host → Guest:
    {"msg":"ack","agent_id":"..."}
    {"msg":"command","command_id":"...","cmd":"load_driver","params":{"ko_path":"..."}}

Supported commands:
  - load_driver   : insmod a .ko from the 9p share
  - unload_driver : rmmod a module
  - verify_device : ls the device node; dump dmesg on failure
  - start_test    : run a Vishwa (or generic) binary, stream logs, parse results
  - shutdown      : stop the agent loop

Zero external dependencies — pure Python stdlib.
"""

import json
import os
import platform
import re
import subprocess
import sys
import threading
import time


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _run(cmd, **kwargs):
    """Run a command and return (stdout+stderr, returncode).

    Always injects a full PATH so tools like insmod/rmmod/mount are found
    even when the agent starts from a minimal init environment with no PATH.
    """
    env = kwargs.pop("env", None) or os.environ.copy()
    env.setdefault("PATH", "/sbin:/usr/sbin:/bin:/usr/bin:/usr/local/sbin:/usr/local/bin")
    result = subprocess.run(cmd, capture_output=True, text=True, env=env, **kwargs)
    return (result.stdout + result.stderr).strip(), result.returncode



def mount_guest_filesystems(mount_path: str = "/mnt/share"):
    """
    Mount the 9p host share and essential virtual filesystems.

    Called once at agent startup so the share is available when the
    first command arrives.  All mounts are best-effort; the agent
    continues even if some are already mounted.
    """
    print("[agent] Mounting essential filesystems...")

    # 9p virtio share
    os.makedirs(mount_path, exist_ok=True)
    out, rc = _run([
        "mount", "-t", "9p",
        "-o", "trans=virtio,version=9p2000.L",
        "hostshare", mount_path,
    ])
    if rc == 0:
        print(f"[agent] 9p share mounted at {mount_path}")
    else:
        # Try without version= for older kernels
        out2, rc2 = _run(["mount", "-t", "9p", "-o", "trans=virtio",
                          "hostshare", mount_path])
        if rc2 == 0:
            print(f"[agent] 9p share mounted (compat) at {mount_path}")
        else:
            print(f"[agent] WARN: 9p mount failed ({out or out2}); continuing")

    # Essential virtual filesystems (skipped if already mounted)
    for fs, fstype, target in [
        ("proc",     "proc",     "/proc"),
        ("sysfs",    "sysfs",    "/sys"),
        ("devtmpfs", "devtmpfs", "/dev"),
    ]:
        _run(["mount", "-t", fstype, fs, target])

    print("[agent] Filesystem setup complete")


def detect_devices():
    """Detect PCI devices available in the guest."""
    try:
        out = subprocess.check_output(["ls", "/dev/"], text=True)
        return [f"/dev/{d}" for d in out.split()
                if d.startswith("gpgpu") or d.startswith("gp_gpu") or d.startswith("fpga")]
    except Exception:
        return []


def _read_vm_id_from_cmdline() -> str:
    """
    Read dvf_vm_id=<value> from /proc/cmdline.
    Returns empty string if not found.
    """
    try:
        with open("/proc/cmdline") as f:
            cmdline = f.read()
        match = re.search(r"dvf_vm_id=(\S+)", cmdline)
        if match:
            return match.group(1)
    except Exception:
        pass
    return ""


# ---------------------------------------------------------------------------
# Output parser — 4-strategy Vishwa result extraction
# (ported from runner/local_runner.py)
# ---------------------------------------------------------------------------

def parse_vishwa_output(output: str, suite_name: str, elapsed_ms: float) -> dict:
    """
    Extract structured test results from raw Vishwa binary output.

    Returns a dict compatible with the orchestrator's processResults JSON schema:
      { "suite": ..., "results": [...], "summary": {...} }
    """
    lines = output.split("\n")
    suite_result = {
        "suite": suite_name,
        "results": [],
        "summary": {"total": 0, "passed": 0, "failed": 0, "duration_ms": round(elapsed_ms, 1)},
    }
    json_found = False

    # Strategy 1: find full JSON object starting with {"suite":
    json_lines: list[str] = []
    collecting = False
    for line in lines:
        stripped = line.strip()
        if stripped.startswith('{"suite":'):
            collecting = True
            json_lines = [stripped]
        elif collecting:
            json_lines.append(stripped)
            try:
                parsed = json.loads(" ".join(json_lines))
                suite_result["results"] = parsed.get("results", [])
                suite_result["summary"] = parsed.get("summary", suite_result["summary"])
                json_found = True
                break
            except json.JSONDecodeError:
                continue

    # Strategy 2: extract summary line
    if not json_found:
        for line in lines:
            stripped = line.strip()
            if '"summary":' in stripped and '"total":' in stripped:
                try:
                    idx = stripped.index('"summary":')
                    summary_obj = json.loads("{" + stripped[idx:])
                    suite_result["summary"] = summary_obj.get("summary", suite_result["summary"])
                    json_found = True
                except (json.JSONDecodeError, ValueError):
                    pass

    # Strategy 3: parse individual JSON result lines
    if not json_found:
        parsed_results = []
        pass_count = fail_count = 0
        for line in lines:
            stripped = line.strip()
            if '"test":' in stripped and '"status":' in stripped:
                try:
                    obj = json.loads(stripped.rstrip(","))
                    parsed_results.append(obj)
                    if obj.get("status") == "PASS":
                        pass_count += 1
                    else:
                        fail_count += 1
                except json.JSONDecodeError:
                    pass
            elif "PASS" in stripped and "running:" in stripped:
                pass_count += 1
            elif "FAIL" in stripped and "running:" in stripped:
                fail_count += 1

        if parsed_results:
            suite_result["results"] = parsed_results
            json_found = True
        if pass_count + fail_count > 0:
            suite_result["summary"] = {
                "total": pass_count + fail_count,
                "passed": pass_count,
                "failed": fail_count,
                "duration_ms": round(elapsed_ms, 1),
            }
            json_found = True

    # Strategy 4: plain-text "TEST PASSED!" / "FAILED!" sentinel
    if not json_found:
        upper = output.upper()
        if ("TEST PASSED" in upper or "PASSED" in upper) and not ("GPURT ERROR" in upper or "DEVICE OPEN FAILED" in upper or "IOCTL FAILED" in upper):
            suite_result["results"] = [{"test": suite_name, "status": "PASS",
                                        "duration_ms": round(elapsed_ms, 1)}]
            suite_result["summary"] = {"total": 1, "passed": 1, "failed": 0,
                                       "duration_ms": round(elapsed_ms, 1)}
        else:
            err_hint = ""
            for ln in lines:
                if "error" in ln.lower() or "failed" in ln.lower():
                    err_hint = ln.strip()[:120]
                    break
            suite_result["results"] = [{"test": suite_name, "status": "FAIL",
                                        "duration_ms": round(elapsed_ms, 1),
                                        "message": err_hint or "hardware/driver initialization failed"}]
            suite_result["summary"] = {"total": 1, "passed": 0, "failed": 1,
                                       "duration_ms": round(elapsed_ms, 1)}

    return suite_result


# ---------------------------------------------------------------------------
# Agent — virtio-serial transport
# ---------------------------------------------------------------------------

VIRTIO_PORT = "/dev/virtio-ports/dvf.agent.0"


class DVFAgent:
    def __init__(self, vm_id: str, port_path: str, mount_path: str = "/mnt/share"):
        self.vm_id = vm_id
        self.port_path = port_path
        self.mount_path = mount_path
        self.agent_id: str | None = None
        self.heartbeat_interval = 5
        self.running = True

        # Thread-safe write lock for the virtio-serial port.
        # Multiple threads (heartbeat, log streaming, result reporting)
        # may write concurrently; the lock prevents interleaved JSON lines.
        self._write_lock = threading.Lock()
        self._port = None  # opened in run()

    # ------------------------------------------------------------------ #
    #  Virtio-serial I/O                                                   #
    # ------------------------------------------------------------------ #

    def _send(self, msg: dict):
        """Write a single JSON line to the virtio-serial port (thread-safe).

        Uses raw os.write() on the file descriptor — Python's buffered text-mode
        IO stalls silently on character devices (/dev/vportNpN).
        """
        data = (json.dumps(msg, separators=(",", ":")) + "\n").encode("utf-8")
        with self._write_lock:
            os.write(self._fd, data)

    def _recv(self) -> dict | None:
        """Read one JSON line from the virtio-serial port. Returns None on EOF.

        Uses raw os.read() byte-by-byte until newline — reliable on char devices.
        """
        buf = b""
        while True:
            try:
                ch = os.read(self._fd, 1)
            except OSError:
                return None
            if not ch:
                return None
            buf += ch
            if ch == b"\n":
                break
        try:
            return json.loads(buf.decode("utf-8"))
        except (json.JSONDecodeError, UnicodeDecodeError):
            print(f"[agent] WARN: bad JSON from host: {buf!r}")
            return {}

    # ------------------------------------------------------------------ #
    #  Registration / heartbeat                                           #
    # ------------------------------------------------------------------ #

    def register(self):
        """Register this agent with the orchestrator via virtio-serial."""
        self._send({
            "msg": "register",
            "vm_id": self.vm_id,
            "hostname": platform.node(),
            "os_version": platform.platform(),
            "agent_version": "2.0.0",
            "available_devices": detect_devices(),
        })
        # Wait for ack
        ack = self._recv()
        if ack and ack.get("msg") == "ack":
            self.agent_id = ack.get("agent_id", f"agent-{self.vm_id}")
            print(f"[agent] Registered as {self.agent_id}")
        else:
            # Fallback — proceed anyway
            self.agent_id = f"agent-{self.vm_id}"
            print(f"[agent] WARN: no ack received, using fallback ID {self.agent_id}")

    def heartbeat_loop(self):
        """Send periodic heartbeats in background."""
        while self.running:
            try:
                self._send({
                    "msg": "heartbeat",
                    "vm_id": self.vm_id,
                    "state": "READY",
                })
            except Exception as e:
                print(f"[agent] Heartbeat failed: {e}")
            time.sleep(self.heartbeat_interval)

    def command_loop(self):
        """Read commands from virtio-serial and execute them."""
        while self.running:
            msg = self._recv()
            if msg is None:
                # EOF — host closed the connection
                print("[agent] virtio-serial EOF, stopping")
                break
            if msg.get("msg") != "command":
                continue
            print(f"[agent] Got command: {msg.get('cmd')} ({msg.get('command_id')})")
            self.execute(msg)

    # ------------------------------------------------------------------ #
    #  Command dispatch                                                   #
    # ------------------------------------------------------------------ #

    def execute(self, msg: dict):
        """Dispatch a command and report the result."""
        cmd_id = msg.get("command_id", "")
        cmd_type = msg.get("cmd", "")
        params = msg.get("params", {})
        start = time.time()
        status = "passed"
        output = ""
        logs = ""

        try:
            if cmd_type == "load_driver":
                output, status = self._handle_load_driver(params)

            elif cmd_type == "unload_driver":
                module = params.get("module", "")
                out, rc = _run(["rmmod", module])
                output = out
                status = "passed" if rc == 0 else "failed"

            elif cmd_type == "verify_device":
                output, status, logs = self._handle_verify_device(params)

            elif cmd_type == "start_test":
                output, status, logs = self._handle_start_test(params, cmd_id)

            elif cmd_type == "shutdown":
                self.running = False
                status = "passed"
                output = "shutting down"

            else:
                status = "errored"
                output = f"unknown command type: {cmd_type}"

        except Exception as e:
            status = "errored"
            output = str(e)

        duration_ms = int((time.time() - start) * 1000)

        # Stream summary log
        self._stream_log(f"Command {cmd_id} ({cmd_type}): {status}", "INFO")
        if logs:
            self._stream_log(logs[:4096], "DEBUG")

        # Report result
        self._send({
            "msg": "result",
            "command_id": cmd_id,
            "status": status,
            "output": output,
            "logs": logs,
            "duration_ms": duration_ms,
        })
        print(f"[agent] Reported: {cmd_id} -> {status} ({duration_ms}ms)")

    # ------------------------------------------------------------------ #
    #  Command handlers                                                   #
    # ------------------------------------------------------------------ #

    def _handle_load_driver(self, params: dict) -> tuple[str, str]:
        """insmod the .ko from the 9p share."""
        ko_path = params.get("ko_path", params.get("module_path", ""))
        if not ko_path:
            return "ko_path parameter is required", "errored"

        # Verify the file is accessible
        if not os.path.exists(ko_path):
            return f"ko file not found: {ko_path}", "errored"

        out, rc = _run(["insmod", ko_path])
        if rc != 0:
            # Capture dmesg for context
            dmesg, _ = _run(["sh", "-c", "dmesg | tail -5"])
            return f"insmod failed (rc={rc}): {out}\ndmesg: {dmesg}", "failed"
        return out or "driver loaded", "passed"

    def _handle_verify_device(self, params: dict) -> tuple[str, str, str]:
        """ls the device node; dump dmesg on failure."""
        device_node = params.get("device_node", "/dev/gp_gpu")

        # Give the kernel a moment to complete PCI probe
        time.sleep(1)

        out, rc = _run(["ls", "-la", device_node])
        if rc == 0:
            return out, "passed", ""

        # Device not found — collect diagnostics
        dmesg, _ = _run(["sh", "-c", "dmesg | tail -20"])
        lspci, _ = _run([
            "sh", "-c",
            "lspci -nn 2>/dev/null || "
            "cat /proc/bus/pci/devices 2>/dev/null || "
            "echo 'no lspci'",
        ])
        logs = f"dmesg:\n{dmesg}\n\nlspci:\n{lspci}"
        return (f"device node {device_node} not found (rc={rc}): {out}",
                "failed", logs)

    def _handle_start_test(self, params: dict, command_id: str) -> tuple[str, str, str]:
        """
        Run a Vishwa (or generic) test binary.

        For Vishwa binaries:
          - cd into binary_dir so relative assets (kernel.cl, input.jpg…) resolve
          - run via the Vishwa ld-linux loader to bypass guest glibc
          - apply the full env dict
          - stream raw output line-by-line to host via virtio-serial log messages
          - apply the 4-strategy parser to extract structured results
        """
        binary = params.get("binary", "")
        timeout = int(params.get("timeout", "120"))
        binary_dir = params.get("binary_dir", "")
        loader = params.get("loader", "")
        lib_dir = params.get("lib_dir", "")
        suite_name = params.get("suite_name", binary)

        # Decode env — the orchestrator JSON-encodes nested dicts
        extra_env: dict[str, str] = {}
        env_raw = params.get("env", "")
        if env_raw:
            try:
                extra_env = json.loads(env_raw) if isinstance(env_raw, str) else env_raw
            except (json.JSONDecodeError, TypeError):
                pass

        if not binary:
            return "binary parameter is required", "errored", ""

        # Build environment
        run_env = os.environ.copy()
        run_env.update({k: str(v) for k, v in extra_env.items()})

        # ── Strategy 1: Self-Contained Bundles ────────────────────────────────
        # If the test author placed a lib/ directory next to their binary,
        # automatically prepend it to LD_LIBRARY_PATH and the Vishwa loader
        # path.  No CI YAML changes or Packer rebuilds required.
        binary_dir_resolved = binary_dir or os.path.dirname(binary)
        bundled_lib = os.path.join(binary_dir_resolved, "lib")
        if os.path.isdir(bundled_lib):
            print(f"[agent] Found bundled lib/ at {bundled_lib} — prepending to library path")
            existing_ldpath = run_env.get("LD_LIBRARY_PATH", "")
            run_env["LD_LIBRARY_PATH"] = (
                f"{bundled_lib}:{existing_ldpath}" if existing_ldpath else bundled_lib
            )
            # Also prepend to the Vishwa loader's --library-path
            lib_dir = f"{bundled_lib}:{lib_dir}" if lib_dir else bundled_lib
        # ─────────────────────────────────────────────────────────────────────

        # Make binary executable
        try:
            os.chmod(binary, 0o755)
        except OSError:
            pass

        # Build command: use Vishwa loader if provided.
        # NOTE: ld-linux-x86-64.so.2 requires space-separated args, NOT --opt=value.
        if loader and lib_dir:
            cmd = [loader, "--library-path", lib_dir, binary]
        else:
            cmd = [binary]

        cwd = binary_dir if binary_dir else None

        # ---- streaming execution ----
        output_lines: list[str] = []
        logs_lines: list[str] = []
        t_start = time.time()
        status = "passed"

        try:
            proc = subprocess.Popen(
                cmd,
                cwd=cwd,
                env=run_env,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                bufsize=1,
            )

            # Stream stdout and stderr concurrently
            def drain(stream, store: list, severity: str):
                for line in stream:
                    line = line.rstrip("\n")
                    store.append(line)
                    self._stream_log(line, severity)

            t_out = threading.Thread(target=drain, args=(proc.stdout, output_lines, "INFO"), daemon=True)
            t_err = threading.Thread(target=drain, args=(proc.stderr, logs_lines, "DEBUG"), daemon=True)
            t_out.start()
            t_err.start()

            try:
                proc.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
                status = "errored"
                output_lines.append(f"test timed out after {timeout}s")

            t_out.join(timeout=5)
            t_err.join(timeout=5)

            if status != "errored":
                status = "passed" if proc.returncode == 0 else "failed"

        except Exception as e:
            status = "errored"
            output_lines.append(str(e))

        elapsed_ms = (time.time() - t_start) * 1000.0
        raw_output = "\n".join(output_lines)
        raw_logs = "\n".join(logs_lines)

        # Parse structured results from raw output
        parsed = parse_vishwa_output(raw_output + "\n" + raw_logs, suite_name, elapsed_ms)
        json_output = json.dumps(parsed)

        # ── Status reconciliation ─────────────────────────────────────────────
        # A binary may exit 0 even when hardware/GPURT errors occurred (e.g.
        # vecaddx exits 0 despite [GPURT ERROR] device open failures).
        # The parser has already correctly identified these as failures, so
        # we MUST trust the parsed result over the raw exit code.
        if status == "passed":
            summary = parsed.get("summary", {})
            parsed_failed = summary.get("failed", 0)
            parsed_passed = summary.get("passed", 0)
            parsed_total  = summary.get("total", 0)
            # Mark failed if the parser found failures, OR if it found zero
            # passes on a non-empty run (nothing succeeded at all).
            if parsed_failed > 0 or (parsed_total > 0 and parsed_passed == 0):
                status = "failed"
                print(
                    f"[agent] Status reconciled: exit code was 0 but parser detected "
                    f"{parsed_failed} failure(s) / {parsed_passed} pass(es) — marking FAILED"
                )
        # ─────────────────────────────────────────────────────────────────────

        return json_output, status, raw_logs

    # ------------------------------------------------------------------ #
    #  Telemetry                                                          #
    # ------------------------------------------------------------------ #

    def _stream_log(self, message: str, severity: str = "INFO"):
        """Send a single log entry to the host via virtio-serial (best-effort)."""
        try:
            self._send({
                "msg": "log",
                "vm_id": self.vm_id,
                "severity": severity,
                "message": message[:4096],
            })
        except Exception:
            pass  # telemetry is best-effort, never block the agent

    # ------------------------------------------------------------------ #
    #  Entry point                                                        #
    # ------------------------------------------------------------------ #

    def run(self):
        """Main entry point."""
        # Open the virtio-serial port as a raw file descriptor.
        # O_RDWR | O_NOCTTY: read-write, don't make it the controlling terminal.
        # Python's buffered text IO (open(..., 'r+')) stalls silently on char
        # devices — raw fd I/O via os.read/os.write is the only reliable option.
        print(f"[agent] Opening virtio-serial port (raw fd): {self.port_path}")
        self._fd = os.open(self.port_path, os.O_RDWR | os.O_NOCTTY)

        try:
            self.register()

            hb = threading.Thread(target=self.heartbeat_loop, daemon=True)
            hb.start()

            self.command_loop()
        finally:
            os.close(self._fd)

        print("[agent] Agent stopped.")


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def main():
    # Resolve VM ID — prefer env var, fall back to /proc/cmdline
    vm_id = os.environ.get("DVF_VM_ID", "") or _read_vm_id_from_cmdline()
    if not vm_id:
        print("[agent] ERROR: dvf_vm_id not found on kernel cmdline")
        sys.exit(1)

    print(f"[agent] VM ID: {vm_id}")

    # Mount filesystems before registering
    mount_guest_filesystems("/mnt/share")

    # Wait for virtio-serial port to appear.
    # /dev/virtio-ports/dvf.agent.0 is a udev symlink (not created without udev).
    # The raw device is /dev/vportNpN — scan dynamically since the bus index
    # depends on QEMU's PCI enumeration order (vport1p1 is common, not vport0p0).
    port_path = None
    print("[agent] Waiting for virtio-serial port...")
    for attempt in range(30):
        # Preferred: udev symlink
        if os.path.exists(VIRTIO_PORT):
            port_path = VIRTIO_PORT
            break
        # Fallback: find any /dev/vportNpN device
        try:
            vports = sorted(f"/dev/{d}" for d in os.listdir("/dev")
                            if d.startswith("vport"))
            if vports:
                port_path = vports[0]
                print(f"[agent] Found raw vport device: {port_path}")
                break
        except Exception:
            pass
        if attempt == 5:
            try:
                all_vports = [d for d in os.listdir("/dev") if d.startswith("vport")]
                print(f"[agent] /dev/vport* so far: {all_vports}")
            except Exception:
                pass
        time.sleep(1)

    if not port_path:
        try:
            all_vports = [d for d in os.listdir("/dev") if d.startswith("vport")]
        except Exception:
            all_vports = []
        print(f"[agent] ERROR: no virtio-serial port found after 30s (found: {all_vports})")
        sys.exit(1)

    print(f"[agent] Using port: {port_path}")

    agent = DVFAgent(vm_id, port_path, "/mnt/share")
    agent.run()


if __name__ == "__main__":
    main()
