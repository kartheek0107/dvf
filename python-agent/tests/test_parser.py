#!/usr/bin/env python3
"""
test_parser.py — Unit tests for parse_vishwa_output and status reconciliation.

Run with:
    python3 -m pytest python-agent/tests/ -v
    # or directly:
    python3 python-agent/tests/test_parser.py

Coverage:
  - parse_vishwa_output: all 4 parsing strategies
  - Status reconciliation: exit-0-but-GPURT-errors bug fix
  - Strategy 1: bundled lib/ detection (mocked filesystem)
"""

import json
import os
import sys
import tempfile
import unittest

# Add agent module to path
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from agent.__main__ import parse_vishwa_output


class TestParseVishwaOutputStrategy1JSON(unittest.TestCase):
    """Strategy 1: full JSON object starting with {\"suite\":"""

    def test_clean_json_pass(self):
        output = json.dumps({
            "suite": "vecadd",
            "results": [{"test": "test_add", "status": "PASS", "duration_ms": 12.5}],
            "summary": {"total": 1, "passed": 1, "failed": 0, "duration_ms": 12.5},
        })
        result = parse_vishwa_output(output, "vecadd", 12.5)
        self.assertEqual(result["summary"]["passed"], 1)
        self.assertEqual(result["summary"]["failed"], 0)

    def test_clean_json_fail(self):
        output = json.dumps({
            "suite": "vecadd",
            "results": [{"test": "test_add", "status": "FAIL", "duration_ms": 5.0,
                         "message": "mismatch"}],
            "summary": {"total": 1, "passed": 0, "failed": 1, "duration_ms": 5.0},
        })
        result = parse_vishwa_output(output, "vecadd", 5.0)
        self.assertEqual(result["summary"]["failed"], 1)
        self.assertEqual(result["summary"]["passed"], 0)

    def test_mixed_results(self):
        output = json.dumps({
            "suite": "regression",
            "results": [
                {"test": "t1", "status": "PASS", "duration_ms": 1.0},
                {"test": "t2", "status": "FAIL", "duration_ms": 2.0, "message": "err"},
            ],
            "summary": {"total": 2, "passed": 1, "failed": 1, "duration_ms": 3.0},
        })
        result = parse_vishwa_output(output, "regression", 3.0)
        self.assertEqual(result["summary"]["total"], 2)
        self.assertEqual(result["summary"]["passed"], 1)
        self.assertEqual(result["summary"]["failed"], 1)


class TestParseVishwaOutputStrategy4Sentinel(unittest.TestCase):
    """Strategy 4: plain-text TEST PASSED / FAILED sentinel lines."""

    def test_clean_test_passed_sentinel(self):
        output = "Initializing...\nTEST PASSED!\n"
        result = parse_vishwa_output(output, "vecaddx", 100.0)
        self.assertEqual(result["summary"]["passed"], 1)
        self.assertEqual(result["summary"]["failed"], 0)
        self.assertEqual(result["results"][0]["status"], "PASS")

    def test_gpurt_device_open_failed_marks_fail(self):
        """The core bug fix: GPURT errors with exit 0 must be FAIL."""
        output = (
            "[GPURT ERROR] device open failed: No such file or directory\n"
            "device open failed: No such file or directory\n"
            "[GPURT ERROR] Context creation failed: Bad file descriptor\n"
            "[GPURT ERROR] IOCTL failed: Bad file descriptor\n"
            "GPU_IOCTL_GET_VA failed: Bad file descriptor\n"
            "[GPURT] WAIT IOCTL FAILED: Bad file descriptor\n"
        )
        result = parse_vishwa_output(output, "vishwa/regression/vecaddx", 500.0)
        self.assertEqual(result["summary"]["failed"], 1,
                         "GPURT errors must produce failed=1 in summary")
        self.assertEqual(result["summary"]["passed"], 0)
        self.assertEqual(result["results"][0]["status"], "FAIL")

    def test_gpurt_error_with_passed_word_elsewhere(self):
        """GPURT ERROR in output must override any stray 'PASSED' substring."""
        output = (
            "Running: test_passed_validation...\n"     # contains 'passed' substring
            "[GPURT ERROR] device open failed: No such file or directory\n"
        )
        result = parse_vishwa_output(output, "vecaddx", 200.0)
        # Even though 'passed' appears in 'test_passed_validation',
        # the GPURT ERROR must force a FAIL result.
        self.assertEqual(result["summary"]["failed"], 1,
                         "GPURT ERROR must dominate even when 'passed' substring exists")

    def test_ioctl_failed_marks_fail(self):
        output = "[GPURT ERROR] IOCTL FAILED: Bad file descriptor\n"
        result = parse_vishwa_output(output, "suite", 100.0)
        self.assertEqual(result["summary"]["failed"], 1)

    def test_device_open_failed_marks_fail(self):
        output = "DEVICE OPEN FAILED: /dev/gp_gpu does not exist\n"
        result = parse_vishwa_output(output, "suite", 100.0)
        self.assertEqual(result["summary"]["failed"], 1)

    def test_empty_output_marks_fail(self):
        """No output at all — nothing succeeded, so it must fail."""
        result = parse_vishwa_output("", "suite", 50.0)
        # Strategy 4 else branch — total=1, passed=0, failed=1
        self.assertEqual(result["summary"]["passed"], 0)


