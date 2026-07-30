# Adding a New Test to the DVF Platform

This guide is for **hardware and validation engineers**. You do not need to understand QEMU, Docker, or CI YAML. Just follow the steps below.

---

## How the System Works (One-Time Read)

Every test binary runs **inside a QEMU virtual machine** that has the real CDAC GPGPU driver loaded. The VM mounts your files over a 9p network share, so anything you put in the share directory is immediately visible to the VM.

The **self-contained bundle pattern** means you copy the exact `.so` libraries your binary needs into a `lib/` folder right next to your binary. The loader inside the VM automatically finds them — no changes to the VM, no CI YAML edits, no Packer rebuilds ever.

```
share/vishwa_tests/
└── regression/
    └── my_new_test/
        ├── my_new_test     ← your compiled binary
        ├── kernel.cl       ← (if OpenCL) your kernel source
        └── lib/
            ├── libgpurt.so     ← bundled automatically
            ├── libvishwa.so    ← bundled automatically
            └── libstdc++.so.6  ← bundled automatically
```

---

## Scenario A — Adding a New Vishwa / OpenCL Test

These are pre-compiled binaries from the Vishwa build system.

### Step 1 — Build your binary (in the Vishwa source)

Build as you normally would inside the Vishwa source tree:

```bash
cd ~/Downloads/CDAC/FW_SW_Milestone_2/code/vishwa_hw_testing_env
bash vishwa_run.sh
# Your compiled binary will be at: build/tests/<category>/<name>/<name>
```

### Step 2 — Register the test in the device registry

Open `go-orchestrator/configs/device_registry.json` and add your test to `test_suites`:

```json
"test_suites": [
    "vishwa/regression/vecaddx",
    "vishwa/opencl/vecadd",
    "vishwa/regression/my_new_test"    ← add this
]
```

> **Naming convention:** `vishwa/<category>/<test_name>` maps to the directory  
> `vishwa_tests/<category>/<test_name>/` inside the VM share.

### Step 3 — Update the CI deploy job (`.gitlab-ci.yml`)

In the `deploy-share` job, find the block that copies Vishwa binaries (Step 5) and add:

```yaml
copy_if_exists "${VISHWA_BUILD}/tests/regression/my_new_test/my_new_test" \
               "${SHARE_DIR}/vishwa_tests/regression/my_new_test/my_new_test"
```

Also add any assets (`.cl` files, images, weights) the same way:

```yaml
cp_asset "${TESTS_SRC}/regression/my_new_test/kernel.cl" \
         "${SHARE_DIR}/vishwa_tests/regression/my_new_test/"
```

Also add bundling in Step 7 (Strategy 1):

```yaml
bundle_test "${SHARE_DIR}/vishwa_tests/regression/my_new_test/my_new_test"
```

### Step 4 — Deploy locally and verify

Run the deploy script to push everything to the share (relative symlinks are automatically configured via `scripts/setup_relative_symlinks.sh` during deploy):

```bash
cd ~/driver-validation-suite
bash scripts/deploy_share.sh --skip-driver-build --skip-vishwa-build

# Or run the symlink shortcut script standalone if modifying symlinks manually:
bash scripts/setup_relative_symlinks.sh
```

Then verify inside the VM manually:

```bash
# In the manual test VM:
ls -la /mnt/share/vishwa_tests/regression/my_new_test/
ls -la /mnt/share/vishwa_tests/regression/my_new_test/lib/
```

### Step 5 — Test it manually in the VM

```bash
cd /mnt/share/vishwa_tests/regression/my_new_test

export LD_LIBRARY_PATH=/mnt/share/vishwa_tests/lib
export OCL_ICD_VENDORS=/mnt/share/vishwa_tests/lib/OpenCL/vendors
export POCL_DEVICES=basic
export POCL_CACHE_DIR=/tmp/pocl_cache

/mnt/share/vishwa_tests/lib/ld-linux-x86-64.so.2 \
  --library-path /mnt/share/vishwa_tests/lib:./lib \
  ./my_new_test \
  1>/tmp/stdout.txt 2>/tmp/stderr.txt

echo "Exit: $?"
cat /tmp/stdout.txt
cat /tmp/stderr.txt
```

### Step 6 — Run through the orchestrator and view logs

```bash
curl -X POST http://localhost:9080/api/v1/test-runs \
  -H "Content-Type: application/json" \
  -d '{
    "device_id": "gpgpu",
    "test_suite_id": "vishwa/regression/my_new_test",
    "priority": 1,
    "requested_by": "your-name"
  }'
```

Watch the result via the REST API or using the PostgreSQL log viewer:
```bash
# View result in PostgreSQL:
python3 scripts/view_pg_logs.py --failed

# Or via API:
curl http://localhost:9080/api/v1/test-runs/<id-from-above>
```

