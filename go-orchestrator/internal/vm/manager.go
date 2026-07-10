// Package vm provides QEMU virtual machine management for the DVF orchestrator.
//
// This file implements the VM Manager — responsible for the full lifecycle
// of QEMU virtual machines: building command lines, starting/stopping processes,
// connecting QMP clients, and tracking state.
package vm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/config"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/core"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/storage"
)

// VMConfig holds all parameters needed to create a QEMU virtual machine.
type VMConfig struct {
	// Device under test
	DeviceEntry *config.DeviceEntry

	// Resource overrides (0 = use defaults from global config)
	MemoryMB int
	CPUs     int

	// Paths (empty = use defaults from global config)
	KernelPath string
	RootFSPath string
	ShareDir   string
	GuestMount string
}

// liveVM tracks a running QEMU process and its associated QMP client.
type liveVM struct {
	process   *os.Process
	qmpClient *QMPClient
	instance  *core.VMInstance
}

// VMManager manages the lifecycle of QEMU virtual machines.
type VMManager struct {
	cfg      *config.GlobalConfig
	store    storage.Store
	logger   *zap.Logger

	mu      sync.RWMutex
	liveVMs map[string]*liveVM // key: VM ID

	counter int64 // for generating unique IDs
}

// NewVMManager creates a new VM Manager.
func NewVMManager(cfg *config.GlobalConfig, store storage.Store, logger *zap.Logger) *VMManager {
	return &VMManager{
		cfg:     cfg,
		store:   store,
		logger:  logger,
		liveVMs: make(map[string]*liveVM),
	}
}

// nextID generates a unique VM identifier.
func (m *VMManager) nextID() string {
	m.mu.Lock()
	m.counter++
	id := fmt.Sprintf("vm-%d-%d", time.Now().Unix(), m.counter)
	m.mu.Unlock()
	return id
}

// BuildQEMUArgs constructs the QEMU command-line arguments for a VM.
// This is exported for testing — it does not start anything.
func (m *VMManager) BuildQEMUArgs(vmID string, vmCfg *VMConfig) []string {
	qCfg := m.cfg.QEMU

	memMB := vmCfg.MemoryMB
	if memMB == 0 {
		memMB = qCfg.DefaultMemoryMB
	}
	cpus := vmCfg.CPUs
	if cpus == 0 {
		cpus = qCfg.DefaultCPUs
	}
	kernelPath := vmCfg.KernelPath
	if kernelPath == "" {
		kernelPath = qCfg.KernelPath
	}
	rootfsPath := vmCfg.RootFSPath
	if rootfsPath == "" {
		rootfsPath = qCfg.RootFSPath
	}
	shareDir := vmCfg.ShareDir
	if shareDir == "" {
		shareDir = qCfg.ShareDir
	}
	guestMount := vmCfg.GuestMount
	if guestMount == "" {
		guestMount = qCfg.GuestMount
	}
	if guestMount == "" {
		guestMount = "/mnt/share"
	}

	// QMP socket path
	socketDir := qCfg.SocketDir
	if socketDir == "" {
		socketDir = "/tmp/dvf/qmp"
	}
	qmpSocket := filepath.Join(socketDir, vmID+".sock")

	// virtio-serial socket: host side is a Unix socket the orchestrator reads/writes;
	// guest side is /dev/virtio-ports/dvf.agent.0
	agentSocket := filepath.Join("/tmp/dvf/agent", vmID+".sock")

	args := []string{
		// Kernel + rootfs.
		// -snapshot opens the image read-only and uses an in-memory
		// temp overlay for all writes. This allows multiple VMs to share
		// the same rootfs.ext4 simultaneously without write-lock conflicts.
		"-kernel", kernelPath,
		"-drive", fmt.Sprintf("file=%s,format=raw,if=virtio", rootfsPath),
		"-snapshot",
		// Pass vm_id on cmdline so the agent can self-identify without networking
		"-append", fmt.Sprintf("root=/dev/vda console=ttyS0 rw init=/bin/bash dvf_vm_id=%s", vmID),


		// Resources
		"-m", strconv.Itoa(memMB),
		"-smp", strconv.Itoa(cpus),

		// Display
		"-nographic",

		// 9p virtio share (host ↔ guest file sharing)
		"-virtfs", fmt.Sprintf("local,path=%s,mount_tag=hostshare,security_model=mapped,id=hostshare", shareDir),

		// QMP control socket
		"-qmp", fmt.Sprintf("unix:%s,server,wait=off", qmpSocket),

		// virtio-serial bus + agent channel
		// ponytail: single chardev per VM; upgrade to multi-port if needed
		"-chardev", fmt.Sprintf("socket,id=agent-chr,path=%s,server=on,wait=off", agentSocket),
		"-device", "virtio-serial-pci",
		"-device", "virtserialport,chardev=agent-chr,name=dvf.agent.0",
	}

	// Device under test
	if vmCfg.DeviceEntry != nil {
		args = append(args, "-device", vmCfg.DeviceEntry.QEMUDeviceName)
	}

	return args
}