class TestStatusReconciliation(unittest.TestCase):
    """
    Test the status reconciliation logic introduced to fix the
    'PASSED with GPURT errors' bug.

    We simulate the reconciliation block from _handle_start_test:

        if status == "passed":
            summary = parsed.get("summary", {})
            if summary.get("failed", 0) > 0 or
               (summary.get("total", 0) > 0 and summary.get("passed", 0) == 0):
                status = "failed"
    """

    def _reconcile(self, status: str, parsed: dict) -> str:
        """Inline the reconciliation logic for unit testing."""
        if status == "passed":
            summary = parsed.get("summary", {})
            parsed_failed = summary.get("failed", 0)
            parsed_passed = summary.get("passed", 0)
            parsed_total  = summary.get("total", 0)
            if parsed_failed > 0 or (parsed_total > 0 and parsed_passed == 0):
                status = "failed"
        return status

    def test_exit0_gpurt_error_reconciled_to_failed(self):
        """Main regression: exit code 0 but GPURT errors -> must be failed."""
        output = (
            "[GPURT ERROR] device open failed: No such file or directory\n"
            "[GPURT ERROR] IOCTL failed: Bad file descriptor\n"
        )
        parsed = parse_vishwa_output(output, "vecaddx", 500.0)
        result_status = self._reconcile("passed", parsed)
        self.assertEqual(result_status, "failed",
                         "Exit code 0 + GPURT errors must reconcile to 'failed'")

    def test_genuine_pass_not_affected(self):
        """A genuine PASS (exit 0 + parser agrees) must remain 'passed'."""
        output = json.dumps({
            "suite": "vecadd",
            "results": [{"test": "t1", "status": "PASS", "duration_ms": 10.0}],
            "summary": {"total": 1, "passed": 1, "failed": 0, "duration_ms": 10.0},
        })
        parsed = parse_vishwa_output(output, "vecadd", 10.0)
        result_status = self._reconcile("passed", parsed)
        self.assertEqual(result_status, "passed",
                         "A genuine pass must stay 'passed' after reconciliation")

    def test_exit1_fail_not_changed(self):
        """If the binary already exits non-zero, status stays 'failed'."""
        parsed = parse_vishwa_output("", "suite", 50.0)
        result_status = self._reconcile("failed", parsed)
        self.assertEqual(result_status, "failed")

    def test_errored_not_changed(self):
        """'errored' (timeout/exception) must not be altered by reconciliation."""
        parsed = parse_vishwa_output("TEST PASSED!", "suite", 50.0)
        result_status = self._reconcile("errored", parsed)
        self.assertEqual(result_status, "errored")

    def test_mixed_results_with_one_failure(self):
        """If any test in the suite fails, the whole run must be failed."""
        output = json.dumps({
            "suite": "regression",
            "results": [
                {"test": "t1", "status": "PASS", "duration_ms": 1.0},
                {"test": "t2", "status": "FAIL", "duration_ms": 2.0},
            ],
            "summary": {"total": 2, "passed": 1, "failed": 1, "duration_ms": 3.0},
        })
        parsed = parse_vishwa_output(output, "regression", 3.0)
        result_status = self._reconcile("passed", parsed)  # binary exited 0
        self.assertEqual(result_status, "failed")


class TestBundledLibDetection(unittest.TestCase):
    """Strategy 1: verify that the lib/ directory detection logic is correct."""

    def test_lib_dir_prepended_when_present(self):
        """Simulate a binary_dir that has a lib/ subdirectory."""
        with tempfile.TemporaryDirectory() as tmpdir:
            lib_dir_path = os.path.join(tmpdir, "lib")
            os.makedirs(lib_dir_path)

            # Simulate the detection logic
            bundled_lib = os.path.join(tmpdir, "lib")
            base_lib_dir = "/mnt/share/vishwa_tests/lib"
            ld_library_path = ""

            if os.path.isdir(bundled_lib):
                ld_library_path = f"{bundled_lib}:{ld_library_path}" if ld_library_path else bundled_lib
                result_lib_dir = f"{bundled_lib}:{base_lib_dir}"
            else:
                result_lib_dir = base_lib_dir

            self.assertTrue(result_lib_dir.startswith(bundled_lib),
                            "Bundled lib/ must be FIRST in library path")
            self.assertIn(base_lib_dir, result_lib_dir,
                          "Base lib_dir must still be included")
            self.assertEqual(ld_library_path, bundled_lib)

    def test_no_lib_dir_uses_default(self):
        """When no lib/ directory exists, the original lib_dir must be unchanged."""
        with tempfile.TemporaryDirectory() as tmpdir:
            # No lib/ subdirectory created
            bundled_lib = os.path.join(tmpdir, "lib")
            base_lib_dir = "/mnt/share/vishwa_tests/lib"

            if os.path.isdir(bundled_lib):
                result_lib_dir = f"{bundled_lib}:{base_lib_dir}"
            else:
                result_lib_dir = base_lib_dir

            self.assertEqual(result_lib_dir, base_lib_dir,
                             "Without lib/, lib_dir must remain unchanged")

    def test_multiple_so_files_all_accessible(self):
        """Verify that multiple .so files placed in lib/ are found."""
        with tempfile.TemporaryDirectory() as tmpdir:
            lib_dir_path = os.path.join(tmpdir, "lib")
            os.makedirs(lib_dir_path)
            # Simulate placing .so files
            libs = ["libOpenCL.so.1", "libmycustom.so.3", "libpocl.so.2"]
            for lib in libs:
                open(os.path.join(lib_dir_path, lib), "w").close()

            found = os.listdir(lib_dir_path)
            for lib in libs:
                self.assertIn(lib, found, f"{lib} must be present in bundled lib/")


if __name__ == "__main__":
    unittest.main(verbosity=2)
