#!/usr/bin/env python3
"""
DVF Guest Agent — runs inside the QEMU VM.

Lifecycle (executed in order):
  1. On startup: mount 9p share + essential VFS (/proc, /sys, /dev)
  2. Register with the orchestrator via gRPC
  3. Poll for commands, execute them, report results back

Supported commands:
  - load_driver   : insmod a .ko from the 9p share
  - unload_driver : rmmod a module
  - verify_device : ls the device node; dump dmesg on failure
  - start_test    : run a Vishwa (or generic) binary, stream logs, parse results
  - shutdown      : stop the agent loop

Environment variables / parameters for start_test (Vishwa):
  binary      — absolute path to test binary
  binary_dir  — cd here before running (relative assets resolve correctly)
  loader      — ld-linux path inside the 9p share (bypasses host glibc)
  lib_dir     — LD_LIBRARY_PATH root for Vishwa libs
  env         — JSON-encoded dict of extra env vars (OCL_ICD_VENDORS, etc.)
  timeout     — seconds (default 60)

Usage (inside guest):
  python3 -m agent --vm-id <id> --host 10.0.2.2:50051

Requires: grpcio, grpcio-tools, protobuf (pre-installed or in 9p share venv)
"""

import argparse
import json
import os
import platform
import re
import subprocess
import sys
import threading
import time

import grpc
from google.protobuf.timestamp_pb2 import Timestamp

# ponytail: generated stubs are imported from the proto output dir.
# If running inside the VM the share mount has these pre-built.
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "proto_gen"))

import agent_pb2
import agent_pb2_grpc
import telemetry_pb2
import telemetry_pb2_grpc


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def now_ts():
    """Return a protobuf Timestamp for the current time."""
    ts = Timestamp()
    ts.GetCurrentTime()
    return ts


def _run(cmd, **kwargs):
    """Run a command and return (stdout+stderr, returncode)."""
    result = subprocess.run(cmd, capture_output=True, text=True, **kwargs)
    return (result.stdout + result.stderr).strip(), result.returncode


