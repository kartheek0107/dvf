// Package config provides configuration loading and validation for the DVF orchestrator.
package config

// GlobalConfig is the top-level configuration structure loaded from global_config.json.
type GlobalConfig struct {
	Server     ServerConfig     `json:"server"`
	QEMU       QEMUConfig       `json:"qemu"`
	FPGA       FPGAConfig       `json:"fpga"`
	Storage    StorageConfig    `json:"storage"`
	Telemetry  TelemetryConfig  `json:"telemetry"`
	VMDefaults VMDefaultsConfig `json:"vm_defaults"`
}

// ServerConfig defines the gRPC and REST server settings.
type ServerConfig struct {
	GRPCPort int    `json:"grpc_port"`
	RESTPort int    `json:"rest_port"`
	Host     string `json:"host"`
}

// QEMUConfig defines QEMU binary and VM infrastructure paths.
type QEMUConfig struct {
	BinaryPath      string `json:"binary_path"`
	KernelPath      string `json:"kernel_path"`
	RootFSPath      string `json:"rootfs_path"`
	ShareDir        string `json:"share_dir"`
	GuestMount      string `json:"guest_mount"`
	DefaultMemoryMB int    `json:"default_memory_mb"`
	DefaultCPUs     int    `json:"default_cpus"`
	VMImageDir      string `json:"vm_image_dir"`
	SocketDir       string `json:"socket_dir"`
	SerialSocketDir string `json:"serial_socket_dir"`
}

// FPGAConfig defines FPGA infrastructure settings for physical device validation.
type FPGAConfig struct {
	VFIOGroupPath string   `json:"vfio_group_path"`
	IOMMUEnabled  bool     `json:"iommu_enabled"`
	PCIAddresses  []string `json:"pci_addresses"`
}

// StorageConfig groups all storage backend configurations.
type StorageConfig struct {
	Postgres    PostgresConfig    `json:"postgres"`
	Redis       RedisConfig       `json:"redis"`
	ObjectStore ObjectStoreConfig `json:"object_store"`
}

// PostgresConfig defines the PostgreSQL connection parameters.
type PostgresConfig struct {
	Host           string `json:"host"`
	Port           int    `json:"port"`
	User           string `json:"user"`
	Password       string `json:"password"`
	Database       string `json:"database"`
	MaxConnections int    `json:"max_connections"`
	SSLMode        string `json:"ssl_mode"`
}

// RedisConfig defines the Redis connection parameters.
type RedisConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Password string `json:"password"`
	DB       int    `json:"db"`
	PoolSize int    `json:"pool_size"`
}

// ObjectStoreConfig defines the S3-compatible object store parameters.
type ObjectStoreConfig struct {
	Endpoint  string `json:"endpoint"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Bucket    string `json:"bucket"`
	UseSSL    bool   `json:"use_ssl"`
}

// TelemetryConfig defines observability and telemetry settings.
type TelemetryConfig struct {
	MetricsPort   int    `json:"metrics_port"`
	TraceEndpoint string `json:"trace_endpoint"`
	LogLevel      string `json:"log_level"`
	LogFormat     string `json:"log_format"`
}

// VMDefaultsConfig defines default timeout and concurrency limits for VMs.
type VMDefaultsConfig struct {
	BootTimeoutSeconds      int `json:"boot_timeout_seconds"`
	HeartbeatIntervalSeconds int `json:"heartbeat_interval_seconds"`
	HeartbeatTimeoutSeconds int `json:"heartbeat_timeout_seconds"`
	TestTimeoutSeconds      int `json:"test_timeout_seconds"`
	MaxConcurrentVMs        int `json:"max_concurrent_vms"`
}

// DeviceRegistry holds the full device registry loaded from device_registry.json.
type DeviceRegistry struct {
	Devices []DeviceEntry `json:"devices"`
}

// DeviceEntry represents a single device in the registry.
type DeviceEntry struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	VendorID       string            `json:"vendor_id"`
	DeviceID       string            `json:"device_id"`
	PCIClass       string            `json:"pci_class"`
	QEMUDeviceName string            `json:"qemu_device_name"`
	DriverModule   string            `json:"driver_module"`
	DriverPath     string            `json:"driver_path"`
	// DeviceNode is the /dev path created after the driver is loaded.
	DeviceNode     string            `json:"device_node"`
	Description    string            `json:"description"`
	TargetModes    []string          `json:"target_modes"`
	Capabilities   []string          `json:"capabilities"`
	BARLayout      map[string]BARDef `json:"bar_layout"`
	TestSuites     []string          `json:"test_suites"`

	// Vishwa-specific fields — only populated for devices that run Vishwa tests.
	// VishwaEnv contains environment variables required by the Vishwa OpenCL runtime.
	VishwaEnv     map[string]string `json:"vishwa_env,omitempty"`
	// VishwaLoader is the path to the musl/glibc loader inside the 9p share
	// used to bypass the guest's system libc.
	VishwaLoader  string            `json:"vishwa_loader,omitempty"`
	// VishwaLibDir is the directory containing Vishwa shared libraries.
	VishwaLibDir  string            `json:"vishwa_lib_dir,omitempty"`
	// VishwaTestDir is the root directory for Vishwa test binaries inside the guest.
	VishwaTestDir string            `json:"vishwa_test_dir,omitempty"`
}

// BARDef describes a PCI Base Address Register.
type BARDef struct {
	Type        string `json:"type"`
	SizeBytes   int    `json:"size_bytes"`
	Description string `json:"description"`
}
