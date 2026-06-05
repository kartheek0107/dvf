/*
 * test_throughput.c — Stress and throughput benchmarks for the GPGPU device.
 *
 * Tests:
 *   - Write throughput (ops/sec)
 *   - Read throughput (ops/sec)
 *   - Sustained mixed load
 */

#include "test_framework.h"
#include "device_helpers.h"

#define BENCH_ITERATIONS 10000
#define SUSTAINED_SECS   3

int test_write_throughput(void) {
    int fd = gpgpu_open_device(O_WRONLY);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    struct timespec start, end;
    clock_gettime(CLOCK_MONOTONIC, &start);

    for (int i = 0; i < BENCH_ITERATIONS; i++) {
        uint32_t val = (uint32_t)i;
        gpgpu_write_reg(fd, i % GPGPU_REG_COUNT, val);
    }

    clock_gettime(CLOCK_MONOTONIC, &end);
    double elapsed_ms = timespec_diff_ms(&start, &end);
    double ops_per_sec = (BENCH_ITERATIONS / elapsed_ms) * 1000.0;

    fprintf(stderr, "    write throughput: %.0f ops/sec (%.1fms for %d ops)\n",
            ops_per_sec, elapsed_ms, BENCH_ITERATIONS);

    ASSERT_TRUE(ops_per_sec > 0, "throughput should be positive");
    close(fd);
    return 0;
}

int test_read_throughput(void) {
    int fd = gpgpu_open_device(O_RDONLY);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    struct timespec start, end;
    clock_gettime(CLOCK_MONOTONIC, &start);

    int err = 0;
    for (int i = 0; i < BENCH_ITERATIONS; i++) {
        gpgpu_read_reg(fd, i % GPGPU_REG_COUNT, &err);
    }

    clock_gettime(CLOCK_MONOTONIC, &end);
    double elapsed_ms = timespec_diff_ms(&start, &end);
    double ops_per_sec = (BENCH_ITERATIONS / elapsed_ms) * 1000.0;

    fprintf(stderr, "    read throughput: %.0f ops/sec (%.1fms for %d ops)\n",
            ops_per_sec, elapsed_ms, BENCH_ITERATIONS);

    ASSERT_EQ(err, 0, "read errors during throughput test");
    ASSERT_TRUE(ops_per_sec > 0, "throughput should be positive");
    close(fd);
    return 0;
}

int test_sustained_load(void) {
    int fd = gpgpu_open_device(O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device");

    struct timespec start, now;
    clock_gettime(CLOCK_MONOTONIC, &start);

    long write_ops = 0, read_ops = 0;
    int err = 0;

    while (1) {
        /* Write */
        gpgpu_write_reg(fd, (int)(write_ops % GPGPU_REG_COUNT), (uint32_t)write_ops);
        write_ops++;

        /* Read */
        gpgpu_read_reg(fd, (int)(read_ops % GPGPU_REG_COUNT), &err);
        read_ops++;

        /* Check time every 1000 ops */
        if ((write_ops % 1000) == 0) {
            clock_gettime(CLOCK_MONOTONIC, &now);
            double elapsed = timespec_diff_ms(&start, &now);
            if (elapsed >= SUSTAINED_SECS * 1000.0)
                break;
        }
    }

    clock_gettime(CLOCK_MONOTONIC, &now);
    double elapsed_ms = timespec_diff_ms(&start, &now);
    double total_ops = (double)(write_ops + read_ops);
    double ops_per_sec = (total_ops / elapsed_ms) * 1000.0;

    fprintf(stderr, "    sustained: %ld writes + %ld reads in %.1fms = %.0f ops/sec\n",
            write_ops, read_ops, elapsed_ms, ops_per_sec);

    ASSERT_EQ(err, 0, "read errors during sustained load");
    ASSERT_TRUE(write_ops > 100, "too few write ops completed");
    close(fd);
    return 0;
}

int main(void) {
    TEST_SUITE_BEGIN("stress_performance/throughput");
    RUN_TEST(test_write_throughput);
    RUN_TEST(test_read_throughput);
    RUN_TEST(test_sustained_load);
    TEST_SUITE_END();
}