### Step 7 — Commit and push

```bash
git add go-orchestrator/configs/device_registry.json
git add .gitlab-ci.yml
git commit -m "feat(tests): add my_new_test to validation suite"
git push
```

CI picks it up automatically on every subsequent push.

---

## Scenario B — Adding a New C Test (DVF Framework)

These are custom C tests you write yourself against the GPGPU device.

### Step 1 — Write your test

Create your source file in `c-test-binaries/`:

```
c-test-binaries/
└── my_category/
    ├── my_test.c
    └── my_test.json    ← metadata sidecar (see below)
```

**`my_test.c` skeleton:**
```c
#include "../common/test_framework.h"
#include "../common/device_helpers.h"

static int test_my_feature(void)
{
    int fd = gpgpu_open_device("/dev/gp_gpu");
    ASSERT_TRUE(fd >= 0, "Failed to open /dev/gp_gpu");

    /* ... your ioctl calls, mmio reads, DMA operations ... */

    gpgpu_close_device(fd);
    return 0;   /* 0 = PASS */
}

int main(void)
{
    TEST_SUITE_BEGIN("my_category/my_test");
    RUN_TEST(test_my_feature);
    TEST_SUITE_END();
}
```

**`my_test.json` sidecar (tells the CI which device this test targets):**
```json
{
    "suite_name":  "my_category/my_test",
    "device_id":   "gpgpu",
    "description": "Tests my feature on the CDAC GPGPU",
    "timeout_s":   60,
    "deps":        []
}
```

### Step 2 — Add to the Makefile

Open `c-test-binaries/Makefile` and add your binary to the `TESTS` list:

```makefile
TESTS := \
    read_write/test_register_rw \
    ...
    my_category/my_test          ← add this
```

The Makefile already has `-Wl,-rpath,'$$ORIGIN/lib'` baked in, so your binary will automatically look for libraries in a `./lib/` directory at runtime.

### Step 3 — Build and bundle

```bash
cd ~/driver-validation-suite/c-test-binaries
make my_category/my_test

# If your test needs shared libraries:
cd ~/driver-validation-suite
bash scripts/bundle_libs.sh c-test-binaries/my_category/my_test
```

The bundler creates `c-test-binaries/my_category/lib/` with all needed `.so` files.

### Step 4 — Register in device_registry.json

```json
"test_suites": [
    "smoke/register_rw",
    "my_category/my_test"     ← add this
]
```

### Step 5 — Verify RPATH is set correctly

```bash
readelf -d c-test-binaries/my_category/my_test | grep -i rpath
# Expected: Library rpath: [$ORIGIN/lib]
```

### Step 6 — Commit and push

```bash
git add c-test-binaries/my_category/
git add go-orchestrator/configs/device_registry.json
git commit -m "feat(tests): add my_category/my_test"
git push
```

---

## Scenario B2 — Adding a New C++ Test (DVF Framework)

The DVF framework also supports tests written in C++. The same headers, JSON
sidecar, and `device_registry.json` workflow apply — you only need to use a
`.cpp` source file and a `g++`-compiled binary.

### Step 1 — Write your test

Create your source file in `c-test-binaries/`:

```
c-test-binaries/
└── my_category/
    ├── my_test.cpp
    └── my_test.json    ← metadata sidecar (identical format to C tests)
```

**`my_test.cpp` skeleton:**
```cpp
#include "../common/test_framework.h"
#include "../common/device_helpers.h"

#include <string>       // C++ standard library is available
#include <vector>

// Test functions must return int (0 = PASS, non-zero = FAIL)
static int test_my_feature(void)
{
    DeviceConfig cfg = dvf_load_config();
    int fd = dvf_open_device(&cfg, O_RDWR);
    ASSERT_TRUE(fd >= 0, "Failed to open device");

    // Use C++ freely here
    std::vector<uint32_t> values = {0xDEAD, 0xBEEF, 0xCAFE};
    for (int i = 0; i < (int)values.size(); i++) {
        ASSERT_EQ(dvf_write_reg(fd, i, values[i], &cfg), 0, "write failed");
        int err = 0;
        ASSERT_EQ(dvf_read_reg(fd, i, &err, &cfg), values[i], "readback mismatch");
    }

    close(fd);
    return 0;   // 0 = PASS
}

int main(void)
{
    TEST_SUITE_BEGIN("my_category/my_test");
    RUN_TEST(test_my_feature);
    TEST_SUITE_END();
}
```

> **Note:** The `extern "C"` guards are already present in all DVF headers,
> so the test framework macros (`RUN_TEST`, `ASSERT_EQ`, etc.) work identically
> in C++ as in C. You do not need to add any linkage annotations yourself.

