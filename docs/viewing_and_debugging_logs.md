# Viewing & Debugging Logs in DVF

This document provides a comprehensive guide on how to locate, view, query, and debug logs within the Driver Validation Framework (DVF).

---

## 1. DVF Logging Architecture

DVF records logs across three primary layers:

| Layer | Source | Location / Storage | Format |
|---|---|---|---|
| **Orchestrator & Test Runner** | Orchestrator API & REST engine | PostgreSQL DB (`dvf_validation`) / `orchestrator.log` | Structured JSON / SQL |
| **QEMU Guest VM Console** | Guest OS kernel (`dmesg`), serial output, & agent | Host `/tmp/dvf/qemu-vm-*.log` & CI artifacts `logs/qemu/` | Plain text |
| **Test Case Results** | Sub-test assertions, error messages, and metrics | PostgreSQL `test_results` table & REST API | Structured JSON / Text |

---

## 2. Viewing Logs via PostgreSQL CLI (`scripts/view_pg_logs.py`)

DVF includes a dedicated CLI log viewer tool located at `scripts/view_pg_logs.py`.

### Prerequisites
Ensure the PostgreSQL container is running:
```bash
podman start dvf-postgres
```

### Usage Commands

#### A. View All Recent Test Runs
Displays a color-coded table of the 25 most recent test executions (Status, Device, Test Suite, Duration, Timestamp, and error summaries):
```bash
python3 scripts/view_pg_logs.py
```

#### B. Filter Only Failed Test Runs
Displays only test runs that resulted in `FAILED`, `ERRORED`, `CANCELLED`, or `TIMEOUT`:
```bash
python3 scripts/view_pg_logs.py --failed
```

#### C. View Detailed Logs for a Specific Test Run
Pass the specific Test Run ID (e.g. `tr-1785392521-2` or UUID) to view all individual test case assertions, messages, and embedded log snippets:
```bash
python3 scripts/view_pg_logs.py --run-id <RUN_ID>
```

#### D. Export Logs as JSON
Output raw JSON for programmatic inspection or integration:
```bash
python3 scripts/view_pg_logs.py --json
```

---

## 3. Direct SQL Queries on PostgreSQL

You can connect directly to PostgreSQL to run custom queries against the validation database.

### Connecting to PostgreSQL
Via Podman container:
```bash
podman exec -it dvf-postgres psql -U dvf -d dvf_validation
```

Via local `psql` client:
```bash
psql -h localhost -p 5432 -U dvf -d dvf_validation
```

### Database Schema

#### `test_runs` Table
Tracks top-level test execution lifecycles:
```sql
SELECT id, device_id, test_suite_id, status, duration_ms, created_at, error_message
FROM test_runs
ORDER BY created_at DESC;
```

#### `test_results` Table
Tracks granular test case outcomes and logs for each run:
```sql
SELECT test_name, category, status, duration_ms, message, logs
FROM test_results
WHERE test_run_id = 'YOUR_TEST_RUN_ID'
ORDER BY completed_at ASC;
```

#### `vms` Table
Tracks active and historical QEMU VM instances:
```sql
SELECT id, status, device_id, qemu_device_name, pid, created_at, agent_status
FROM vms
ORDER BY created_at DESC;
```

### Useful SQL Queries

#### Find all failed test cases with their error messages:
```sql
SELECT tr.id AS run_id, tr.device_id, tr.test_suite_id, res.test_name, res.message, res.logs
FROM test_runs tr
JOIN test_results res ON tr.id = res.test_run_id
WHERE res.status = 'FAILED'
ORDER BY res.completed_at DESC;
```

#### Get execution summary by device:
```sql
SELECT device_id, status, COUNT(*) AS count
FROM test_runs
GROUP BY device_id, status;
```

---

## 4. Viewing Logs in GitLab CI/CD

When tests run within GitLab CI (`.gitlab-ci.yml`), orchestrator logs and QEMU console outputs are automatically captured as **Job Artifacts**.

### How to Access CI Artifacts
1. Go to your repository's **CI/CD → Pipelines** in GitLab.
2. Click on the target Pipeline and select the `test` stage job.
3. On the right sidebar, click **Job Artifacts** → **Browse** or **Download**.

### Artifact Files Included:
- `orchestrator.log`: Orchestrator API request trace, VM scheduling, and virtio-serial communication logs.
- `logs/qemu/qemu-vm-*.log`: Full console, kernel `dmesg`, and Python agent startup logs for each VM started during the job.

---

## 5. Viewing Real-Time Local Logs

When running tests or debugging locally on the host system:

### QEMU Guest Console Output
QEMU writes stdout/stderr for each VM instance to `/tmp/dvf/`:
```bash
# List all QEMU VM log files
ls -la /tmp/dvf/qemu-vm-*.log

# Tail the latest QEMU VM log in real time
tail -f $(ls -t /tmp/dvf/qemu-vm-*.log | head -1)
```

### Orchestrator Log Output
When running orchestrator locally, logs are written to stdout or to `orchestrator.log`:
```bash
tail -f orchestrator.log
```

---

## 6. Storage Configuration & Fallback Mechanism

DVF supports two storage backends for logs and state:
- **`postgres`**: Persistent SQL database (`dvf_validation`).
- **`memory`**: Ephemeral in-memory store for quick local testing.

In `.gitlab-ci.yml` and `global_config.json`, `--storage postgres` is enabled by default. If PostgreSQL is offline, `go-orchestrator` automatically outputs a warning and falls back to in-memory storage safely without failing the test run:

```text
WARN postgres unavailable — falling back to in-memory store {"dsn_host": "localhost"}
```
