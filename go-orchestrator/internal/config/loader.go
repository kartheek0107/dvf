package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultConfigDir is the default path to the configs directory relative to the binary.
const DefaultConfigDir = "configs"

// Load reads global_config.json from the given directory and returns a GlobalConfig.
func Load(configDir string) (*GlobalConfig, error) {
	cfg := &GlobalConfig{}
	if err := loadJSON(filepath.Join(configDir, "global_config.json"), cfg); err != nil {
		return nil, fmt.Errorf("loading global config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating global config: %w", err)
	}
	return cfg, nil
}

// LoadDeviceRegistry reads device_registry.json from the given directory.
func LoadDeviceRegistry(configDir string) (*DeviceRegistry, error) {
	reg := &DeviceRegistry{}
	if err := loadJSON(filepath.Join(configDir, "device_registry.json"), reg); err != nil {
		return nil, fmt.Errorf("loading device registry: %w", err)
	}
	if err := reg.validate(); err != nil {
		return nil, fmt.Errorf("validating device registry: %w", err)
	}
	return reg, nil
}

// loadJSON is a helper that reads a JSON file, expands environment variables, and unmarshals it into the target.
func loadJSON(path string, target interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	expanded := os.ExpandEnv(string(data))
	if err := json.Unmarshal([]byte(expanded), target); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	return nil
}

// validate checks that required fields in GlobalConfig are populated.
func (c *GlobalConfig) validate() error {
	if c.Server.GRPCPort == 0 {
		return fmt.Errorf("server.grpc_port is required")
	}
	if c.Server.RESTPort == 0 {
		return fmt.Errorf("server.rest_port is required")
	}
	if c.QEMU.BinaryPath == "" {
		return fmt.Errorf("qemu.binary_path is required")
	}
	if c.VMDefaults.MaxConcurrentVMs == 0 {
		c.VMDefaults.MaxConcurrentVMs = 5 // sensible default
	}
	return nil
}

// validate checks that the device registry is well-formed.
func (r *DeviceRegistry) validate() error {
	if len(r.Devices) == 0 {
		return fmt.Errorf("device registry is empty — at least one device is required")
	}
	seen := make(map[string]bool)
	for i, d := range r.Devices {
		if d.ID == "" {
			return fmt.Errorf("device[%d].id is required", i)
		}
		if seen[d.ID] {
			return fmt.Errorf("duplicate device id: %s", d.ID)
		}
		seen[d.ID] = true
		if d.QEMUDeviceName == "" {
			return fmt.Errorf("device[%d] (%s): qemu_device_name is required", i, d.ID)
		}
		if d.DriverModule == "" {
			return fmt.Errorf("device[%d] (%s): driver_module is required", i, d.ID)
		}
	}
	return nil
}

// FindDevice looks up a device by its ID in the registry.
func (r *DeviceRegistry) FindDevice(id string) (*DeviceEntry, error) {
	for i := range r.Devices {
		if r.Devices[i].ID == id {
			return &r.Devices[i], nil
		}
	}
	return nil, fmt.Errorf("device %q not found in registry", id)
}