**`my_test.json` sidecar (identical format to C tests):**
```json
{
    "name": "my_test",
    "category": "my_category",
    "capabilities": ["mmio"],
    "dependencies": [""],
    "timeout_seconds": 60
}
```

### Step 2 — Add to the Makefile

Open `c-test-binaries/Makefile` and add your binary to the `TESTS` list:

```makefile
TESTS := \
    read_write/test_register_rw \
    ...
    my_category/my_test          ← add this
```

The Makefile automatically detects `.cpp` sources via the `%: %.cpp` pattern
rule and compiles with `g++ -std=c++17`. No other Makefile changes are needed.

### Step 3 — Build and bundle

```bash
cd ~/driver-validation-suite/c-test-binaries
make my_category/my_test

# Bundle runtime .so dependencies (libstdc++, libpthread, etc.)
cd ~/driver-validation-suite
bash scripts/bundle_libs.sh c-test-binaries/my_category/my_test \
  c-test-binaries/my_category/lib
```

> **libstdc++ note:** C++ binaries link against `libstdc++.so.6` dynamically.
> `bundle_libs.sh` detects this automatically via `ldd` and copies it into
> the `lib/` directory — no manual step needed.

### Step 4 — Register in device_registry.json

```json
"test_suites": [
    "smoke/register_rw",
    "my_category/my_test"     ← add this
]
```

### Step 5 — Verify RPATH is set correctly

```bash
readelf -d c-test-binaries/my_category/my_test | grep -i rpath
# Expected: Library rpath: [$ORIGIN/lib]
```

### Step 6 — Commit and push

```bash
git add c-test-binaries/my_category/
git add go-orchestrator/configs/device_registry.json
git commit -m "feat(tests): add my_category/my_test (C++)"
git push
```

---

## Scenario C — Adding a Library to an Existing Test

If you add a new `.so` dependency to an already-registered test:

```bash
# Re-run the bundler on the updated binary:
bash scripts/bundle_libs.sh \
  ~/qemu-rootfs/share/vishwa_tests/regression/my_test/my_test \
  ~/qemu-rootfs/share/vishwa_tests/regression/my_test/lib/

# The new .so is automatically picked up by the agent on the next run.
# No CI YAML changes needed.
```

If the library isn't on the host system and `ldd` can't find it, copy it manually:

```bash
cp /path/to/libmylib.so.3 \
   ~/qemu-rootfs/share/vishwa_tests/regression/my_test/lib/libmylib.so.3
```

Then re-deploy:
```bash
bash scripts/deploy_share.sh --skip-vishwa-build --skip-driver-build
```

---

## Quick Reference Table

| Task | Files to Change | Command to Run |
|---|---|---|
| Add a Vishwa test | `device_registry.json`, `.gitlab-ci.yml` | `deploy_share.sh` → `curl POST /api/v1/test-runs` |
| Add a C test | New `.c` + `Makefile` + `device_registry.json` | `make` → `bundle_libs.sh` → `deploy_share.sh` |
| Add a C++ test | New `.cpp` + `Makefile` + `device_registry.json` | `make` → `bundle_libs.sh` → `deploy_share.sh` |
| Add a library | Nothing (auto-bundled) | `bundle_libs.sh <binary>` |
| Change driver source | `driver-source/gpgpu_driver/src/` | `make -C driver-source/gpgpu_driver` → `deploy_share.sh` |
| Rebuild everything | — | `bash scripts/deploy_share.sh` (no --skip flags) |

---

## Checklist for Every New Test

```
[ ] Binary compiled with -Wl,-rpath,'$ORIGIN/lib'  (automatic via DVF Makefile)
[ ] libs bundled into ./lib/ via bundle_libs.sh  (libstdc++.so.6 for C++ tests)
[ ] Test registered in device_registry.json
[ ] Test name follows convention: <category>/<test_name>
[ ] Binary passes manual VM test (scripts/manual_test_vm.sh)
[ ] git add + commit + push → CI green
```

---

## Key Paths Quick Reference

| What | Path |
|---|---|
| QEMU 9p share (host) | `~/qemu-rootfs/share/` |
| Vishwa tests in guest | `/mnt/share/vishwa_tests/` |
| Vishwa runtime libs | `~/qemu-rootfs/share/vishwa_tests/lib/` |
| Driver `.ko` in share | `~/qemu-rootfs/share/gpgpu_pcie_ep_driver.ko` |
| DVF agent | `~/qemu-rootfs/share/python-agent/` |
| Device registry | `go-orchestrator/configs/device_registry.json` |
| Deploy script | `scripts/deploy_share.sh` |
| Bundle script | `scripts/bundle_libs.sh` |
| Manual VM script | `scripts/manual_test_vm.sh` |
| Orchestrator API | `http://localhost:9080/api/v1/` |
