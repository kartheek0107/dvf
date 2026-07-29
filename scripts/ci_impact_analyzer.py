#!/usr/bin/env python3
"""
DVF CI Impact Analyzer & Test Runner.
Detects changed files, maps them to tests using sidecar JSON files,
resolves dependencies (DAG), and runs them via the Orchestrator API.
"""

import argparse
import json
import os
import subprocess
import sys
import time
import urllib.request
import urllib.error

# Config paths
PROJECT_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
REGISTRY_PATH = os.path.join(PROJECT_ROOT, "go-orchestrator", "configs", "device_registry.json")
C_TESTS_DIR = os.path.join(PROJECT_ROOT, "c-test-binaries")

def run_git(args):
    """Run git command and return output lines."""
    try:
        res = subprocess.run(["git"] + args, cwd=PROJECT_ROOT, capture_output=True, text=True, check=True)
        return [line.strip() for line in res.stdout.splitlines() if line.strip()]
    except Exception as e:
        print(f"Error running git {' '.join(args)}: {e}")
        return []

def get_changed_files(compare_ref):
    """Get list of changed files compared to compare_ref."""
    if not compare_ref or compare_ref == "0000000000000000000000000000000000000000":
        compare_ref = "HEAD~1"
    
    # Check if compare_ref is available locally
    local_check = subprocess.run(["git", "cat-file", "-e", compare_ref], cwd=PROJECT_ROOT, capture_output=True)
    if local_check.returncode != 0:
        # Not found locally, try fetching from origin silently
        subprocess.run(["git", "fetch", "origin", compare_ref], cwd=PROJECT_ROOT, capture_output=True)
        
    return run_git(["diff", "--name-only", compare_ref, "HEAD"])

def load_device_registry():
    """Load registry to map drivers to devices."""
    if not os.path.exists(REGISTRY_PATH):
        return {"devices": []}
    with open(REGISTRY_PATH) as f:
        return json.load(f)

def qemu_devices(registry):
    """Return only devices that support QEMU emulation.

    Devices with target_modes that do NOT include "qemu" (e.g. pure FPGA
    hardware devices) cannot be run in CI — scheduling them produces
    CANCELLED results that fail the job and pollute the summary.
    Devices with no target_modes set are assumed to support QEMU (backward compat).
    """
    result = []
    for dev in registry.get("devices", []):
        modes = dev.get("target_modes", [])
        if not modes or "qemu" in modes:
            result.append(dev)
    return result

def load_sidecar(category, name):
    """Load JSON sidecar for a test."""
    path = os.path.join(C_TESTS_DIR, category, f"{name}.json")
    if os.path.exists(path):
        with open(path) as f:
            return json.load(f)
    return None

def find_all_sidecars():
    """Find all test sidecar files and their dependencies."""
    sidecars = {}
    for root, _, files in os.walk(C_TESTS_DIR):
        for file in files:
            if file.endswith(".json"):
                rel_path = os.path.relpath(os.path.join(root, file), C_TESTS_DIR)
                category = os.path.dirname(rel_path)
                test_name = os.path.splitext(file)[0]
                key = f"{category}/{test_name}"
                with open(os.path.join(root, file)) as f:
                    try:
                        sidecars[key] = json.load(f)
                    except Exception:
                        pass
    return sidecars

