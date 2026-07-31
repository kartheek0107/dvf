/*
 * test_concurrent_rw.c — Multi-threaded concurrency tests.
 *
 * Uses the generic DVF device API so this binary works against any
 * register-based device (QEMU GPGPU, physical FPGA, etc.).
 *
 * Tests:
 *   - Writer/reader race on the same register
 *   - Multiple writers to the same register
 *   - Concurrent writes to different registers
 */

#include "test_framework.h"
#include "device_helpers.h"
#include <pthread.h>
#include <stdatomic.h>

#define ITERATIONS  5000
#define DURATION_MS 1000

static DeviceConfig g_cfg;
static atomic_int g_stop = 0;
static atomic_int g_errors = 0;

/* --- Writer/Reader race --- */

// testing 

static void *writer_func(void *arg) {
    (void)arg;
    int fd = dvf_open_device(&g_cfg, O_RDWR);
    if (fd < 0) { atomic_fetch_add(&g_errors, 1); return NULL; }

    uint32_t count = 0;
    while (!atomic_load(&g_stop)) {
        uint32_t val = 100 + (count % 100);
        dvf_write_reg(fd, 0, val, &g_cfg);
        count++;
    }
    close(fd);
    return NULL;
}

static void *reader_func(void *arg) {
    (void)arg;
    int fd = dvf_open_device(&g_cfg, O_RDONLY);
    if (fd < 0) { atomic_fetch_add(&g_errors, 1); return NULL; }

    while (!atomic_load(&g_stop)) {
        int err = 0;
        dvf_read_reg(fd, 0, &err, &g_cfg);
        if (err) atomic_fetch_add(&g_errors, 1);
    }
    close(fd);
    return NULL;
}

int test_writer_reader_race(void) {
    atomic_store(&g_stop, 0);
    atomic_store(&g_errors, 0);

    pthread_t writer, reader;
    ASSERT_EQ(pthread_create(&writer, NULL, writer_func, NULL), 0, "writer create failed");
    ASSERT_EQ(pthread_create(&reader, NULL, reader_func, NULL), 0, "reader create failed");

    /* Let them race for DURATION_MS */
    usleep(DURATION_MS * 1000);
    atomic_store(&g_stop, 1);

    pthread_join(writer, NULL);
    pthread_join(reader, NULL);

    ASSERT_EQ(atomic_load(&g_errors), 0, "errors during concurrent read/write");
    return 0;
}

/* --- Multiple writers to same register --- */

static void *multi_writer_func(void *arg) {
    int thread_id = *(int *)arg;
    int fd = dvf_open_device(&g_cfg, O_RDWR);
    if (fd < 0) { atomic_fetch_add(&g_errors, 1); return NULL; }

    for (int i = 0; i < ITERATIONS && !atomic_load(&g_stop); i++) {
        uint32_t val = (uint32_t)((thread_id << 16) | i);
        if (dvf_write_reg(fd, 0, val, &g_cfg) != 0)
            atomic_fetch_add(&g_errors, 1);
    }
    close(fd);
    return NULL;
}

int test_multi_writer(void) {
    atomic_store(&g_stop, 0);
    atomic_store(&g_errors, 0);

    int ids[4] = {0, 1, 2, 3};
    pthread_t threads[4];

    for (int i = 0; i < 4; i++)
        ASSERT_EQ(pthread_create(&threads[i], NULL, multi_writer_func, &ids[i]),
                  0, "multi-writer thread create failed");

    for (int i = 0; i < 4; i++)
        pthread_join(threads[i], NULL);

    ASSERT_EQ(atomic_load(&g_errors), 0, "errors during multi-writer");

    /* After all writes complete, read reg 0 — should contain SOME valid value */
    int fd = dvf_open_device(&g_cfg, O_RDONLY);
    ASSERT_TRUE(fd >= 0, "post-write read open failed");
    int err = 0;
    dvf_read_reg(fd, 0, &err, &g_cfg);
    ASSERT_EQ(err, 0, "post-write read failed");
    close(fd);

    return 0;
}

/* --- Concurrent writes to different registers --- */

typedef struct {
    int reg_idx;
    uint32_t value;
} RegTask;

static void *reg_writer_func(void *arg) {
    RegTask *task = (RegTask *)arg;
    int fd = dvf_open_device(&g_cfg, O_RDWR);
    if (fd < 0) { atomic_fetch_add(&g_errors, 1); return NULL; }

    for (int i = 0; i < ITERATIONS; i++) {
        if (dvf_write_reg(fd, task->reg_idx, task->value + (uint32_t)i, &g_cfg) != 0)
            atomic_fetch_add(&g_errors, 1);
    }
    /* Write final known value for verification */
    dvf_write_reg(fd, task->reg_idx, task->value, &g_cfg);
    close(fd);
    return NULL;
}

int test_multi_register_concurrent(void) {
    atomic_store(&g_errors, 0);

    RegTask tasks[4] = {
        {.reg_idx = 50, .value = 0xAA000000},
        {.reg_idx = 51, .value = 0xBB000000},
        {.reg_idx = 52, .value = 0xCC000000},
        {.reg_idx = 53, .value = 0xDD000000},
    };
    pthread_t threads[4];

    for (int i = 0; i < 4; i++)
        ASSERT_EQ(pthread_create(&threads[i], NULL, reg_writer_func, &tasks[i]),
                  0, "reg writer thread create failed");

    for (int i = 0; i < 4; i++)
        pthread_join(threads[i], NULL);

    ASSERT_EQ(atomic_load(&g_errors), 0, "errors during multi-reg concurrent write");

    /* Verify each register has the correct final value */
    int fd = dvf_open_device(&g_cfg, O_RDONLY);
    ASSERT_TRUE(fd >= 0, "verification open failed");
    int err = 0;
    for (int i = 0; i < 4; i++) {
        uint32_t actual = dvf_read_reg(fd, tasks[i].reg_idx, &err, &g_cfg);
        ASSERT_EQ(err, 0, "verification read failed");
        ASSERT_EQ(actual, tasks[i].value, "concurrent reg final value mismatch");
    }
    close(fd);

    return 0;
}

int main(void) {
    g_cfg = dvf_load_config();
    dvf_print_config(&g_cfg);

    TEST_SUITE_BEGIN("concurrency/concurrent_rw");
    RUN_TEST(test_writer_reader_race);
    RUN_TEST(test_multi_writer);
    RUN_TEST(test_multi_register_concurrent);
    TEST_SUITE_END();
}
