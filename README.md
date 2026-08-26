# Device Validation Framework (DVF) — Driver Validation Suite

The Device Validation Framework (DVF) is a production-grade, fault-tolerant, multi-node driver validation control plane and guest automation system. It orchestrates the lifecycle of virtual machines running custom QEMU simulated hardware devices, loads drivers under validation, executes structured tests, and captures high-performance telemetry and logging.

---

## 1. System Architecture

The DVF architecture consists of three core components:

```mermaid
graph TD
    Client[REST/gRPC Clients] -->|Submit Test Run| API[gRPC Server & REST Gateway]
    API -->|Enqueue| Scheduler[FIFO Scheduler]
    Scheduler -->|Poll| Engine[Execution Engine]
    Engine -->|Check head room| Allocator[Resource Allocator]
    Engine -->|Register VM| Coord[Agent Coordinator]
    Engine -->|Provision QEMU| VM[VM Manager]
    VM -->|Boot VM| GuestVM[Guest VM]
    GuestVM -->|Register| VirtioHub[VirtioSerialHub]
    VirtioHub -->|Bridge msg| Coord
    Engine -->|Topological execution| Workflow[DAG Workflow Manager]
    Workflow -->|Send commands| Coord
    Coord -->|virtio-serial| GuestVM
```

### 1.1 Go Orchestrator (Control Plane)
The Orchestrator is a microservices-based control plane implemented in Go (1.22+). Its subcomponents include:
*   **Ingress Gateway**: A high-performance REST/gRPC API gateway providing endpoints to submit test runs, list running VMs, update heartbeats, and audit activities.
*   **FIFO Scheduler**: Orchestrates queueing of submitted `TestRun` requests, respecting priority and concurrency thresholds.
*   **Resource Allocator**: Tracks host CPU and memory budget dynamically. Prevents over-subscription of host resources during parallel VM execution.
*   **DAG Workflow Manager**: Evaluates multi-step test dependency trees defined in `suite.json`. Performs cycle detection, topological sorting, and parallel step dispatching. Supports per-step retry loops with exponential backoff.
*   **State Manager & Crash Recovery**: Reconciles the database (PostgreSQL/In-Memory) state on boot. Safely re-queues interrupted/crashed tests and cleans up orphan QEMU processes.
*   **Multi-Node Registry**: Coordinates distributed deployments using a thread-safe registry with TTL-based heartbeat eviction.

### 1.2 Python Guest Agent
A lightweight, dependency-free guest agent written in Python 3.
*   **Virtio-Serial Communication**: Bypasses guest TCP/IP networking completely, communicating via low-latency character devices (`/dev/virtio-ports/dvf.agent.0`).
*   **9p Filesystem mount**: Automatically mounts host shared directories (`/mnt/share`) to access test binaries and kernel modules.
*   **Driver & Device Management**: Dynamically loads kernel modules (`.ko`) and validates the presence of `/dev/gpgpu` character nodes.
*   **Structured Test Execution**: Runs compiled C validation test binaries, collects stdout/stderr logs, and parses output into standard JSON test reports.

### 1.3 VM Infrastructure
*   **VM Manager**: Automates QEMU process lifecycles, configuring memory size, CPU cores, custom PCI device arguments, and 9p share mounts.
*   **Virtio-Serial Hub**: Bridges host-side Unix sockets with QEMU guest virtio-serial channels. Handles JSON-RPC multiplexing.

---

## 2. Directory Structure

```
driver-validation-suite/
├── README.md                      # Project documentation
├── docker-compose.yml             # Dev database (Postgres) and cache (Redis)
├── configs/                       # Global orchestration configurations
│   ├── global_config.json
│   └── device_registry.json
├── go-orchestrator/               # Go Orchestrator source code
│   ├── cmd/orchestrator/          # main entry point
│   ├── internal/
│   │   ├── api/                   # REST/gRPC gateways
│   │   ├── cluster/               # Node heartbeat and cluster discovery
│   │   ├── config/                # Struct definitions and JSON loading
│   │   ├── core/                  # Scheduler, allocator, DAG workflow, and engine
│   │   ├── storage/               # PostgreSQL and Memory stores
│   │   ├── telemetry/             # Event bus (Redis streams)
│   │   └── vm/                    # QEMU manager and virtio-serial hub
│   └── proto/                     # Protocol Buffer definitions
├── python-agent/                  # Guest agent source code
│   └── agent/
│       ├── __init__.py
│       └── __main__.py            # Main loops and command handlers
├── test-suites/                   # Test suite definitions
│   ├── smoke/
│   ├── regression/
│   └── stress/
└── c-test-binaries/               # Compiled C validation tests
```

