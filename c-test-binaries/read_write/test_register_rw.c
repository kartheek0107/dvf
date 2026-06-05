/*
 * test_register_rw.c — Core register read/write validation for the GPGPU device.
 *
 * Tests:
 *   - Write and read back a known value
 *   - Maximum value (0xFFFFFFFF)
 *   - Zero value
 *   - Known patterns (0xDEADBEEF, 0xCAFEBABE)
 *   - Sequential register writes
 *   - Register independence (writing one doesn't corrupt another)
 */

#include "test_framework.h"
#include "device_helpers.h"

/* ---- Tests ---- */

int test_write_read_register_0(void) {
    int fd = gpgpu_open_device(O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    uint32_t write_val = 999999999;
    ASSERT_EQ(gpgpu_write_reg(fd, 0, write_val), 0, "write failed");

    int err = 0;
    uint32_t read_val = gpgpu_read_reg(fd, 0, &err);
    ASSERT_EQ(err, 0, "read failed");
    ASSERT_EQ(read_val, write_val, "readback mismatch on register 0");

    close(fd);
    return 0;
}

int test_write_read_max_value(void) {
    int fd = gpgpu_open_device(O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    uint32_t write_val = 0xFFFFFFFF;
    ASSERT_EQ(gpgpu_write_reg(fd, 0, write_val), 0, "write max failed");

    int err = 0;
    uint32_t read_val = gpgpu_read_reg(fd, 0, &err);
    ASSERT_EQ(err, 0, "read failed");
    ASSERT_EQ(read_val, write_val, "max value readback mismatch");

    close(fd);
    return 0;
}

int test_write_read_zero(void) {
    int fd = gpgpu_open_device(O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    /* Write non-zero first, then zero, verify zero sticks */
    ASSERT_EQ(gpgpu_write_reg(fd, 0, 0xDEADBEEF), 0, "setup write failed");
    ASSERT_EQ(gpgpu_write_reg(fd, 0, 0), 0, "zero write failed");

    int err = 0;
    uint32_t read_val = gpgpu_read_reg(fd, 0, &err);
    ASSERT_EQ(err, 0, "read failed");
    ASSERT_EQ(read_val, 0, "zero readback mismatch");

    close(fd);
    return 0;
}

int test_write_read_known_patterns(void) {
    int fd = gpgpu_open_device(O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    uint32_t patterns[] = {0xDEADBEEF, 0xCAFEBABE, 0x12345678, 0xA5A5A5A5};
    int err = 0;

    for (int i = 0; i < 4; i++) {
        ASSERT_EQ(gpgpu_write_reg(fd, 0, patterns[i]), 0, "pattern write failed");
        uint32_t val = gpgpu_read_reg(fd, 0, &err);
        ASSERT_EQ(err, 0, "pattern read failed");
        ASSERT_EQ(val, patterns[i], "pattern readback mismatch");
    }

    close(fd);
    return 0;
}

int test_sequential_registers(void) {
    int fd = gpgpu_open_device(O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    /* Write unique values to first 16 registers */
    for (int i = 0; i < 16; i++) {
        uint32_t val = 0x1000 + i;
        ASSERT_EQ(gpgpu_write_reg(fd, i, val), 0, "sequential write failed");
    }

    /* Read them all back */
    int err = 0;
    for (int i = 0; i < 16; i++) {
        uint32_t expected = 0x1000 + i;
        uint32_t actual = gpgpu_read_reg(fd, i, &err);
        ASSERT_EQ(err, 0, "sequential read failed");
        ASSERT_EQ(actual, expected, "sequential readback mismatch");
    }

    close(fd);
    return 0;
}

int test_register_independence(void) {
    int fd = gpgpu_open_device(O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    /* Write to reg 0 and reg 1 with different values */
    ASSERT_EQ(gpgpu_write_reg(fd, 0, 0xAAAAAAAA), 0, "write reg 0 failed");
    ASSERT_EQ(gpgpu_write_reg(fd, 1, 0x55555555), 0, "write reg 1 failed");

    /* Verify reg 0 wasn't corrupted by writing reg 1 */
    int err = 0;
    uint32_t val0 = gpgpu_read_reg(fd, 0, &err);
    ASSERT_EQ(err, 0, "read reg 0 failed");
    ASSERT_EQ(val0, 0xAAAAAAAA, "reg 0 corrupted after writing reg 1");

    uint32_t val1 = gpgpu_read_reg(fd, 1, &err);
    ASSERT_EQ(err, 0, "read reg 1 failed");
    ASSERT_EQ(val1, 0x55555555, "reg 1 value wrong");

    /* Now overwrite reg 1, verify reg 0 still intact */
    ASSERT_EQ(gpgpu_write_reg(fd, 1, 0x11111111), 0, "overwrite reg 1 failed");
    val0 = gpgpu_read_reg(fd, 0, &err);
    ASSERT_EQ(err, 0, "re-read reg 0 failed");
    ASSERT_EQ(val0, 0xAAAAAAAA, "reg 0 corrupted after overwriting reg 1");

    close(fd);
    return 0;
}

/* ---- Main ---- */

int main(void) {
    TEST_SUITE_BEGIN("read_write/register_rw");

    RUN_TEST(test_write_read_register_0);
    RUN_TEST(test_write_read_max_value);
    RUN_TEST(test_write_read_zero);
    RUN_TEST(test_write_read_known_patterns);
    RUN_TEST(test_sequential_registers);
    RUN_TEST(test_register_independence);

    TEST_SUITE_END();
}
