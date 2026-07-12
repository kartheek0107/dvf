// Package storage provides a PostgreSQL-backed implementation of the Store interface.
//
// This implementation uses pgx/v5 (https://github.com/jackc/pgx) with a
// connection pool. On first connection it auto-migrates the schema via
// CREATE TABLE IF NOT EXISTS — no external migration tool required.
//
// DSN format (from config.PostgresConfig.DSN()):
//
//	postgres://user:password@host:port/database?sslmode=disable
package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/core"
)

// PostgresStore is a PostgreSQL-backed implementation of Store.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore connects to PostgreSQL, creates the pool, and ensures the
// schema is up-to-date. Returns a ready-to-use store.
func NewPostgresStore(ctx context.Context, dsn string, maxConns int) (*PostgresStore, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing postgres DSN: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = int32(maxConns)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating postgres pool: %w", err)
	}

	// Verify connectivity
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}

	s := &PostgresStore{pool: pool}
	if err := s.ensureSchema(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ensuring schema: %w", err)
	}

	return s, nil
}

// ensureSchema creates tables if they do not already exist.
// Idempotent — safe to call on every startup.
func (s *PostgresStore) ensureSchema(ctx context.Context) error {
	ddl := `
-- Enable pgcrypto for gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- test_runs: one row per test execution lifecycle
CREATE TABLE IF NOT EXISTS test_runs (
    id              TEXT        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    device_id       TEXT        NOT NULL,
    test_suite_id   TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'PENDING',
    vm_id           TEXT,
    priority        INTEGER     NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    duration_ms     BIGINT      NOT NULL DEFAULT 0,
    requested_by    TEXT,
    tags            JSONB       NOT NULL DEFAULT '[]',
    error_message   TEXT
);

CREATE INDEX IF NOT EXISTS idx_test_runs_status    ON test_runs (status);
CREATE INDEX IF NOT EXISTS idx_test_runs_device_id ON test_runs (device_id);
CREATE INDEX IF NOT EXISTS idx_test_runs_created_at ON test_runs (created_at DESC);

-- test_results: individual test case outcomes within a run
CREATE TABLE IF NOT EXISTS test_results (
    id              TEXT        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    test_run_id     TEXT        NOT NULL REFERENCES test_runs(id) ON DELETE CASCADE,
    test_name       TEXT        NOT NULL,
    category        TEXT        NOT NULL DEFAULT '',
    status          TEXT        NOT NULL,
    duration_ms     BIGINT      NOT NULL DEFAULT 0,
    message         TEXT,
    metrics         JSONB       NOT NULL DEFAULT '{}',
    logs            TEXT,
    completed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_test_results_run_id ON test_results (test_run_id);

-- vms: QEMU VM instance tracking
CREATE TABLE IF NOT EXISTS vms (
    id                   TEXT        PRIMARY KEY,
    status               TEXT        NOT NULL DEFAULT 'CREATING',
    device_id            TEXT        NOT NULL,
    qemu_device_name     TEXT,
    qmp_socket_path      TEXT,
    serial_ports         JSONB       NOT NULL DEFAULT '[]',
    pid                  INTEGER     NOT NULL DEFAULT 0,
    allocated_cpus       INTEGER     NOT NULL DEFAULT 0,
    allocated_mem_mb     INTEGER     NOT NULL DEFAULT 0,
    image_path           TEXT,
    overlay_path         TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    agent_status         TEXT        NOT NULL DEFAULT 'UNKNOWN',
    last_heartbeat       TIMESTAMPTZ,
    current_test_run_id  TEXT
);

CREATE INDEX IF NOT EXISTS idx_vms_status ON vms (status);
`
	_, err := s.pool.Exec(ctx, ddl)
	return err
}

// ─── TestRunStore ────────────────────────────────────────────────────────────