---

## 3. Getting Started

### 3.1 Quick Start (automated)

The fastest way to set up DVF on a fresh machine:

```bash
git clone <your-remote-url> driver-validation-suite
cd driver-validation-suite
cp .env.example .env          # review and edit paths if needed
bash scripts/bootstrap.sh     # installs everything, ~30 min first time
```

The bootstrap script is idempotent — re-run it safely at any time.
Use `--skip-kernel`, `--skip-qemu`, `--skip-rootfs`, `--skip-vishwa`,
or `--skip-packages` to skip individual steps.

### 3.2 Prerequisites (system packages)

| Category | Fedora / RHEL | Ubuntu / Debian |
|---|---|---|
| **Build tools** | `gcc gcc-c++ make git` | `gcc g++ make git` |
| **Go** (1.22+) | `golang` | `golang-go` |
| **Python** (3.8+) | `python3 python3-pip` | `python3 python3-pip` |
| **QEMU build deps** | `ninja-build meson pkg-config glib2-devel pixman-devel zlib-devel` | `ninja-build meson pkg-config libglib2.0-dev libpixman-1-dev zlib1g-dev` |
| **Runtime** | `qemu-img rsync curl` | `qemu-utils rsync curl` |
| **Containers** | `docker docker-compose` | `docker.io docker-compose` |
| **OpenCL** (optional) | `pocl ocl-icd` | `pocl-opencl-icd` |
| **Guest image** | [Packer](https://developer.hashicorp.com/packer/install) | [Packer](https://developer.hashicorp.com/packer/install) |
| **KVM** | `/dev/kvm` must be accessible | `/dev/kvm` must be accessible |

### 3.3 External Dependencies (not in git)

These must be built or obtained separately — they are too large or proprietary to commit:

| Dependency | How to get it | Default path |
|---|---|---|
| **Linux kernel source** | `git clone --depth=1 --branch v6.6 https://github.com/torvalds/linux` then `make defconfig && make -j$(nproc)` | `$HOME/VirtualMachines/linux` |
| **Custom QEMU** (with `gp_gpu` device) | `bash qemu-accelerator-models/scripts/build_qemu_with_models.sh` | `$DVF_ROOT/builds/qemu/qemu-system-x86_64` |
| **Guest rootfs** (`rootfs.ext4`) | `cd guest-os && make build && make install` | `$HOME/qemu-rootfs/rootfs.ext4` |
| **CDAC / Vishwa source** (optional) | Obtain from CDAC — proprietary IP | `$HOME/cdac/FW_SW_Milestone_2/code` |

> **Tip**: Copy `.env.example` → `.env` and set paths to match your machine layout.

### 3.4 Running the Orchestrator

1. Start the background databases:
   ```bash
   docker-compose up -d
   ```
2. Build the orchestrator binary:
   ```bash
   cd go-orchestrator
   go build -o orchestrator ./cmd/orchestrator
   ```
3. Run the orchestrator:
   ```bash
   export DVF_ROOT=$(pwd)/..
   ./orchestrator --config configs --storage postgres
   ```

### 3.5 Running Unit Tests

```bash
cd go-orchestrator && go test ./...                                    # Go
python3 -m unittest discover -s python-agent/tests -p "test_*.py" -v  # Python
```


---

## 4. REST APIs

*   `GET /healthz` - Check API server liveness
*   `GET /readyz` - Check storage & network readiness
*   `POST /test-runs` - Submit a new validation test run
*   `GET /test-runs/{id}` - View the status and results of a run
*   `GET /cluster/nodes` - List active orchestrator worker nodes
*   `POST /cluster/heartbeat` - Report node health

---

## 5. CI/CD Automation Pipeline

DVF features automated continuous integration pipelines via GitLab CI (`.gitlab-ci.yml`). On every push to the repository:
1. **Go control plane & test binaries are built** and passed as artifacts.
2. **Drivers and guest-agent assets are deployed** to the shared host mount.
3. **The orchestrator service starts dynamically** inside the runner.
4. **`ci_impact_analyzer.py` calculates the impacted tests** using Kahn's topological sort and executes them to completion.
5. **The service cleanly shuts down** and cleanup is performed, preventing ports/VM resources from leaking.
