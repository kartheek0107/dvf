/*
 * test_offset_sweep.c — Sweep all register offsets in the device.
 *
 * Uses the generic DVF device API so this binary works against any
 * register-based device (QEMU GPGPU, physical FPGA, etc.).
 *
 * Tests:
 *   - Write/read all registers with unique values
 *   - Reverse sweep verification
 *   - Last valid register
 *   - Boundary testing at the BAR limit
 */

#include "test_framework.h"
#include "device_helpers.h"

static DeviceConfig g_cfg;

int test_all_registers(void) {
    int fd = dvf_open_device(&g_cfg, O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    /* Phase 1: Write unique value to every register */
    for (int i = 0; i < g_cfg.reg_count; i++) {
        uint32_t val = 0xBEEF0000 | (uint32_t)i;
        ASSERT_EQ(dvf_write_reg(fd, i, val, &g_cfg), 0, "write failed during sweep");
    }

    /* Phase 2: Read back every register and verify */
    int err = 0;
    for (int i = 0; i < g_cfg.reg_count; i++) {
        uint32_t expected = 0xBEEF0000 | (uint32_t)i;
        uint32_t actual = dvf_read_reg(fd, i, &err, &g_cfg);
        ASSERT_EQ(err, 0, "read failed during sweep");
        ASSERT_EQ(actual, expected, "sweep readback mismatch");
    }

    close(fd);
    return 0;
}

int test_reverse_sweep(void) {
    int fd = dvf_open_device(&g_cfg, O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    /* Write in forward order */
    for (int i = 0; i < g_cfg.reg_count; i++) {
        uint32_t val = (uint32_t)(i * 7 + 3);  /* arbitrary pattern */
        ASSERT_EQ(dvf_write_reg(fd, i, val, &g_cfg), 0, "forward write failed");
    }

    /* Read in reverse order */
    int err = 0;
    for (int i = g_cfg.reg_count - 1; i >= 0; i--) {
        uint32_t expected = (uint32_t)(i * 7 + 3);
        uint32_t actual = dvf_read_reg(fd, i, &err, &g_cfg);
        ASSERT_EQ(err, 0, "reverse read failed");
        ASSERT_EQ(actual, expected, "reverse sweep readback mismatch");
    }

    close(fd);
    return 0;
}

int test_boundary_offset_read(void) {
    int fd = dvf_open_device(&g_cfg, O_RDONLY);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    /*
     * Try to read past the BAR boundary (offset >= bar_size).
     * The driver checks: if (*offset >= bar_size) return 0;
     * So read should return 0 bytes (EOF).
     */
    off_t boundary = g_cfg.bar_size;
    if (lseek(fd, boundary, SEEK_SET) == (off_t)-1) {
        /* lseek itself failing is also acceptable */
        close(fd);
        return 0;
    }

    uint32_t val = 0;
    ssize_t n = read(fd, &val, sizeof(val));
    /* Should return 0 (EOF) since offset >= bar_size */
    ASSERT_EQ(n, 0, "read beyond BAR should return 0 (EOF)");

    close(fd);
    return 0;
}

int test_boundary_offset_write(void) {
    int fd = dvf_open_device(&g_cfg, O_WRONLY);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    /*
     * Try to write past the BAR boundary (offset >= bar_size).
     * The driver checks: if (*offset >= bar_size) return -EFAULT;
     */
    off_t boundary = g_cfg.bar_size;
    if (lseek(fd, boundary, SEEK_SET) == (off_t)-1) {
        close(fd);
        return 0;
    }

    uint32_t val = 0x42;
    ssize_t n = write(fd, &val, sizeof(val));
    /* Should return -1 (EFAULT) or at least not 4 */
    ASSERT_TRUE(n != sizeof(val),
                "write beyond BAR should not succeed");

    close(fd);
    return 0;
}

int test_last_valid_register(void) {
    int fd = dvf_open_device(&g_cfg, O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    /* Last valid register */
    int last_reg = g_cfg.reg_count - 1;
    uint32_t val = 0xFEEDFACE;

    ASSERT_EQ(dvf_write_reg(fd, last_reg, val, &g_cfg), 0,
              "write to last register failed");

    int err = 0;
    uint32_t readback = dvf_read_reg(fd, last_reg, &err, &g_cfg);
    ASSERT_EQ(err, 0, "read from last register failed");
    ASSERT_EQ(readback, val, "last register readback mismatch");

    close(fd);
    return 0;
}

/* ---- Main ---- */

int main(void) {
    g_cfg = dvf_load_config();
    dvf_print_config(&g_cfg);

    TEST_SUITE_BEGIN("read_write/offset_sweep");

    RUN_TEST(test_all_registers);
    RUN_TEST(test_reverse_sweep);
    RUN_TEST(test_last_valid_register);
    RUN_TEST(test_boundary_offset_read);
    RUN_TEST(test_boundary_offset_write);

    TEST_SUITE_END();
}