func (s *PostgresStore) CreateTestRun(ctx context.Context, run *core.TestRun) (*core.TestRun, error) {
	tagsJSON, err := json.Marshal(run.Tags)
	if err != nil {
		tagsJSON = []byte("[]")
	}

	const q = `
INSERT INTO test_runs
    (device_id, test_suite_id, status, vm_id, priority,
     created_at, requested_by, tags, error_message)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, created_at`

	now := time.Now().UTC()
	if run.Status == "" {
		run.Status = core.TestRunStatusPending
	}

	row := s.pool.QueryRow(ctx, q,
		run.DeviceID, run.TestSuiteID, string(run.Status),
		nullableStr(run.VMID), run.Priority, now,
		nullableStr(run.RequestedBy), tagsJSON,
		nullableStr(run.ErrorMessage),
	)

	if err := row.Scan(&run.ID, &run.CreatedAt); err != nil {
		return nil, fmt.Errorf("inserting test run: %w", err)
	}
	return run, nil
}

func (s *PostgresStore) GetTestRun(ctx context.Context, id string) (*core.TestRun, error) {
	const q = `
SELECT id, device_id, test_suite_id, status, COALESCE(vm_id,''),
       priority, created_at, started_at, completed_at, duration_ms,
       COALESCE(requested_by,''), tags, COALESCE(error_message,'')
FROM test_runs WHERE id = $1`

	run := &core.TestRun{}
	var tags []byte

	row := s.pool.QueryRow(ctx, q, id)
	err := row.Scan(
		&run.ID, &run.DeviceID, &run.TestSuiteID, &run.Status, &run.VMID,
		&run.Priority, &run.CreatedAt, &run.StartedAt, &run.CompletedAt, &run.DurationMs,
		&run.RequestedBy, &tags, &run.ErrorMessage,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("test run %q not found", id)
		}
		return nil, fmt.Errorf("querying test run: %w", err)
	}

	if len(tags) > 0 {
		_ = json.Unmarshal(tags, &run.Tags)
	}
	return run, nil
}

