/*
 * test_boundaries.c — Error injection and boundary condition tests.
 *
 * Uses the generic DVF device API so this binary works against any
 * register-based device (QEMU GPGPU, physical FPGA, etc.).
 *
 * Tests:
 *   - Read beyond BAR boundary
 *   - Write beyond BAR boundary
 *   - Zero-length read/write
 *   - Device existence check
 */

#include "test_framework.h"
#include "device_helpers.h"

static DeviceConfig g_cfg;

int test_read_beyond_bar(void) {
    int fd = dvf_open_device(&g_cfg, O_RDONLY);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    /* Seek to offset at BAR boundary (beyond last valid register) */
    off_t pos = lseek(fd, g_cfg.bar_size, SEEK_SET);
    if (pos == (off_t)-1) {
        /* lseek failing is acceptable */
        close(fd);
        return 0;
    }

    uint32_t val = 0xDEAD;
    ssize_t n = read(fd, &val, sizeof(val));
    /* Driver returns 0 for offset >= bar_size */
    ASSERT_TRUE(n <= 0, "read beyond BAR should fail or return 0");

    close(fd);
    return 0;
}

int test_write_beyond_bar(void) {
    int fd = dvf_open_device(&g_cfg, O_WRONLY);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    off_t pos = lseek(fd, g_cfg.bar_size, SEEK_SET);
    if (pos == (off_t)-1) {
        close(fd);
        return 0;
    }

    uint32_t val = 0x42;
    ssize_t n = write(fd, &val, sizeof(val));
    ASSERT_TRUE(n != (ssize_t)sizeof(val),
                "write beyond BAR should not succeed normally");

    close(fd);
    return 0;
}

int test_read_large_offset(void) {
    int fd = dvf_open_device(&g_cfg, O_RDONLY);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    /* Try a very large offset */
    off_t pos = lseek(fd, 0x100000, SEEK_SET);
    if (pos == (off_t)-1) {
        close(fd);
        return 0;
    }

    uint32_t val = 0;
    ssize_t n = read(fd, &val, sizeof(val));
    ASSERT_TRUE(n <= 0, "read at large offset should fail or return 0");

    close(fd);
    return 0;
}

int test_zero_length_read(void) {
    int fd = dvf_open_device(&g_cfg, O_RDONLY);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    char buf;
    ssize_t n = read(fd, &buf, 0);
    /* Zero-length read should return 0 */
    ASSERT_EQ(n, 0, "zero-length read should return 0");

    close(fd);
    return 0;
}

int test_zero_length_write(void) {
    int fd = dvf_open_device(&g_cfg, O_WRONLY);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    char buf = 0;
    ssize_t n = write(fd, &buf, 0);
    /* Zero-length write should return 0 */
    ASSERT_EQ(n, 0, "zero-length write should return 0");

    close(fd);
    return 0;
}

int test_device_exists(void) {
    ASSERT_TRUE(dvf_device_exists(&g_cfg),
                "device node does not exist — is the driver loaded?");
    return 0;
}

int main(void) {
    g_cfg = dvf_load_config();
    dvf_print_config(&g_cfg);

    TEST_SUITE_BEGIN("error_injection/boundaries");
    RUN_TEST(test_device_exists);
    RUN_TEST(test_read_beyond_bar);
    RUN_TEST(test_write_beyond_bar);
    RUN_TEST(test_read_large_offset);
    RUN_TEST(test_zero_length_read);
    RUN_TEST(test_zero_length_write);
    TEST_SUITE_END();
}
