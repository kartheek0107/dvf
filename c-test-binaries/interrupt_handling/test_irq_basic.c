/*
 * test_irq_basic.c — Basic IRQ / interrupt validation tests.
 *
 * Uses the generic DVF device API. Works against any register-based device
 * that exposes its IRQ in /proc/interrupts (QEMU GPGPU, physical FPGA, etc.).
 *
 * Tests:
 *   test_device_exists      — driver is loaded and /dev node is accessible
 *   test_irq_registered     — device IRQ line appears in /proc/interrupts
 *   test_irq_count_increments — writing a trigger register bumps the IRQ count
 *   test_irq_no_storm       — IRQ rate stays below a sanity threshold
 *
 * Environment variables (from dvf_load_config / device_config.h):
 *   DVF_DEVICE_PATH  — device node (default: /dev/gpgpu)
 *   DVF_IRQ_NAME     — string to grep for in /proc/interrupts (default: "gpgpu")
 *   DVF_IRQ_TRIGGER_REG — register index that triggers an IRQ when written
 *                         (default: 0; set to -1 to skip increment test)
 */

#include "test_framework.h"
#include "device_helpers.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <time.h>

/* ---------------------------------------------------------------------------
 * Helpers
 * --------------------------------------------------------------------------- */

#define IRQ_NAME_MAX 64
#define PROC_INTERRUPTS "/proc/interrupts"

/* Read the IRQ count for a named device from /proc/interrupts.
 * Returns the sum of per-CPU counts for the matching line,
 * or -1 if the device is not found. */
static long read_irq_count(const char *irq_name) {
    FILE *f = fopen(PROC_INTERRUPTS, "r");
    if (!f) return -1;

    char line[1024];
    long total = -1;

    while (fgets(line, sizeof(line), f)) {
        /* Match by IRQ name substring anywhere on the line */
        if (strstr(line, irq_name) == NULL)
            continue;

        /* Parse: skip the "  123:" prefix, then sum all numeric columns */
        const char *p = line;
        /* Skip leading spaces and the IRQ number + colon */
        while (*p == ' ') p++;
        while (*p && *p != ':') p++;
        if (*p == ':') p++;

        total = 0;
        char *endptr;
        while (1) {
            long count = strtol(p, &endptr, 10);
            if (endptr == p) break;   /* no more numbers */
            total += count;
            p = endptr;
        }
        break;
    }

    fclose(f);
    return total;
}

/* ---------------------------------------------------------------------------
 * Global config
 * --------------------------------------------------------------------------- */

static DeviceConfig g_cfg;
static char g_irq_name[IRQ_NAME_MAX];
static int  g_trigger_reg;

/* ---------------------------------------------------------------------------
 * Tests
 * --------------------------------------------------------------------------- */

int test_device_exists(void) {
    ASSERT_TRUE(dvf_device_exists(&g_cfg),
                "device node does not exist — is the driver loaded?");
    return 0;
}

int test_irq_registered(void) {
    long count = read_irq_count(g_irq_name);
    ASSERT_TRUE(count >= 0,
                "device IRQ not found in /proc/interrupts — "
                "check DVF_IRQ_NAME or verify driver registration");
    fprintf(stderr, "    [irq] %s: current count = %ld\n", g_irq_name, count);
    return 0;
}

int test_irq_count_increments(void) {
    if (g_trigger_reg < 0) {
        /* Trigger register disabled — skip gracefully */
        fprintf(stderr, "    [irq] DVF_IRQ_TRIGGER_REG=-1, skipping increment test\n");
        return 0;
    }

    long before = read_irq_count(g_irq_name);
    ASSERT_TRUE(before >= 0, "IRQ not in /proc/interrupts before trigger");

    int fd = dvf_open_device(&g_cfg, O_RDWR);
    ASSERT_TRUE(fd >= 0, "failed to open device for IRQ trigger");

    /* Write to the trigger register to generate an interrupt */
    int rc = dvf_write_reg(fd, g_trigger_reg, 0x1, &g_cfg);
    close(fd);
    ASSERT_EQ(rc, 0, "IRQ trigger register write failed");

    /* Give the kernel a moment to process the interrupt */
    usleep(50000); /* 50ms */

    long after = read_irq_count(g_irq_name);
    ASSERT_TRUE(after >= 0, "IRQ not in /proc/interrupts after trigger");

    fprintf(stderr, "    [irq] count before=%ld  after=%ld  delta=%ld\n",
            before, after, after - before);

    ASSERT_TRUE(after > before,
                "IRQ count did not increase after writing trigger register "
                "— check DVF_IRQ_TRIGGER_REG or driver interrupt handler");
    return 0;
}

int test_irq_no_storm(void) {
    /* Sanity check: IRQ rate must not exceed 10000/sec over a 200ms window.
     * A storm would indicate a runaway ISR or misconfigured trigger. */
    const long MAX_IRQS_PER_WINDOW = 2000;   /* 200ms * 10k/s */
    const int  WINDOW_US           = 200000; /* 200ms */

    long before = read_irq_count(g_irq_name);
    if (before < 0) {
        /* IRQ not registered — not our concern here */
        return 0;
    }

    usleep(WINDOW_US);

    long after = read_irq_count(g_irq_name);
    if (after < 0) return 0;

    long delta = after - before;
    fprintf(stderr, "    [irq] storm check: %ld IRQs in %dms (max=%ld)\n",
            delta, WINDOW_US / 1000, MAX_IRQS_PER_WINDOW);

    ASSERT_TRUE(delta < MAX_IRQS_PER_WINDOW,
                "IRQ storm detected — interrupt rate exceeded 10k/sec");
    return 0;
}

/* ---------------------------------------------------------------------------
 * Main
 * --------------------------------------------------------------------------- */

int main(void) {
    g_cfg = dvf_load_config();
    dvf_print_config(&g_cfg);

    /* IRQ name to search for in /proc/interrupts.
     * Default: last path component of the device path (e.g. "gpgpu"). */
    const char *irq_env = getenv("DVF_IRQ_NAME");
    if (irq_env && irq_env[0]) {
        strncpy(g_irq_name, irq_env, sizeof(g_irq_name) - 1);
    } else {
        /* Derive from device path: "/dev/gpgpu" -> "gpgpu" */
        const char *slash = strrchr(g_cfg.device_path, '/');
        strncpy(g_irq_name,
                slash ? slash + 1 : g_cfg.device_path,
                sizeof(g_irq_name) - 1);
    }
    g_irq_name[sizeof(g_irq_name) - 1] = '\0';

    /* Trigger register: -1 disables the increment test */
    const char *trig_env = getenv("DVF_IRQ_TRIGGER_REG");
    g_trigger_reg = (trig_env && trig_env[0]) ? atoi(trig_env) : 0;

    fprintf(stderr, "[DVF] IRQ name: '%s'  trigger_reg: %d\n",
            g_irq_name, g_trigger_reg);

    TEST_SUITE_BEGIN("interrupt_handling/irq_basic");

    RUN_TEST(test_device_exists);
    RUN_TEST(test_irq_registered);
    RUN_TEST(test_irq_count_increments);
    RUN_TEST(test_irq_no_storm);

    TEST_SUITE_END();
}
