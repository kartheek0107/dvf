# DVF Framework Extension & Manual Operations Guide

This guide details the manual workflow for extending, testing, bundling libraries, running, and viewing logs within the Driver Validation Framework (DVF).

---

## 1. File Lifecycle Matrix (What Files to Create, Update, or Delete)

Whenever you add or modify components in DVF, refer to this matrix to know exactly which files are affected:

| Operation | Created Files `[NEW]` | Updated Files `[MODIFY]` | Deleted / Cleaned `[REMOVE]` |
|---|---|---|---|
| **Add a New C/C++ Test** | `c-test-binaries/<cat>/<name>.c` (or `.cpp`)<br>`c-test-binaries/<cat>/<name>.json` | `c-test-binaries/Makefile`<br>`go-orchestrator/configs/device_registry.json` | Previous compiled test binaries (via `make clean`) |
| **Add a New Vishwa / OpenCL Test** | Vishwa compiled test binary & `.cl` kernel sources in `vishwa_tests/` | `go-orchestrator/configs/device_registry.json`<br>`.gitlab-ci.yml` (in `deploy-share` section) | — |
| **Add a New QEMU Device Model** | `qemu-accelerator-models/hw/misc/<device>.c` | `qemu-accelerator-models/hw/misc/meson.build`<br>`qemu-accelerator-models/meson.build`<br>`go-orchestrator/configs/device_registry.json` | Clear stale build dir (`rm -rf builds/qemu-build`) |
| **Add / Update Shared Libraries (`.so`)** | Bundled `.so` files in `lib/` next to test binary | Relative symlinks in `opencl/*/lib/` & `share/` | Stale host-absolute symlinks |
| **Run / Debug Tests** | Host VM console logs in `/tmp/dvf/qemu-vm-*.log`<br>PostgreSQL records in `dvf_validation` | — | Stale socket files in `/tmp/dvf/qmp/` & `/tmp/dvf/serial/` |

---

## 2. Step-by-Step Manual Workflow

### Step A: Manually Add a New Test

#### Option 1: Adding a Custom C/C++ Test Binary
1. **Create Source & Metadata**:
   - Source: `c-test-binaries/read_write/my_test.c` (or `.cpp`)
   - Sidecar JSON: `c-test-binaries/read_write/my_test.json`
   ```json
   {
       "name": "my_test",
       "category": "read_write",
       "capabilities": ["mmio"],
       "dependencies": [""],
       "timeout_seconds": 60
   }
   ```
2. **Update Makefile**:
   Edit `c-test-binaries/Makefile` to add `read_write/my_test` to `TESTS`.
3. **Compile Binary**:
   ```bash
   cd c-test-binaries && make read_write/my_test
   ```
4. **Register in Device Registry**:
   Open `go-orchestrator/configs/device_registry.json` and add `"smoke"` or `"read_write/my_test"` under the target device's `"test_suites"`.

---

#### Option 2: Adding a Vishwa OpenCL Test
1. **Copy Binary & Kernel**:
   Place test executable and `.cl` kernel into `~/qemu-rootfs/share/vishwa_tests/opencl/my_opencl_test/`.
2. **Register in Registry**:
   Add `"vishwa/opencl/my_opencl_test"` to `test_suites` in `go-orchestrator/configs/device_registry.json`.
3. **Update CI Script (`.gitlab-ci.yml`)**:
   Add `copy_if_exists` and `bundle_test` lines under `deploy-share` in `.gitlab-ci.yml`.

---

### Step B: Manually Add a New QEMU Device Model

1. **Write Device Source**:
   Create `qemu-accelerator-models/hw/misc/my_device.c`.
2. **Register in Meson**:
   Edit `qemu-accelerator-models/hw/misc/meson.build` and add `files('my_device.c')` to `dvf_device_sources`.
3. **Compile Custom QEMU Binary**:
   ```bash
   bash qemu-accelerator-models/scripts/build_qemu_with_models.sh \
     "qemu-accelerator-models" \
     "builds/qemu-build" \
     "v8.2.0" \
     ""
   cp builds/qemu-build/qemu-system-x86_64 builds/qemu/qemu-system-x86_64
   ```
4. **Verify Device is Baked In**:
   ```bash
   ./builds/qemu/qemu-system-x86_64 -device help | grep my_device
   ```
5. **Register Device in `device_registry.json`**:
   Add new entry under `"devices"` in `go-orchestrator/configs/device_registry.json`.

---

### Step C: Manually Manage Dynamic Libraries & Symlinks

1. **Bundle Dependencies (`bundle_libs.sh`)**:
   Automatically inspect and bundle required `.so` dynamic libraries next to your test binary:
   ```bash
   bash scripts/bundle_libs.sh c-test-binaries/read_write/my_test c-test-binaries/read_write/lib/
   ```
2. **Set Relative Symlinks (CRITICAL for 9p VM mount)**:
   Ensure all per-test `lib/` and `share/` symlinks use **relative paths** (`../../../lib/pocl`, etc.), so guest QEMU VMs can resolve them:
   ```bash
   BASE=~/qemu-rootfs/share/vishwa_tests
   cd $BASE/opencl/vecadd/lib
   ln -sf ../../../lib/pocl pocl
   ln -sf ../../../lib/ld-linux-x86-64.so.2 ld-linux-x86-64.so.2
   cd $BASE/opencl/vecadd
   ln -sf ../../share share
   ```

---

### Step D: Deploy Assets & Run CI Locally

1. **Deploy to 9p Share**:
   ```bash
   bash scripts/deploy_share.sh --skip-driver-build --skip-vishwa-build
   ```
2. **Run Orchestrator & Impact Analyzer Locally**:
   ```bash
   bash scripts/run_ci_locally.sh
   ```
3. **Manual Interactive VM Testing**:
   To test inside a single guest VM manually:
   ```bash
   bash scripts/manual_test_vm.sh gpgpu
   ```

---

### Step E: Viewing All Logs

1. **Using PostgreSQL CLI (`scripts/view_pg_logs.py`)**:
   - List recent runs: `python3 scripts/view_pg_logs.py`
   - List failed runs: `python3 scripts/view_pg_logs.py --failed`
   - Inspect specific run: `python3 scripts/view_pg_logs.py --run-id <RUN_ID>`
2. **Direct SQL Queries**:
   ```bash
   podman exec -it dvf-postgres psql -U dvf -d dvf_validation -c "SELECT * FROM test_runs ORDER BY created_at DESC LIMIT 10;"
   ```
3. **Real-time QEMU Console Logs**:
   ```bash
   tail -f /tmp/dvf/qemu-vm-*.log
   ```
4. **GitLab CI Web UI**:
   Download artifacts `orchestrator.log` and `logs/qemu/` from the pipeline job summary.
