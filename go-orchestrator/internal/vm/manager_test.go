package vm

import (
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/config"
	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/storage"
)

// TestBuildQEMUArgs verifies the QEMU command-line construction.
// This is a pure unit test — no QEMU process is started.
func TestBuildQEMUArgs(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	store := storage.NewMemoryStore()

	cfg := &config.GlobalConfig{
		QEMU: config.QEMUConfig{
			BinaryPath:      "/usr/bin/qemu-system-x86_64",
			KernelPath:      "/boot/bzImage",
			RootFSPath:      "/images/rootfs.ext4",
			ShareDir:        "/share",
			GuestMount:      "/mnt/share",
			DefaultMemoryMB: 1024,
			DefaultCPUs:     2,
			SocketDir:       "/tmp/dvf/qmp",
		},
	}

	mgr := NewVMManager(cfg, store, logger)

	device := &config.DeviceEntry{
		ID:             "gpgpu",
		QEMUDeviceName: "gp_gpu",
	}

	vmCfg := &VMConfig{
		DeviceEntry: device,
	}

	args := mgr.BuildQEMUArgs("test-vm-1", vmCfg)

	// Convert to a single string for easier assertion
	cmdLine := strings.Join(args, " ")

	// Verify essential components are present
	checks := []struct {
		name     string
		contains string
	}{
		{"kernel", "-kernel /boot/bzImage"},
		{"rootfs", "file=/images/rootfs.ext4"},
		{"virtio-disk", "if=virtio"},
		{"console", "console=ttyS0"},
		{"init", "init=/bin/bash"},
		{"vm-id-cmdline", "dvf_vm_id=test-vm-1"},
		{"memory", "-m 1024"},
		{"cpus", "-smp 2"},
		{"nographic", "-nographic"},
		{"9p-share", "path=/share"},
		{"9p-tag", "mount_tag=hostshare"},
		{"qmp-socket", "unix:/tmp/dvf/qmp/test-vm-1.sock"},
		{"qmp-server", "server,wait=off"},
		{"device", "gp_gpu"},
		// virtio-serial agent channel (always present — carries gRPC agent)
		{"virtio-serial", "virtio-serial-pci"},
		{"agent-port", "dvf.agent.0"},
	}

	for _, check := range checks {
		if !strings.Contains(cmdLine, check.contains) {
			t.Errorf("%s: expected command line to contain %q\n  got: %s", check.name, check.contains, cmdLine)
		}
	}
}

// TestBuildQEMUArgsWithOverrides verifies that per-VM overrides take effect.
func TestBuildQEMUArgsWithOverrides(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	store := storage.NewMemoryStore()

	cfg := &config.GlobalConfig{
		QEMU: config.QEMUConfig{
			BinaryPath:      "/usr/bin/qemu-system-x86_64",
			KernelPath:      "/boot/bzImage",
			RootFSPath:      "/images/rootfs.ext4",
			ShareDir:        "/share",
			DefaultMemoryMB: 1024,
			DefaultCPUs:     2,
			SocketDir:       "/tmp/dvf/qmp",
		},
	}

	mgr := NewVMManager(cfg, store, logger)

	vmCfg := &VMConfig{
		MemoryMB:   2048,
		CPUs:       4,
		KernelPath: "/custom/bzImage",
		RootFSPath: "/custom/rootfs.img",
		ShareDir:   "/custom/share",
	}

	args := mgr.BuildQEMUArgs("override-vm", vmCfg)
	cmdLine := strings.Join(args, " ")

	// Verify overrides won
	overrides := []struct {
		name     string
		contains string
	}{
		{"memory override", "-m 2048"},
		{"cpu override", "-smp 4"},
		{"kernel override", "/custom/bzImage"},
		{"rootfs override", "/custom/rootfs.img"},
		{"share override", "path=/custom/share"},
	}

	for _, check := range overrides {
		if !strings.Contains(cmdLine, check.contains) {
			t.Errorf("%s: expected %q in command line\n  got: %s", check.name, check.contains, cmdLine)
		}
	}

	// Verify defaults did NOT leak
	if strings.Contains(cmdLine, "/boot/bzImage") {
		t.Error("default kernel should have been overridden")
	}
	if strings.Contains(cmdLine, "-m 1024") {
		t.Error("default memory should have been overridden")
	}
}

// TestBuildQEMUArgsNoDevice verifies command works without a device.
func TestBuildQEMUArgsNoDevice(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	store := storage.NewMemoryStore()

	cfg := &config.GlobalConfig{
		QEMU: config.QEMUConfig{
			BinaryPath:      "/usr/bin/qemu-system-x86_64",
			KernelPath:      "/boot/bzImage",
			RootFSPath:      "/images/rootfs.ext4",
			ShareDir:        "/share",
			DefaultMemoryMB: 512,
			DefaultCPUs:     1,
			SocketDir:       "/tmp/dvf/qmp",
		},
	}

	mgr := NewVMManager(cfg, store, logger)

	vmCfg := &VMConfig{
		// No DeviceEntry — should produce a valid command without -device
	}

	args := mgr.BuildQEMUArgs("no-device-vm", vmCfg)

	// When no DeviceEntry is provided, the only -device flags that appear
	// should be the always-present virtio-serial agent channel.
	// No custom device-under-test should be appended.
	allowedDevices := map[string]bool{
		"virtio-serial-pci": true,
	}
	for i, arg := range args {
		if arg == "-device" && i+1 < len(args) {
			dev := args[i+1]
			// Allow virtio-serial-pci and virtserialport (agent channel)
			if !allowedDevices[dev] && !strings.HasPrefix(dev, "virtserialport") {
				t.Errorf("unexpected -device flag: -device %s", dev)
			}
		}
	}


	// Should still have essential flags
	cmdLine := strings.Join(args, " ")
	if !strings.Contains(cmdLine, "-kernel") {
		t.Error("missing -kernel flag")
	}
	if !strings.Contains(cmdLine, "-m 512") {
		t.Error("expected -m 512")
	}
}