def get_affected_tests(changed_files, registry, sidecars):
    """Determine which tests are affected based on changed files."""
    run_all = False
    affected_tests = set()
    affected_devices = set()

    for file in changed_files:
        # Check global environment changes
        if any(p in file for p in ["go-orchestrator/", "runner/", "qemu-accelerator-models/", "guest-os/", ".gitlab-ci.yml"]):
            print(f"Global/infra file changed: {file}. Running all tests.")
            run_all = True
            break
        
        # Check driver changes
        if file.startswith("driver-source/"):
            parts = file.split("/")
            if len(parts) > 1:
                driver_dir = parts[1]
                for dev in qemu_devices(registry):
                    if dev.get("driver_module") == driver_dir or dev.get("id") in driver_dir:
                        print(f"Driver changed: {file}. Scheduling tests for device: {dev['id']}")
                        affected_devices.add(dev["id"])
                        for suite in dev.get("test_suites", []):
                            affected_tests.add((dev["id"], suite))

        # Check test code changes
        elif file.startswith("c-test-binaries/"):
            parts = file.split("/")
            if len(parts) > 2:
                category = parts[1]
                test_file = parts[2]
                test_name = os.path.splitext(test_file)[0]
                key = f"{category}/{test_name}"
                if key in sidecars:
                    print(f"Test case changed: {file}. Scheduling {key}")
                    # Find all QEMU-capable devices supporting this test or capabilities
                    test_caps = set(sidecars[key].get("capabilities", []))
                    for dev in qemu_devices(registry):
                        # Match by capability overlap
                        dev_caps = set(dev.get("capabilities", []))
                        if test_caps.issubset(dev_caps) or key in dev.get("test_suites", []):
                            affected_tests.add((dev["id"], key))

    if run_all:
        for dev in qemu_devices(registry):
            for suite in dev.get("test_suites", []):
                affected_tests.add((dev["id"], suite))

    return affected_tests

def build_dag(affected, sidecars):
    """Build dependency DAG for affected tests and sort topologically."""
    adj = {}
    in_degree = {}
    
    # Initialize graphs
    for dev_id, test_key in affected:
        node = (dev_id, test_key)
        if node not in adj:
            adj[node] = []
            in_degree[node] = 0

    # Add edges based on dependencies within the same device
    for (dev_id, test_key) in list(adj.keys()):
        meta = sidecars.get(test_key)
        if meta:
            for dep in meta.get("dependencies", []):
                if not dep or not dep.strip():
                    continue  # skip empty/blank dependency strings
                dep_node = (dev_id, dep)
                # If dependency is not already in queue, we must add it to ensure correctness
                if dep_node not in adj:
                    adj[dep_node] = []
                    in_degree[dep_node] = 0
                
                adj[dep_node].append((dev_id, test_key))
                in_degree[(dev_id, test_key)] += 1

    # Topological sort (Kahn's algorithm)
    queue = [node for node, deg in in_degree.items() if deg == 0]
    sorted_nodes = []

    while queue:
        # Sort queue to ensure deterministic run order
        queue.sort()
        curr = queue.pop(0)
        sorted_nodes.append(curr)
        for neighbor in adj[curr]:
            in_degree[neighbor] -= 1
            if in_degree[neighbor] == 0:
                queue.append(neighbor)

    if len(sorted_nodes) < len(adj):
        print("Warning: Cycle detected in dependencies! Fallback to default sort.")
        return sorted(list(adj.keys()))

    return sorted_nodes

def submit_test_run(api_url, device_id, test_suite_id):
    """Submit test run via REST API."""
    url = f"{api_url}/api/v1/test-runs"
    data = json.dumps({
        "deviceId": device_id,
        "testSuiteId": test_suite_id,
        "priority": 1,
        "requestedBy": "gitlab-ci"
    }).encode("utf-8")
    
    req = urllib.request.Request(
        url, data=data,
        headers={"Content-Type": "application/json"},
        method="POST"
    )
    
    try:
        with urllib.request.urlopen(req) as resp:
            body = json.loads(resp.read().decode("utf-8"))
            return body.get("testRun", {}).get("id")
    except Exception as e:
        print(f"Failed to submit test run for {device_id}/{test_suite_id}: {e}")
        return None

def get_test_run_status(api_url, run_id):
    """Retrieve test run status and error message."""
    url = f"{api_url}/api/v1/test-runs/{run_id}"
    try:
        with urllib.request.urlopen(url) as resp:
            body = json.loads(resp.read().decode("utf-8"))
            return body.get("status"), body.get("errorMessage")
    except Exception as e:
        print(f"Failed to fetch status for run {run_id}: {e}")
        return "UNKNOWN", str(e)

