/*
 * test_framework.h — Lightweight test framework for DVF driver validation.
 *
 * Provides:
 *   - TEST_SUITE_BEGIN / TEST_SUITE_END for suite lifecycle
 *   - RUN_TEST() macro to run individual test functions
 *   - ASSERT_EQ / ASSERT_NEQ / ASSERT_TRUE / ASSERT_FALSE for assertions
 *   - Structured JSON output per test case
 *   - Timing and result aggregation
 *
 * Each test function has the signature:  int test_name(void)
 *   Return 0 for pass, non-zero for fail.
 *
 * Usage:
 *   int test_something(void) {
 *       int fd = gpgpu_open_device();
 *       ASSERT_TRUE(fd >= 0, "device open failed");
 *       uint32_t val = 42;
 *       gpgpu_write_reg(fd, 0, val);
 *       ASSERT_EQ(gpgpu_read_reg(fd, 0), val, "readback mismatch");
 *       close(fd);
 *       return 0;
 *   }
 *
 *   int main(void) {
 *       TEST_SUITE_BEGIN("read_write");
 *       RUN_TEST(test_something);
 *       TEST_SUITE_END();
 *   }
 */

#ifndef DVF_TEST_FRAMEWORK_H
#define DVF_TEST_FRAMEWORK_H

#ifdef __cplusplus
extern "C" {
#endif

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <signal.h>
#include <setjmp.h>

/* --- Result tracking --- */

typedef struct {
    const char *test_name;
    const char *suite_name;
    int         passed;       /* 1 = pass, 0 = fail */
    double      duration_ms;
    char        message[512]; /* failure reason or info */
} TestResult;

typedef struct {
    const char  *suite_name;
    TestResult   results[256];
    int          count;
    int          passed;
    int          failed;
    double       total_duration_ms;
} TestSuite;

/* Global suite state */
extern TestSuite g_suite;
extern jmp_buf   g_test_jmp;
extern int       g_test_failed;
extern char      g_fail_msg[512];

/* --- Timing helpers --- */

static inline double timespec_diff_ms(struct timespec *start, struct timespec *end) {
    return (end->tv_sec - start->tv_sec) * 1000.0 +
           (end->tv_nsec - start->tv_nsec) / 1e6;
}

/* --- Suite lifecycle --- */

void test_suite_begin(const char *name);
void test_suite_end(void);
void test_suite_print_json(void);
void test_suite_print_summary(void);

/* Install signal handlers for SIGSEGV, SIGBUS, SIGFPE */
void test_install_signal_handlers(void);

/* --- Macros --- */

#define TEST_SUITE_BEGIN(name)  \
    test_install_signal_handlers(); \
    test_suite_begin(name)

#define TEST_SUITE_END() \
    test_suite_print_json(); \
    test_suite_print_summary(); \
    return (g_suite.failed > 0) ? 1 : 0

/*
 * RUN_TEST(func) — runs a test function and records the result.
 * The function must return 0 for pass, non-zero for fail.
 */
#define RUN_TEST(func) do { \
    struct timespec _ts_start, _ts_end; \
    g_test_failed = 0; \
    g_fail_msg[0] = '\0'; \
    fprintf(stderr, "  running: %s ... ", #func); \
    clock_gettime(CLOCK_MONOTONIC, &_ts_start); \
    int _sigval = setjmp(g_test_jmp); \
    if (_sigval == 0) { \
        int _rc = func(); \
        if (_rc != 0 && !g_test_failed) { \
            g_test_failed = 1; \
            if (g_fail_msg[0] == '\0') \
                snprintf(g_fail_msg, sizeof(g_fail_msg), \
                         "returned non-zero: %d", _rc); \
        } \
    } else { \
        g_test_failed = 1; \
        snprintf(g_fail_msg, sizeof(g_fail_msg), \
                 "caught signal %d", _sigval); \
    } \
    clock_gettime(CLOCK_MONOTONIC, &_ts_end); \
    double _dur = timespec_diff_ms(&_ts_start, &_ts_end); \
    TestResult *_r = &g_suite.results[g_suite.count]; \
    _r->test_name = #func; \
    _r->suite_name = g_suite.suite_name; \
    _r->passed = !g_test_failed; \
    _r->duration_ms = _dur; \
    memcpy(_r->message, g_fail_msg, sizeof(_r->message)); \
    _r->message[sizeof(_r->message) - 1] = '\0'; \
    g_suite.count++; \
    g_suite.total_duration_ms += _dur; \
    if (_r->passed) { \
        g_suite.passed++; \
        fprintf(stderr, "PASS (%.1fms)\n", _dur); \
    } else { \
        g_suite.failed++; \
        fprintf(stderr, "FAIL (%.1fms): %s\n", _dur, _r->message); \
    } \
} while(0)

/* --- Assertion macros --- */

#define ASSERT_TRUE(cond, msg) do { \
    if (!(cond)) { \
        g_test_failed = 1; \
        snprintf(g_fail_msg, sizeof(g_fail_msg), \
                 "ASSERT_TRUE failed at %s:%d: %s", \
                 __FILE__, __LINE__, (msg)); \
        return 1; \
    } \
} while(0)

#define ASSERT_FALSE(cond, msg) ASSERT_TRUE(!(cond), msg)

#define ASSERT_EQ(actual, expected, msg) do { \
    unsigned long _a = (unsigned long)(actual); \
    unsigned long _e = (unsigned long)(expected); \
    if (_a != _e) { \
        g_test_failed = 1; \
        snprintf(g_fail_msg, sizeof(g_fail_msg), \
                 "ASSERT_EQ failed at %s:%d: got 0x%lx, expected 0x%lx — %s", \
                 __FILE__, __LINE__, _a, _e, (msg)); \
        return 1; \
    } \
} while(0)

#define ASSERT_NEQ(actual, not_expected, msg) do { \
    unsigned long _a = (unsigned long)(actual); \
    unsigned long _ne = (unsigned long)(not_expected); \
    if (_a == _ne) { \
        g_test_failed = 1; \
        snprintf(g_fail_msg, sizeof(g_fail_msg), \
                 "ASSERT_NEQ failed at %s:%d: got 0x%lx (should differ) — %s", \
                 __FILE__, __LINE__, _a, (msg)); \
        return 1; \
    } \
} while(0)

#define ASSERT_GE(actual, min_val, msg) do { \
    long _a = (long)(actual); \
    long _m = (long)(min_val); \
    if (_a < _m) { \
        g_test_failed = 1; \
        snprintf(g_fail_msg, sizeof(g_fail_msg), \
                 "ASSERT_GE failed at %s:%d: got %ld, expected >= %ld — %s", \
                 __FILE__, __LINE__, _a, _m, (msg)); \
        return 1; \
    } \
} while(0)

#ifdef __cplusplus
} /* extern "C" */
#endif

#endif /* DVF_TEST_FRAMEWORK_H */
