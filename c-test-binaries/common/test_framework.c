/*
 * test_framework.c — Implementation of the DVF test framework.
 */

#include "test_framework.h"

/* Global state */
TestSuite g_suite;
jmp_buf   g_test_jmp;
int       g_test_failed = 0;
char      g_fail_msg[512] = {0};

/* --- Signal handling --- */

static void signal_handler(int sig) {
    /* Jump back to the RUN_TEST setjmp with the signal number */
    longjmp(g_test_jmp, sig);
}

void test_install_signal_handlers(void) {
    struct sigaction sa;
    memset(&sa, 0, sizeof(sa));
    sa.sa_handler = signal_handler;
    sigemptyset(&sa.sa_mask);

    sigaction(SIGSEGV, &sa, NULL);
    sigaction(SIGBUS,  &sa, NULL);
    sigaction(SIGFPE,  &sa, NULL);
    sigaction(SIGABRT, &sa, NULL);
}

/* --- Suite lifecycle --- */

void test_suite_begin(const char *name) {
    memset(&g_suite, 0, sizeof(g_suite));
    g_suite.suite_name = name;
    fprintf(stderr, "\n=== Test Suite: %s ===\n", name);
}

/* --- JSON output (to stdout for machine parsing) --- */

static void json_escape(const char *s, char *out, size_t out_sz) {
    size_t j = 0;
    for (size_t i = 0; s[i] && j < out_sz - 2; i++) {
        switch (s[i]) {
            case '"':  out[j++] = '\\'; out[j++] = '"';  break;
            case '\\': out[j++] = '\\'; out[j++] = '\\'; break;
            case '\n': out[j++] = '\\'; out[j++] = 'n';  break;
            case '\t': out[j++] = '\\'; out[j++] = 't';  break;
            default:   out[j++] = s[i]; break;
        }
    }
    out[j] = '\0';
}

void test_suite_print_json(void) {
    char escaped_msg[1024];

    printf("{\"suite\": \"%s\", \"results\": [\n", g_suite.suite_name);
    for (int i = 0; i < g_suite.count; i++) {
        TestResult *r = &g_suite.results[i];
        json_escape(r->message, escaped_msg, sizeof(escaped_msg));
        printf("  {\"test\": \"%s\", \"status\": \"%s\", "
               "\"duration_ms\": %.2f, \"message\": \"%s\"}%s\n",
               r->test_name,
               r->passed ? "PASS" : "FAIL",
               r->duration_ms,
               escaped_msg,
               (i < g_suite.count - 1) ? "," : "");
    }
    printf("], \"summary\": {\"total\": %d, \"passed\": %d, "
           "\"failed\": %d, \"duration_ms\": %.2f}}\n",
           g_suite.count, g_suite.passed, g_suite.failed,
           g_suite.total_duration_ms);
    fflush(stdout);
}

/* --- Human-readable summary (to stderr) --- */

void test_suite_print_summary(void) {
    fprintf(stderr, "\n--- %s: %d/%d passed",
            g_suite.suite_name, g_suite.passed, g_suite.count);
    if (g_suite.failed > 0)
        fprintf(stderr, " (%d FAILED)", g_suite.failed);
    fprintf(stderr, " [%.1fms] ---\n\n", g_suite.total_duration_ms);
}

void test_suite_end(void) {
    /* no-op — cleanup if needed in the future */
}
