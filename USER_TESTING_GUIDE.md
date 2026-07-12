# User & Client Guide: Validating Custom QEMU Devices and Drivers

This guide outlines how to use the **Device Validation Framework (DVF)** to validate your custom QEMU simulated hardware devices and Linux kernel drivers. By following this guide, you can write tests, register devices, define test suites, and execute validation runs without needing to understand the inner workings of the orchestration control plane.

---

## 1. Overview of the Validation Flow

When you submit a validation request, DVF automatically:
1.  Acquires host CPU/Memory resources for the test run.
2.  Launches a QEMU virtual machine containing your custom emulated PCI/accelerator device.
3.  Mounts your test binaries and kernel modules into the guest OS.
4.  Loads the driver kernel module (`insmod`).
5.  Verifies the driver creates the expected device node under `/dev/`.
6.  Executes your test suite (resolving dependencies and executing steps in parallel).
7.  Unloads the driver, tears down the VM, and collects all logs, metrics, and results.

---

## 2. Step 1: Register Your Custom Device

To let DVF know about your custom hardware and kernel module, add an entry to the `configs/device_registry.json` file. 

### Configuration Example
Add your device inside the `"devices"` array:
```json
{
  "id": "my-custom-accel",
  "name": "My Custom AI Accelerator",
  "qemu_device_name": "custom-ai-accel",
  "driver_module": "my_accel_driver",
  "driver_path": "/mnt/share/drivers/my_accel_driver.ko",
  "device_node": "/dev/my_accel0",
  "target_modes": ["qemu"],
  "test_suites": ["smoke", "stress"]
}
```

### Key Field Descriptions
*   `id`: A unique string identifier for your device used when submitting test runs.
*   `qemu_device_name`: The exact device model name defined in your QEMU source code (passed to QEMU via the `-device` flag).
*   `driver_module`: The name of the kernel module (as shown by `lsmod` after loading).
*   `driver_path`: The path to the compiled `.ko` file *inside the guest*. The host's `c-test-binaries` or shared folder is mounted inside the guest under `/mnt/share`.
*   `device_node`: The character/block device node path that your driver is expected to create upon successful registration.
*   `target_modes`: Leave as `["qemu"]` for simulated software validation.

---

## 3. Step 2: Format Your Test Binaries

Your validation tests should be compiled C/C++ or executable Python binaries. 

### Writing Your Tests
To ensure the orchestrator correctly parses and displays individual test cases, your test binaries should print a structured JSON report to standard output upon completion.

#### Expected JSON Output Format:
```json
{
  "results": [
    {
      "test": "register_bounds_check",
      "status": "passed",
      "duration_ms": 15.2,
      "message": "Successfully verified boundaries of BAR 0",
      "metrics": {
        "read_latency_ns": 45.0
      }
    },
    {
      "test": "dma_transfer_stress",
      "status": "passed",
      "duration_ms": 120.0,
      "message": "Transferred 1GB data without corruption",
      "metrics": {
        "throughput_mbps": 850.5
      }
    }
  ]
}
```

*   **Exit Status**: If a test binary fails, it should return a non-zero exit code (e.g., `exit 1`). If it succeeds, it must return `0`.
*   **Location**: Compile and place your test binaries under the `c-test-binaries/` directory so they are automatically shared with the guest VM.

---

## 4. Step 3: Define Your Test Suite

Test suites group multiple test binaries together and define execution order. Define your suite by creating a `suite.json` file in a sub-folder under `test-suites/<suite-id>/`.

### Example: `test-suites/my-smoke-suite/suite.json`
```json
{
  "id": "my-smoke-suite",
  "name": "Accelerator Smoke Test Suite",
  "description": "Validates register access and basic DMA functionality",
  "timeout_seconds": 180,
  "tests": [
    {
      "binary": "read_write/test_register_rw",
      "description": "Verifies basic register read/write operations",
      "timeout_seconds": 30
    },
    {
      "binary": "dma/test_dma_basic",
      "description": "Performs small-buffer DMA write/readback loops",
      "timeout_seconds": 60,
      "depends_on": ["read_write/test_register_rw"]
    }
  ]
}
```

*   `binary`: The relative path to the binary under the `c-test-binaries/` directory.
*   `depends_on`: List the paths of other test binaries that *must* pass before this test can run. This allows you to construct complex execution DAGs.

---

## 5. Step 4: Run Your Validation Suite

Once your device is registered, tests are compiled, and the suite is defined, you can trigger a validation run via the DVF REST API.

### 5.1 Submit a Test Run
Send a `POST` request to the `/test-runs` endpoint of the running orchestrator:

```bash
curl -X POST http://localhost:8080/test-runs \
  -H "Content-Type: application/json" \
  -d '{
    "device_id": "my-custom-accel",
    "test_suite_id": "my-smoke-suite",
    "priority": 1,
    "requested_by": "hardware-engineer-01"
  }'
```

#### Example Response:
```json
{
  "id": "run-f1a2b3c4d5",
  "device_id": "my-custom-accel",
  "test_suite_id": "my-smoke-suite",
  "status": "QUEUED",
  "priority": 1,
  "created_at": "2026-07-12T07:30:00Z"
}
```

### 5.2 Poll / View Results
To check the status of your validation run, query the specific test run endpoint:

```bash
curl http://localhost:8080/test-runs/run-f1a2b3c4d5
```

#### Example Result:
```json
{
  "id": "run-f1a2b3c4d5",
  "device_id": "my-custom-accel",
  "test_suite_id": "my-smoke-suite",
  "status": "PASSED",
  "duration_ms": 25400,
  "completed_at": "2026-07-12T07:30:25Z"
}
```

---

## 6. Guidelines for Custom Drivers & Tests

To ensure seamless integration with the validation framework:
1.  **Clean Clean-up**: Ensure your driver module can be cleanly unloaded (`rmmod`). Stale resources or memory leaks in your driver can hang the VM tear-down phase.
2.  **Explicit Timeouts**: Always specify realistic `timeout_seconds` for each test in `suite.json`. If a test hangs or deadlocks, DVF will automatically abort it after this limit to free the VM.
3.  **Use `/dev/` Nodes**: Make sure your driver uses standard `udev` or kernel APIs to create the device node path configured in `device_registry.json`. DVF will verify the existence of this node before running tests.
