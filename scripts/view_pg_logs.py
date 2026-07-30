#!/usr/bin/env python3
"""
DVF PostgreSQL Log & Test Result Viewer.

Usage:
  python3 scripts/view_pg_logs.py                 # View recent test runs
  python3 scripts/view_pg_logs.py --failed        # View only failed test runs
  python3 scripts/view_pg_logs.py --run-id <ID>   # View full logs for a specific run ID
  python3 scripts/view_pg_logs.py --json          # Output in raw JSON format
"""

import sys
import json
import argparse
import subprocess

PG_CONTAINER = "dvf-postgres"
PG_USER = "dvf"
PG_DB = "dvf_validation"

def run_psql(query):
    """Execute a SQL query inside the dvf-postgres container or via psql."""
    # 1. Try podman exec
    cmd = ["podman", "exec", "-i", PG_CONTAINER, "psql", "-U", PG_USER, "-d", PG_DB, "-c", query]
    try:
        res = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
        if res.returncode == 0:
            return res.stdout
    except Exception:
        pass

    # 2. Try local psql command
    cmd_local = ["psql", "-h", "localhost", "-U", PG_USER, "-d", PG_DB, "-c", query]
    try:
        res = subprocess.run(cmd_local, capture_output=True, text=True, timeout=10)
        if res.returncode == 0:
            return res.stdout
    except Exception:
        pass

    return None

def query_json(query):
    """Execute a SQL query and parse result as JSON array of dicts."""
    json_sql = f"SELECT json_agg(t) FROM ({query}) t;"
    raw = run_psql(json_sql)
    if not raw:
        return []
    
    # Extract JSON blob from psql output
    lines = [line.strip() for line in raw.splitlines() if line.strip() and not line.startswith("-") and not line.startswith("(")]
    for line in lines:
        if line.startswith("[") and line.endswith("]"):
            try:
                return json.loads(line)
            except Exception:
                pass
    return []

def list_runs(failed_only=False, json_output=False):
    where_clause = "WHERE status IN ('FAILED', 'ERRORED', 'CANCELLED', 'TIMEOUT')" if failed_only else ""
    sql = f"""
        SELECT id, device_id, test_suite_id, status, duration_ms, 
               to_char(created_at, 'YYYY-MM-DD HH24:MI:SS') as timestamp, 
               COALESCE(error_message, '') as error
        FROM test_runs 
        {where_clause}
        ORDER BY created_at DESC 
        LIMIT 25
    """
    
    data = query_json(sql)
    if json_output:
        print(json.dumps(data, indent=2))
        return

    if not data:
        print("No test runs found in PostgreSQL database (or database is offline).")
        print("\nNote: Make sure the PostgreSQL container is running:")
        print("  podman start dvf-postgres")
        return

    print(f"\n{'ID':<38} | {'DEVICE':<10} | {'SUITE':<30} | {'STATUS':<10} | {'DURATION':<10} | {'TIMESTAMP'}")
    print("-" * 120)
    for row in data:
        status_str = row.get('status', 'UNKNOWN')
        # ANSI colors for status
        if status_str == 'PASSED':
            status_colored = f"\033[92m{status_str:<10}\033[0m"
        elif status_str in ('FAILED', 'ERRORED'):
            status_colored = f"\033[91m{status_str:<10}\033[0m"
        else:
            status_colored = f"\033[93m{status_str:<10}\033[0m"

        dur = f"{row.get('duration_ms', 0)}ms"
        print(f"{row.get('id', ''):<38} | {row.get('device_id', ''):<10} | {row.get('test_suite_id', ''):<30} | {status_colored} | {dur:<10} | {row.get('timestamp', '')}")
        if row.get('error'):
            print(f"   └── Error: {row.get('error')}")

def view_run_details(run_id, json_output=False):
    sql_run = f"""
        SELECT id, device_id, test_suite_id, status, duration_ms, 
               to_char(created_at, 'YYYY-MM-DD HH24:MI:SS') as timestamp, 
               COALESCE(error_message, '') as error, requested_by
        FROM test_runs WHERE id = '{run_id}'
    """
    run_data = query_json(sql_run)
    if not run_data:
        print(f"Test run '{run_id}' not found.")
        return

    sql_results = f"""
        SELECT test_name, category, status, duration_ms, 
               COALESCE(message, '') as message, COALESCE(logs, '') as logs
        FROM test_results WHERE test_run_id = '{run_id}'
        ORDER BY completed_at ASC
    """
    results_data = query_json(sql_results)

    if json_output:
        out = {"run": run_data[0], "results": results_data}
        print(json.dumps(out, indent=2))
        return

    run = run_data[0]
    print(f"\n=== Test Run Details: {run['id']} ===")
    print(f"Device:       {run['device_id']}")
    print(f"Test Suite:   {run['test_suite_id']}")
    print(f"Status:       {run['status']}")
    print(f"Duration:     {run['duration_ms']} ms")
    print(f"Timestamp:    {run['timestamp']}")
    if run.get('error'):
        print(f"Error:        {run['error']}")

    if results_data:
        print(f"\n--- Sub-test Results ({len(results_data)} cases) ---")
        for res in results_data:
            st = res.get('status', '')
            print(f"\n  • [{st}] {res.get('test_name', '')} ({res.get('duration_ms', 0)}ms)")
            if res.get('message'):
                print(f"    Message: {res['message']}")
            if res.get('logs'):
                print("    Logs:")
                for l in res['logs'].splitlines():
                    print(f"      {l}")

def main():
    parser = argparse.ArgumentParser(description="DVF PostgreSQL Test Log & Result Viewer")
    parser.add_argument("--failed", action="store_true", help="Filter and display only failed test runs")
    parser.add_argument("--run-id", type=str, help="Display detailed results and logs for a specific test run ID")
    parser.add_argument("--json", action="store_true", help="Output results in raw JSON format")
    args = parser.parse_args()

    if args.run_id:
        view_run_details(args.run_id, json_output=args.json)
    else:
        list_runs(failed_only=args.failed, json_output=args.json)

if __name__ == "__main__":
    main()