// CreateVM creates a new VM record in the store and returns it.
// The VM is not started yet — call StartVM() for that.
func (m *VMManager) CreateVM(ctx context.Context, vmCfg *VMConfig) (*core.VMInstance, error) {
	vmID := m.nextID()
	qCfg := m.cfg.QEMU

	memMB := vmCfg.MemoryMB
	if memMB == 0 {
		memMB = qCfg.DefaultMemoryMB
	}
	cpus := vmCfg.CPUs
	if cpus == 0 {
		cpus = qCfg.DefaultCPUs
	}

	socketDir := qCfg.SocketDir
	if socketDir == "" {
		socketDir = "/tmp/dvf/qmp"
	}
	qmpSocket := filepath.Join(socketDir, vmID+".sock")

	deviceID := ""
	qemuDeviceName := ""
	imagePath := vmCfg.RootFSPath
	if imagePath == "" {
		imagePath = qCfg.RootFSPath
	}
	if vmCfg.DeviceEntry != nil {
		deviceID = vmCfg.DeviceEntry.ID
		qemuDeviceName = vmCfg.DeviceEntry.QEMUDeviceName
	}

	instance := &core.VMInstance{
		ID:             vmID,
		Status:         core.VMStatusCreating,
		DeviceID:       deviceID,
		QEMUDeviceName: qemuDeviceName,
		QMPSocketPath:  qmpSocket,
		AllocatedCPUs:  cpus,
		AllocatedMemMB: memMB,
		ImagePath:      imagePath,
		CreatedAt:      time.Now().UTC(),
		AgentStatus:    core.AgentStatusUnknown,
	}

	if err := m.store.SaveVM(ctx, instance); err != nil {
		return nil, fmt.Errorf("saving VM record: %w", err)
	}

	m.logger.Info("VM created",
		zap.String("vm_id", vmID),
		zap.String("device", deviceID),
		zap.Int("cpus", cpus),
		zap.Int("memory_mb", memMB),
	)

	return instance, nil
}

