/*
 * test_patterns.c — Data integrity tests using bit patterns.
 *
 * Uses the generic DVF device API so this binary works against any
 * register-based device (QEMU GPGPU, physical FPGA, etc.).
 *
 * Tests:
 *   - Walking ones pattern
 *   - Walking zeros pattern
 *   - Alternating 0xAAAAAAAA / 0x55555555
 *   - Checkerboard pattern across the register file
 *   - Walking ones across multiple registers
 */

#include "test_framework.h"
#include "device_helpers.h"

static DeviceConfig g_cfg;

int test_walking_ones(void) {
    int fd = dvf_open_device(&g_cfg, O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    int err = 0;
    /* Test walking-1 pattern on register 0: bit 0 through bit 31 */
    for (int bit = 0; bit < 32; bit++) {
        uint32_t pattern = 1U << bit;
        ASSERT_EQ(dvf_write_reg(fd, 0, pattern, &g_cfg), 0, "walking-1 write failed");
        uint32_t readback = dvf_read_reg(fd, 0, &err, &g_cfg);
        ASSERT_EQ(err, 0, "walking-1 read failed");
        ASSERT_EQ(readback, pattern, "walking-1 pattern mismatch");
    }

    close(fd);
    return 0;
}

int test_walking_zeros(void) {
    int fd = dvf_open_device(&g_cfg, O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    int err = 0;
    /* Test walking-0 pattern on register 0 */
    for (int bit = 0; bit < 32; bit++) {
        uint32_t pattern = ~(1U << bit);
        ASSERT_EQ(dvf_write_reg(fd, 0, pattern, &g_cfg), 0, "walking-0 write failed");
        uint32_t readback = dvf_read_reg(fd, 0, &err, &g_cfg);
        ASSERT_EQ(err, 0, "walking-0 read failed");
        ASSERT_EQ(readback, pattern, "walking-0 pattern mismatch");
    }

    close(fd);
    return 0;
}

int test_alternating_pattern(void) {
    int fd = dvf_open_device(&g_cfg, O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    int err = 0;
    /* Write alternating patterns to first 32 registers (or fewer if device is smaller) */
    int count = (g_cfg.reg_count < 32) ? g_cfg.reg_count : 32;
    for (int i = 0; i < count; i++) {
        uint32_t pattern = (i % 2 == 0) ? 0xAAAAAAAA : 0x55555555;
        ASSERT_EQ(dvf_write_reg(fd, i, pattern, &g_cfg), 0, "alternating write failed");
    }

    /* Read back and verify */
    for (int i = 0; i < count; i++) {
        uint32_t expected = (i % 2 == 0) ? 0xAAAAAAAA : 0x55555555;
        uint32_t actual = dvf_read_reg(fd, i, &err, &g_cfg);
        ASSERT_EQ(err, 0, "alternating read failed");
        ASSERT_EQ(actual, expected, "alternating pattern mismatch");
    }

    close(fd);
    return 0;
}

int test_checkerboard(void) {
    int fd = dvf_open_device(&g_cfg, O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    int err = 0;
    /*
     * Checkerboard: write all-ones to even registers,
     * all-zeros to odd registers.
     */
    int count = (g_cfg.reg_count < 64) ? g_cfg.reg_count : 64;
    for (int i = 0; i < count; i++) {
        uint32_t val = (i % 2 == 0) ? 0xFFFFFFFF : 0x00000000;
        ASSERT_EQ(dvf_write_reg(fd, i, val, &g_cfg), 0, "checkerboard write failed");
    }

    /* Verify */
    for (int i = 0; i < count; i++) {
        uint32_t expected = (i % 2 == 0) ? 0xFFFFFFFF : 0x00000000;
        uint32_t actual = dvf_read_reg(fd, i, &err, &g_cfg);
        ASSERT_EQ(err, 0, "checkerboard read failed");
        ASSERT_EQ(actual, expected, "checkerboard mismatch");
    }

    close(fd);
    return 0;
}

int test_walking_ones_all_regs(void) {
    int fd = dvf_open_device(&g_cfg, O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    int err = 0;
    /* Write walking-1 across first 32 registers (or fewer if device is smaller) */
    int count = (g_cfg.reg_count < 32) ? g_cfg.reg_count : 32;
    for (int i = 0; i < count; i++) {
        uint32_t pattern = 1U << i;
        ASSERT_EQ(dvf_write_reg(fd, i, pattern, &g_cfg), 0, "walking-1 multi-reg write failed");
    }

    /* Verify all at once */
    for (int i = 0; i < count; i++) {
        uint32_t expected = 1U << i;
        uint32_t actual = dvf_read_reg(fd, i, &err, &g_cfg);
        ASSERT_EQ(err, 0, "walking-1 multi-reg read failed");
        ASSERT_EQ(actual, expected, "walking-1 multi-reg mismatch");
    }

    close(fd);
    return 0;
}

/* ---- Main ---- */

int main(void) {
    g_cfg = dvf_load_config();
    dvf_print_config(&g_cfg);

    TEST_SUITE_BEGIN("data_integrity/patterns");

    RUN_TEST(test_walking_ones);
    RUN_TEST(test_walking_zeros);
    RUN_TEST(test_alternating_pattern);
    RUN_TEST(test_checkerboard);
    RUN_TEST(test_walking_ones_all_regs);

    TEST_SUITE_END();
}