def main():
    parser = argparse.ArgumentParser(description="DVF CI Impact Analyzer")
    parser.add_argument("--compare", default=os.getenv("CI_COMMIT_BEFORE_SHA"), help="Git ref to compare against")
    parser.add_argument("--api", default=os.getenv("DVF_API_URL", "http://localhost:8080"), help="Orchestrator REST API URL")
    parser.add_argument("--dry-run", action="store_true", help="Print schedule without running")
    parser.add_argument("--only", help="Comma-separated list of device/suite pairs to run, e.g. fpga:error/boundaries,gpgpu:vishwa/regression/vecaddx")
    args = parser.parse_args()

    print("=== DVF Impact Analyzer ===")
    
    # 1. Load context
    registry = load_device_registry()
    sidecars = find_all_sidecars()
    
    # 2. Get changed files
    if args.only:
        affected = set()
        for pair in args.only.split(","):
            if ":" in pair:
                dev, suite = pair.split(":", 1)
                affected.add((dev.strip(), suite.strip()))
        print(f"Overridden execution to run only: {affected}")
    else:
        compare_ref = args.compare or "master"
        print(f"Comparing against ref: {compare_ref}")
        changed_files = get_changed_files(compare_ref)
        print(f"Found {len(changed_files)} changed files.")

        # If no files changed (e.g. manual CI trigger), default to all tests
        if not changed_files:
            print("No files changed. Scheduling all QEMU-capable tests.")
            affected = set()
            for dev in qemu_devices(registry):
                for suite in dev.get("test_suites", []):
                    affected.add((dev["id"], suite))
        else:
            affected = get_affected_tests(changed_files, registry, sidecars)

    if not affected:
        print("No validation tests affected. Exiting.")
        sys.exit(0)

    # 3. Resolve DAG
    execution_plan = build_dag(affected, sidecars)
    print(f"\nPlanned execution order ({len(execution_plan)} tasks):")
    for i, (dev, test) in enumerate(execution_plan, 1):
        print(f"  {i}. Device: {dev:10} | Test: {test}")

    if args.dry_run:
        print("\nDry-run complete.")
        sys.exit(0)

    # 4. Trigger Orchestration
    print(f"\nTriggering tests on Orchestrator API at {args.api}...")
    
    failed_tests = set()
    skipped_tests = set()
    
    for dev, test in execution_plan:
        # Check if any dependencies failed for this node
        meta = sidecars.get(test)
        has_failed_dep = False
        if meta:
            for dep in meta.get("dependencies", []):
                if (dev, dep) in failed_tests or (dev, dep) in skipped_tests:
                    has_failed_dep = True
                    break
        
        if has_failed_dep:
            print(f"[-] SKIPPED: {dev}/{test} (dependency failed)")
            skipped_tests.add((dev, test))
            continue

        print(f"[>] Running: {dev}/{test} ... ", end="", flush=True)
        run_id = submit_test_run(args.api, dev, test)
        if not run_id:
            print("SUBMISSION FAILED")
            failed_tests.add((dev, test))
            continue
        
        # Poll for completion
        completed = False
        status = "PENDING"
        err_msg = ""
        while not completed:
            time.sleep(3)
            status, err_msg = get_test_run_status(args.api, run_id)
            if status in ["TEST_RUN_STATUS_PASSED", "TEST_RUN_STATUS_FAILED", "TEST_RUN_STATUS_ERRORED", "TEST_RUN_STATUS_CANCELLED", "TEST_RUN_STATUS_TIMEOUT"]:
                completed = True
        
        print(status.replace("TEST_RUN_STATUS_", ""))
        if status == "TEST_RUN_STATUS_CANCELLED":
            skipped_tests.add((dev, test))
        elif status != "TEST_RUN_STATUS_PASSED":
            if err_msg:
                print(f"    Error: {err_msg}")
            failed_tests.add((dev, test))

    # Summary
    print("\n=== Execution Summary ===")
    print(f"  Passed:  {len(execution_plan) - len(failed_tests) - len(skipped_tests)}")
    print(f"  Failed:  {len(failed_tests)}")
    print(f"  Skipped: {len(skipped_tests)}")
    
    if failed_tests:
        sys.exit(1)
    sys.exit(0)

if __name__ == "__main__":
    main()