// StartVM starts the QEMU process for a previously created VM and
// establishes the QMP connection.
func (m *VMManager) StartVM(ctx context.Context, vmID string) error {
	// Retrieve the VM record
	instance, err := m.store.GetVM(ctx, vmID)
	if err != nil {
		return fmt.Errorf("getting VM %s: %w", vmID, err)
	}

	// Ensure socket directories exist (QMP + agent) with 0777 permissions
	socketDir := filepath.Dir(instance.QMPSocketPath)
	if err := os.MkdirAll(socketDir, 0777); err != nil {
		return fmt.Errorf("creating socket dir %s: %w", socketDir, err)
	}
	os.Chmod(socketDir, 0777)

	if err := os.MkdirAll("/tmp/dvf/agent", 0777); err != nil {
		return fmt.Errorf("creating agent socket dir: %w", err)
	}
	os.Chmod("/tmp/dvf/agent", 0777)
	os.Chmod("/tmp/dvf", 0777)


	// Clean up stale socket
	os.Remove(instance.QMPSocketPath)

	// Look up device entry if we have a device ID
	var vmCfg VMConfig
	if instance.QEMUDeviceName != "" {
		// Use the stored QEMU device name (e.g. "gp_gpu"), NOT DeviceID
		// (e.g. "gpgpu") — they differ and QEMU only knows the model name.
		vmCfg.DeviceEntry = &config.DeviceEntry{
			QEMUDeviceName: instance.QEMUDeviceName,
		}
	}
	vmCfg.MemoryMB = instance.AllocatedMemMB
	vmCfg.CPUs = instance.AllocatedCPUs

	// Build QEMU command
	args := m.BuildQEMUArgs(vmID, &vmCfg)
	qemuBin := m.cfg.QEMU.BinaryPath

	m.logger.Info("starting QEMU",
		zap.String("vm_id", vmID),
		zap.String("binary", qemuBin),
		zap.Int("arg_count", len(args)),
	)

	// Start the process, capturing stderr so startup errors are visible.
	// Output goes to both a per-VM log file and an in-memory buffer.
	qemuLogPath := filepath.Join("/tmp/dvf", "qemu-"+vmID+".log")
	logFile, _ := os.OpenFile(qemuLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)

	var stderrBuf bytes.Buffer
	var stderrWriter io.Writer = &stderrBuf
	if logFile != nil {
		stderrWriter = io.MultiWriter(logFile, &stderrBuf)
	}

	cmd := exec.CommandContext(ctx, qemuBin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // New process group so we can kill it cleanly
	}
	cmd.Stderr = stderrWriter
	cmd.Stdout = stderrWriter // QEMU prints some startup info to stdout too

	m.logger.Info("QEMU command line",
		zap.String("vm_id", vmID),
		zap.String("log", qemuLogPath),
		zap.Strings("args", append([]string{qemuBin}, args...)),
	)

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		m.store.UpdateVMStatus(ctx, vmID, core.VMStatusError)
		return fmt.Errorf("starting QEMU process: %w", err)
	}
	if logFile != nil {
		go func() { cmd.Wait(); logFile.Close() }()
	}

	// Update status → BOOTING
	m.store.UpdateVMStatus(ctx, vmID, core.VMStatusBooting)
	instance.PID = cmd.Process.Pid

	m.logger.Info("QEMU process started",
		zap.String("vm_id", vmID),
		zap.Int("pid", cmd.Process.Pid),
		zap.String("log_file", qemuLogPath),
	)

	// Wait for the QMP socket to appear, then connect
	qmpClient := NewQMPClient(instance.QMPSocketPath, m.logger.Named("qmp"))

	qmpCtx, qmpCancel := context.WithTimeout(ctx, time.Duration(m.cfg.VMDefaults.BootTimeoutSeconds)*time.Second)
	defer qmpCancel()

	// Retry connecting to QMP — the socket may take a moment to appear
	var qmpErr error
	for i := 0; i < 30; i++ {
		qmpErr = qmpClient.Connect(qmpCtx)
		if qmpErr == nil {
			break
		}
		select {
		case <-qmpCtx.Done():
			m.store.UpdateVMStatus(ctx, vmID, core.VMStatusError)
			return fmt.Errorf("QMP connect timeout for VM %s: %w", vmID, qmpCtx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	if qmpErr != nil {
		m.store.UpdateVMStatus(ctx, vmID, core.VMStatusError)
		return fmt.Errorf("connecting QMP for VM %s: %w", vmID, qmpErr)
	}

	// Store live VM
	m.mu.Lock()
	m.liveVMs[vmID] = &liveVM{
		process:   cmd.Process,
		qmpClient: qmpClient,
		instance:  instance,
	}
	m.mu.Unlock()

	// Wait for QEMU to report running status
	status, err := qmpClient.QueryStatus(qmpCtx)
	if err != nil {
		m.logger.Warn("could not query VM status", zap.String("vm_id", vmID), zap.Error(err))
	} else if status.Running {
		m.store.UpdateVMStatus(ctx, vmID, core.VMStatusReady)
		m.logger.Info("VM is running", zap.String("vm_id", vmID))
	}

	return nil
}

// StopVM gracefully stops a running VM using QMP system_powerdown,
// with a fallback to SIGTERM and then SIGKILL.
func (m *VMManager) StopVM(ctx context.Context, vmID string) error {
	m.mu.RLock()
	live, ok := m.liveVMs[vmID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("VM %s is not running", vmID)
	}

	m.store.UpdateVMStatus(ctx, vmID, core.VMStatusStopping)
	m.logger.Info("stopping VM", zap.String("vm_id", vmID))

	// Try graceful shutdown via QMP
	if live.qmpClient != nil {
		_, err := live.qmpClient.Execute(ctx, "system_powerdown", nil)
		if err != nil {
			m.logger.Warn("QMP system_powerdown failed, will force-kill",
				zap.String("vm_id", vmID), zap.Error(err))
		} else {
			// Wait up to 10 seconds for the process to exit
			done := make(chan error, 1)
			go func() {
				_, err := live.process.Wait()
				done <- err
			}()

			select {
			case <-done:
				m.logger.Info("VM shut down gracefully", zap.String("vm_id", vmID))
				m.cleanup(vmID)
				m.store.UpdateVMStatus(ctx, vmID, core.VMStatusStopped)
				return nil
			case <-time.After(10 * time.Second):
				m.logger.Warn("graceful shutdown timed out", zap.String("vm_id", vmID))
			}
		}
	}

	// Force kill
	if err := live.process.Signal(syscall.SIGTERM); err != nil {
		m.logger.Warn("SIGTERM failed", zap.String("vm_id", vmID), zap.Error(err))
	}
	time.Sleep(2 * time.Second)

	if err := live.process.Kill(); err != nil {
		m.logger.Warn("SIGKILL failed", zap.String("vm_id", vmID), zap.Error(err))
	}

	m.cleanup(vmID)
	m.store.UpdateVMStatus(ctx, vmID, core.VMStatusStopped)
	return nil
}

// DestroyVM kills the VM process and removes all associated resources
// (sockets, overlays, records).
func (m *VMManager) DestroyVM(ctx context.Context, vmID string) error {
	// Stop first if still running
	m.mu.RLock()
	_, running := m.liveVMs[vmID]
	m.mu.RUnlock()

	if running {
		if err := m.StopVM(ctx, vmID); err != nil {
			m.logger.Warn("StopVM failed during Destroy, continuing cleanup",
				zap.String("vm_id", vmID), zap.Error(err))
		}
	}

	// Clean up QMP socket
	instance, err := m.store.GetVM(ctx, vmID)
	if err == nil && instance.QMPSocketPath != "" {
		os.Remove(instance.QMPSocketPath)
	}

	m.store.UpdateVMStatus(ctx, vmID, core.VMStatusDestroyed)
	m.logger.Info("VM destroyed", zap.String("vm_id", vmID))

	return nil
}

// GetQMPClient returns the QMP client for a running VM.
func (m *VMManager) GetQMPClient(vmID string) (*QMPClient, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	live, ok := m.liveVMs[vmID]
	if !ok {
		return nil, fmt.Errorf("VM %s is not running", vmID)
	}
	return live.qmpClient, nil
}

// cleanup removes a VM from the live map and closes its QMP client.
func (m *VMManager) cleanup(vmID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	live, ok := m.liveVMs[vmID]
	if !ok {
		return
	}

	if live.qmpClient != nil {
		live.qmpClient.Close()
	}
	delete(m.liveVMs, vmID)
}