def mount_guest_filesystems(mount_path: str = "/mnt/share"):
    """
    Mount the 9p host share and essential virtual filesystems.

    Called once at agent startup — before RegisterAgent — so the share is
    available when the first command arrives.  All mounts are best-effort;
    the agent continues even if some are already mounted.
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
        if "TEST PASSED" in upper or ("PASSED" in upper):
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
                                        "message": err_hint or "non-zero exit"}]
            suite_result["summary"] = {"total": 1, "passed": 0, "failed": 1,
                                       "duration_ms": round(elapsed_ms, 1)}

    return suite_result


# ---------------------------------------------------------------------------
# Agent
# ---------------------------------------------------------------------------

class DVFAgent:
    def __init__(self, vm_id: str, host: str, mount_path: str = "/mnt/share"):
        self.vm_id = vm_id
        self.host = host
        self.mount_path = mount_path
        self.agent_id: str | None = None
        self.heartbeat_interval = 5
        self.running = True

        self.channel = grpc.insecure_channel(host)
        self.agent_stub = agent_pb2_grpc.AgentServiceStub(self.channel)
        self.telemetry_stub = telemetry_pb2_grpc.TelemetryServiceStub(self.channel)

    # ------------------------------------------------------------------ #
    #  Registration / heartbeat                                           #
    # ------------------------------------------------------------------ #

    def register(self):
        """Register this agent with the orchestrator."""
        resp = self.agent_stub.RegisterAgent(agent_pb2.RegisterAgentRequest(
            vm_id=self.vm_id,
            hostname=platform.node(),
            os_version=platform.platform(),
            agent_version="1.1.0",
            available_devices=detect_devices(),
            timestamp=now_ts(),
        ))
        self.agent_id = resp.agent_id
        self.heartbeat_interval = resp.heartbeat_interval_seconds
        print(f"[agent] Registered as {self.agent_id}")

    def heartbeat_loop(self):
        """Send periodic heartbeats in background."""
        while self.running:
            try:
                self.agent_stub.Heartbeat(agent_pb2.HeartbeatRequest(
                    agent_id=self.agent_id,
                    vm_id=self.vm_id,
                    state="READY",
                    timestamp=now_ts(),
                ))
            except Exception as e:
                print(f"[agent] Heartbeat failed: {e}")
            time.sleep(self.heartbeat_interval)

    def command_loop(self):
        """Poll for commands and execute them."""
        while self.running:
            try:
                cmd = self.agent_stub.GetCommand(agent_pb2.GetCommandRequest(
                    agent_id=self.agent_id,
                    vm_id=self.vm_id,
                ))
                print(f"[agent] Got command: {cmd.type} ({cmd.command_id})")
                self.execute(cmd)
            except grpc.RpcError as e:
                if e.code() == grpc.StatusCode.CANCELLED:
                    break
                print(f"[agent] GetCommand error: {e}")
                time.sleep(2)

    # ------------------------------------------------------------------ #
    #  Command dispatch                                                   #
    # ------------------------------------------------------------------ #

    def execute(self, cmd):
        """Dispatch a command and report the result."""
        start = time.time()
        status = "passed"
        output = ""
        logs = ""

        try:
            if cmd.type == "load_driver":
                output, status = self._handle_load_driver(cmd.parameters)

            elif cmd.type == "unload_driver":
                module = cmd.parameters.get("module", "")
                out, rc = _run(["rmmod", module])
                output = out
                status = "passed" if rc == 0 else "failed"

            elif cmd.type == "verify_device":
                output, status, logs = self._handle_verify_device(cmd.parameters)

            elif cmd.type == "start_test":
                output, status, logs = self._handle_start_test(
                    cmd.parameters, cmd.command_id)

            elif cmd.type == "shutdown":
                self.running = False
                status = "passed"
                output = "shutting down"

            else:
                status = "errored"
                output = f"unknown command type: {cmd.type}"

        except Exception as e:
            status = "errored"
            output = str(e)

        duration_ms = int((time.time() - start) * 1000)

        # Stream summary log to telemetry
        self._stream_log(f"Command {cmd.command_id} ({cmd.type}): {status}", "INFO")
        if logs:
            self._stream_log(logs[:4096], "DEBUG")

        # Report result
        self.agent_stub.ReportResult(agent_pb2.ReportResultRequest(
            agent_id=self.agent_id,
            command_id=cmd.command_id,
            status=status,
            output=output,
            logs=logs,
            duration_ms=duration_ms,
            timestamp=now_ts(),
        ))
        print(f"[agent] Reported: {cmd.command_id} -> {status} ({duration_ms}ms)")

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
          - stream raw output line-by-line to TelemetryService
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

        # Make binary executable
        try:
            os.chmod(binary, 0o755)
        except OSError:
            pass

        # Build command: use Vishwa loader if provided
        if loader and lib_dir:
            cmd = [loader, f"--library-path={lib_dir}", binary]
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

        return json_output, status, raw_logs

    # ------------------------------------------------------------------ #
    #  Telemetry                                                          #
    # ------------------------------------------------------------------ #

    def _stream_log(self, message: str, severity: str = "INFO"):
        """Send a single log entry to the telemetry service (best-effort)."""
        try:
            def gen():
                yield telemetry_pb2.LogEntry(
                    vm_id=self.vm_id,
                    agent_id=self.agent_id or "",
                    severity=severity,
                    source="dvf-agent",
                    message=message,
                    timestamp=now_ts(),
                )
            self.telemetry_stub.StreamLogs(gen())
        except Exception:
            pass  # ponytail: telemetry is best-effort, never block the agent

    # ------------------------------------------------------------------ #
    #  Entry point                                                        #
    # ------------------------------------------------------------------ #

    def run(self):
        """Main entry point."""
        self.register()

        hb = threading.Thread(target=self.heartbeat_loop, daemon=True)
        hb.start()

        self.command_loop()
        print("[agent] Agent stopped.")


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(description="DVF Guest Agent")
    parser.add_argument(
        "--vm-id",
        default="",
        help="VM ID assigned by orchestrator (auto-detected from /proc/cmdline if empty)",
    )
    parser.add_argument(
        "--host",
        default="10.0.2.2:50051",
        help="Orchestrator gRPC address (default: QEMU user-mode NAT host)",
    )
    parser.add_argument(
        "--mount",
        default="/mnt/share",
        help="9p share mount path inside the guest",
    )
    parser.add_argument(
        "--skip-mount",
        action="store_true",
        help="Skip the initial filesystem mount step (useful for testing on host)",
    )
    args = parser.parse_args()

    # Resolve VM ID — prefer CLI flag, fall back to /proc/cmdline
    vm_id = args.vm_id or _read_vm_id_from_cmdline()
    if not vm_id:
        print("[agent] ERROR: --vm-id is required (or dvf_vm_id=<id> on kernel cmdline)")
        sys.exit(1)

    # Mount filesystems before registering
    if not args.skip_mount:
        mount_guest_filesystems(args.mount)

    agent = DVFAgent(vm_id, args.host, args.mount)
    agent.run()


if __name__ == "__main__":
    main()
