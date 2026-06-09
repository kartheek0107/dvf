/*
 * test_persistence.c — Data persistence and multi-fd visibility tests.
 *
 * Uses the generic DVF device API so this binary works against any
 * register-based device (QEMU GPGPU, physical FPGA, etc.).
 */

#include "test_framework.h"
#include "device_helpers.h"

static DeviceConfig g_cfg;

int test_value_persists_across_close(void) {
    uint32_t write_val = 0xBAADF00D;
    {
        int fd = dvf_open_device(&g_cfg, O_RDWR);
        ASSERT_TRUE(fd >= 0, "failed to open device for write");
        ASSERT_EQ(dvf_write_reg(fd, 5, write_val, &g_cfg), 0, "write failed");
        close(fd);
    }
    {
        int fd = dvf_open_device(&g_cfg, O_RDONLY);
        ASSERT_TRUE(fd >= 0, "failed to reopen device for read");
        int err = 0;
        uint32_t read_val = dvf_read_reg(fd, 5, &err, &g_cfg);
        ASSERT_EQ(err, 0, "read failed");
        ASSERT_EQ(read_val, write_val, "value did not persist across close/reopen");
        close(fd);
    }
    return 0;
}

int test_multi_fd_visibility(void) {
    int fd1 = dvf_open_device(&g_cfg, O_RDWR);
    ASSERT_TRUE(fd1 >= 0, "failed to open fd1");
    int fd2 = dvf_open_device(&g_cfg, O_RDWR);
    ASSERT_TRUE(fd2 >= 0, "failed to open fd2");

    uint32_t val = 0x42424242;
    ASSERT_EQ(dvf_write_reg(fd1, 10, val, &g_cfg), 0, "fd1 write failed");

    int err = 0;
    uint32_t read_val = dvf_read_reg(fd2, 10, &err, &g_cfg);
    ASSERT_EQ(err, 0, "fd2 read failed");
    ASSERT_EQ(read_val, val, "fd2 did not see fd1's write");

    uint32_t val2 = 0x13371337;
    ASSERT_EQ(dvf_write_reg(fd2, 10, val2, &g_cfg), 0, "fd2 write failed");
    read_val = dvf_read_reg(fd1, 10, &err, &g_cfg);
    ASSERT_EQ(err, 0, "fd1 re-read failed");
    ASSERT_EQ(read_val, val2, "fd1 did not see fd2's write");

    close(fd1);
    close(fd2);
    return 0;
}

int test_multiple_registers_persist(void) {
    uint32_t values[8] = {
        0x11111111, 0x22222222, 0x33333333, 0x44444444,
        0x55555555, 0x66666666, 0x77777777, 0x88888888
    };
    {
        int fd = dvf_open_device(&g_cfg, O_RDWR);
        ASSERT_TRUE(fd >= 0, "failed to open for multi-write");
        for (int i = 0; i < 8; i++)
            ASSERT_EQ(dvf_write_reg(fd, 20 + i, values[i], &g_cfg), 0, "multi-write failed");
        close(fd);
    }
    {
        int fd = dvf_open_device(&g_cfg, O_RDONLY);
        ASSERT_TRUE(fd >= 0, "failed to reopen for multi-read");
        int err = 0;
        for (int i = 0; i < 8; i++) {
            uint32_t actual = dvf_read_reg(fd, 20 + i, &err, &g_cfg);
            ASSERT_EQ(err, 0, "multi-read failed");
            ASSERT_EQ(actual, values[i], "multi-register persistence mismatch");
        }
        close(fd);
    }
    return 0;
}

int main(void) {
    g_cfg = dvf_load_config();
    dvf_print_config(&g_cfg);

    TEST_SUITE_BEGIN("data_integrity/persistence");
    RUN_TEST(test_value_persists_across_close);
    RUN_TEST(test_multi_fd_visibility);
    RUN_TEST(test_multiple_registers_persist);
    TEST_SUITE_END();
}