func (s *PostgresStore) UpdateTestRunStatus(ctx context.Context, id string, status core.TestRunStatus, errMsg string) error {
	now := time.Now().UTC()

	var q string
	var args []interface{}

	switch status {
	case core.TestRunStatusRunning:
		q = `UPDATE test_runs SET status=$1, started_at=$2, error_message=NULLIF($3,'') WHERE id=$4`
		args = []interface{}{string(status), now, errMsg, id}
	case core.TestRunStatusPassed, core.TestRunStatusFailed,
		core.TestRunStatusErrored, core.TestRunStatusCancelled,
		core.TestRunStatusTimeout:
		q = `UPDATE test_runs
             SET status=$1, completed_at=$2,
                 duration_ms = EXTRACT(EPOCH FROM ($2 - COALESCE(started_at, created_at))) * 1000,
                 error_message=NULLIF($3,'')
             WHERE id=$4`
		args = []interface{}{string(status), now, errMsg, id}
	default:
		q = `UPDATE test_runs SET status=$1, error_message=NULLIF($2,'') WHERE id=$3`
		args = []interface{}{string(status), errMsg, id}
	}

	tag, err := s.pool.Exec(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("updating test run status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("test run %q not found", id)
	}
	return nil
}

func (s *PostgresStore) ListTestRuns(ctx context.Context, req *core.ListTestRunsRequest) ([]*core.TestRun, error) {
	q := `SELECT id, device_id, test_suite_id, status, COALESCE(vm_id,''),
               priority, created_at, started_at, completed_at, duration_ms,
               COALESCE(requested_by,''), tags, COALESCE(error_message,'')
          FROM test_runs WHERE 1=1`
	args := []interface{}{}
	argN := 1

	if req.DeviceID != "" {
		q += fmt.Sprintf(" AND device_id=$%d", argN)
		args = append(args, req.DeviceID)
		argN++
	}
	if req.Status != "" {
		q += fmt.Sprintf(" AND status=$%d", argN)
		args = append(args, string(req.Status))
		argN++
	}

	q += " ORDER BY created_at DESC"

	if req.Limit > 0 {
		q += fmt.Sprintf(" LIMIT $%d", argN)
		args = append(args, req.Limit)
		argN++
	}
	if req.Offset > 0 {
		q += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, req.Offset)
	}

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing test runs: %w", err)
	}
	defer rows.Close()

	var runs []*core.TestRun
	for rows.Next() {
		run := &core.TestRun{}
		var tags []byte
		if err := rows.Scan(
			&run.ID, &run.DeviceID, &run.TestSuiteID, &run.Status, &run.VMID,
			&run.Priority, &run.CreatedAt, &run.StartedAt, &run.CompletedAt, &run.DurationMs,
			&run.RequestedBy, &tags, &run.ErrorMessage,
		); err != nil {
			return nil, fmt.Errorf("scanning test run row: %w", err)
		}
		if len(tags) > 0 {
			_ = json.Unmarshal(tags, &run.Tags)
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// ─── TestResultStore ─────────────────────────────────────────────────────────

func (s *PostgresStore) SaveTestResult(ctx context.Context, result *core.TestResult) error {
	metricsJSON, err := json.Marshal(result.Metrics)
	if err != nil {
		metricsJSON = []byte("{}")
	}

	const q = `
INSERT INTO test_results
    (test_run_id, test_name, category, status, duration_ms,
     message, metrics, logs, completed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id`

	if result.CompletedAt.IsZero() {
		result.CompletedAt = time.Now().UTC()
	}

	return s.pool.QueryRow(ctx, q,
		result.TestRunID, result.TestName, string(result.Category),
		string(result.Status), result.DurationMs,
		nullableStr(result.Message), metricsJSON,
		nullableStr(result.Logs), result.CompletedAt,
	).Scan(&result.ID)
}

func (s *PostgresStore) GetTestResults(ctx context.Context, testRunID string) ([]*core.TestResult, error) {
	const q = `
SELECT id, test_run_id, test_name, category, status, duration_ms,
       COALESCE(message,''), metrics, COALESCE(logs,''), completed_at
FROM test_results WHERE test_run_id=$1 ORDER BY completed_at`

	rows, err := s.pool.Query(ctx, q, testRunID)
	if err != nil {
		return nil, fmt.Errorf("querying test results: %w", err)
	}
	defer rows.Close()

	var results []*core.TestResult
	for rows.Next() {
		r := &core.TestResult{}
		var metricsJSON []byte
		if err := rows.Scan(
			&r.ID, &r.TestRunID, &r.TestName, &r.Category, &r.Status,
			&r.DurationMs, &r.Message, &metricsJSON, &r.Logs, &r.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning test result: %w", err)
		}
		if len(metricsJSON) > 0 {
			_ = json.Unmarshal(metricsJSON, &r.Metrics)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ─── VMStore ─────────────────────────────────────────────────────────────────

func (s *PostgresStore) SaveVM(ctx context.Context, vm *core.VMInstance) error {
	portsJSON, err := json.Marshal(vm.SerialPorts)
	if err != nil {
		portsJSON = []byte("[]")
	}

	const q = `
INSERT INTO vms
    (id, status, device_id, qemu_device_name, qmp_socket_path,
     serial_ports, pid, allocated_cpus, allocated_mem_mb,
     image_path, overlay_path, created_at, agent_status,
     last_heartbeat, current_test_run_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
ON CONFLICT (id) DO UPDATE SET
    status               = EXCLUDED.status,
    qmp_socket_path      = EXCLUDED.qmp_socket_path,
    serial_ports         = EXCLUDED.serial_ports,
    pid                  = EXCLUDED.pid,
    agent_status         = EXCLUDED.agent_status,
    last_heartbeat       = EXCLUDED.last_heartbeat,
    current_test_run_id  = EXCLUDED.current_test_run_id`

	if vm.CreatedAt.IsZero() {
		vm.CreatedAt = time.Now().UTC()
	}

	_, err = s.pool.Exec(ctx, q,
		vm.ID, string(vm.Status), vm.DeviceID,
		nullableStr(vm.QEMUDeviceName), nullableStr(vm.QMPSocketPath),
		portsJSON, vm.PID, vm.AllocatedCPUs, vm.AllocatedMemMB,
		nullableStr(vm.ImagePath), nullableStr(vm.OverlayPath), vm.CreatedAt,
		string(vm.AgentStatus), vm.LastHeartbeat,
		nullableStr(vm.CurrentTestRunID),
	)
	return err
}

func (s *PostgresStore) GetVM(ctx context.Context, id string) (*core.VMInstance, error) {
	const q = `
SELECT id, status, device_id, COALESCE(qemu_device_name,''),
       COALESCE(qmp_socket_path,''), serial_ports,
       pid, allocated_cpus, allocated_mem_mb,
       COALESCE(image_path,''), COALESCE(overlay_path,''),
       created_at, agent_status, last_heartbeat,
       COALESCE(current_test_run_id,'')
FROM vms WHERE id=$1`

	vm := &core.VMInstance{}
	var portsJSON []byte

	err := s.pool.QueryRow(ctx, q, id).Scan(
		&vm.ID, &vm.Status, &vm.DeviceID, &vm.QEMUDeviceName,
		&vm.QMPSocketPath, &portsJSON,
		&vm.PID, &vm.AllocatedCPUs, &vm.AllocatedMemMB,
		&vm.ImagePath, &vm.OverlayPath,
		&vm.CreatedAt, &vm.AgentStatus, &vm.LastHeartbeat,
		&vm.CurrentTestRunID,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("vm %q not found", id)
		}
		return nil, fmt.Errorf("querying vm: %w", err)
	}

	if len(portsJSON) > 0 {
		_ = json.Unmarshal(portsJSON, &vm.SerialPorts)
	}
	return vm, nil
}

func (s *PostgresStore) UpdateVMStatus(ctx context.Context, id string, status core.VMStatus) error {
	const q = `UPDATE vms SET status=$1 WHERE id=$2`
	tag, err := s.pool.Exec(ctx, q, string(status), id)
	if err != nil {
		return fmt.Errorf("updating vm status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("vm %q not found", id)
	}
	return nil
}

func (s *PostgresStore) ListVMs(ctx context.Context, req *core.ListVMsRequest) ([]*core.VMInstance, error) {
	q := `SELECT id, status, device_id, COALESCE(qemu_device_name,''),
               COALESCE(qmp_socket_path,''), serial_ports,
               pid, allocated_cpus, allocated_mem_mb,
               COALESCE(image_path,''), COALESCE(overlay_path,''),
               created_at, agent_status, last_heartbeat,
               COALESCE(current_test_run_id,'')
          FROM vms WHERE 1=1`
	args := []interface{}{}
	argN := 1

	if req.Status != "" {
		q += fmt.Sprintf(" AND status=$%d", argN)
		args = append(args, string(req.Status))
		argN++
	}
	if req.DeviceID != "" {
		q += fmt.Sprintf(" AND device_id=$%d", argN)
		args = append(args, req.DeviceID)
	}
	q += " ORDER BY created_at DESC"

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing vms: %w", err)
	}
	defer rows.Close()

	var vms []*core.VMInstance
	for rows.Next() {
		vm := &core.VMInstance{}
		var portsJSON []byte
		if err := rows.Scan(
			&vm.ID, &vm.Status, &vm.DeviceID, &vm.QEMUDeviceName,
			&vm.QMPSocketPath, &portsJSON,
			&vm.PID, &vm.AllocatedCPUs, &vm.AllocatedMemMB,
			&vm.ImagePath, &vm.OverlayPath,
			&vm.CreatedAt, &vm.AgentStatus, &vm.LastHeartbeat,
			&vm.CurrentTestRunID,
		); err != nil {
			return nil, fmt.Errorf("scanning vm row: %w", err)
		}
		if len(portsJSON) > 0 {
			_ = json.Unmarshal(portsJSON, &vm.SerialPorts)
		}
		vms = append(vms, vm)
	}
	return vms, rows.Err()
}

func (s *PostgresStore) DeleteVM(ctx context.Context, id string) error {
	const q = `DELETE FROM vms WHERE id=$1`
	_, err := s.pool.Exec(ctx, q, id)
	return err
}

// ListRunningTestRuns returns all test runs in non-terminal states.
// Used by RecoverState on startup to reconcile in-flight work.
func (s *PostgresStore) ListRunningTestRuns(ctx context.Context) ([]*core.TestRun, error) {
	const q = `
SELECT id, device_id, test_suite_id, status, COALESCE(vm_id,''),
       priority, created_at, started_at, completed_at, duration_ms,
       COALESCE(requested_by,''), tags, COALESCE(error_message,'')
FROM test_runs
WHERE status IN ('RUNNING','QUEUED','PENDING')
ORDER BY created_at`

	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("listing running test runs: %w", err)
	}
	defer rows.Close()

	var runs []*core.TestRun
	for rows.Next() {
		run := &core.TestRun{}
		var tags []byte
		if err := rows.Scan(
			&run.ID, &run.DeviceID, &run.TestSuiteID, &run.Status, &run.VMID,
			&run.Priority, &run.CreatedAt, &run.StartedAt, &run.CompletedAt, &run.DurationMs,
			&run.RequestedBy, &tags, &run.ErrorMessage,
		); err != nil {
			return nil, fmt.Errorf("scanning test run: %w", err)
		}
		if len(tags) > 0 {
			_ = json.Unmarshal(tags, &run.Tags)
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// ListOrphanedVMs returns VMs in transient states that were never torn down.
func (s *PostgresStore) ListOrphanedVMs(ctx context.Context) ([]*core.VMInstance, error) {
	const q = `
SELECT id, status, device_id, COALESCE(qemu_device_name,''),
       COALESCE(qmp_socket_path,''), serial_ports,
       pid, allocated_cpus, allocated_mem_mb,
       COALESCE(image_path,''), COALESCE(overlay_path,''),
       created_at, agent_status, last_heartbeat,
       COALESCE(current_test_run_id,'')
FROM vms
WHERE status IN ('CREATING','BOOTING','RUNNING_TEST')
ORDER BY created_at`

	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("listing orphaned vms: %w", err)
	}
	defer rows.Close()

	var vms []*core.VMInstance
	for rows.Next() {
		vm := &core.VMInstance{}
		var portsJSON []byte
		if err := rows.Scan(
			&vm.ID, &vm.Status, &vm.DeviceID, &vm.QEMUDeviceName,
			&vm.QMPSocketPath, &portsJSON,
			&vm.PID, &vm.AllocatedCPUs, &vm.AllocatedMemMB,
			&vm.ImagePath, &vm.OverlayPath,
			&vm.CreatedAt, &vm.AgentStatus, &vm.LastHeartbeat,
			&vm.CurrentTestRunID,
		); err != nil {
			return nil, fmt.Errorf("scanning orphaned vm: %w", err)
		}
		if len(portsJSON) > 0 {
			_ = json.Unmarshal(portsJSON, &vm.SerialPorts)
		}
		vms = append(vms, vm)
	}
	return vms, rows.Err()
}

// ─── Utility ─────────────────────────────────────────────────────────────────

func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *PostgresStore) Close() error {
	s.pool.Close()
	return nil
}

// nullableStr converts an empty string to nil for nullable TEXT columns.
func nullableStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
