/*
 * device_config.h — Runtime device configuration for DVF test binaries.
 *
 * Allows the same test binary to target different devices (QEMU GPGPU,
 * physical FPGA, etc.) by reading configuration from environment variables.
 *
 * Environment variables (all optional — defaults match the GPGPU device):
 *   DVF_DEVICE_PATH  — Device node path        (default: "/dev/gpgpu")
 *   DVF_REG_COUNT    — Number of registers      (default: 256)
 *   DVF_REG_SIZE     — Bytes per register       (default: 4)
 *   DVF_BAR_SIZE     — Total BAR0 bytes         (default: 1024)
 */

#ifndef DVF_DEVICE_CONFIG_H
#define DVF_DEVICE_CONFIG_H

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* Defaults matching the GPGPU QEMU device */
#define DVF_DEFAULT_DEVICE_PATH  "/dev/gp_gpu"
#define DVF_DEFAULT_REG_COUNT    256
#define DVF_DEFAULT_REG_SIZE     4
#define DVF_DEFAULT_BAR_SIZE     1024

typedef struct {
    char device_path[256];  /* e.g., "/dev/gp_gpu" or "/dev/fpga0" */
    int  reg_count;         /* Number of registers (e.g., 256) */
    int  reg_size;          /* Bytes per register (e.g., 4) */
    int  bar_size;          /* Total BAR0 addressable bytes (e.g., 1024) */
} DeviceConfig;

/*
 * dvf_load_config — Load device configuration from environment variables.
 *
 * Falls back to GPGPU defaults if environment variables are not set.
 * This allows the same test binary to run against any register-based device.
 */
static inline DeviceConfig dvf_load_config(void) {
    DeviceConfig cfg;
    const char *env;

    /* Device path */
    env = getenv("DVF_DEVICE_PATH");
    if (env && env[0]) {
        strncpy(cfg.device_path, env, sizeof(cfg.device_path) - 1);
        cfg.device_path[sizeof(cfg.device_path) - 1] = '\0';
    } else {
        strncpy(cfg.device_path, DVF_DEFAULT_DEVICE_PATH, sizeof(cfg.device_path) - 1);
        cfg.device_path[sizeof(cfg.device_path) - 1] = '\0';
    }

    /* Register count */
    env = getenv("DVF_REG_COUNT");
    cfg.reg_count = (env && env[0]) ? atoi(env) : DVF_DEFAULT_REG_COUNT;
    if (cfg.reg_count <= 0) cfg.reg_count = DVF_DEFAULT_REG_COUNT;

    /* Register size */
    env = getenv("DVF_REG_SIZE");
    cfg.reg_size = (env && env[0]) ? atoi(env) : DVF_DEFAULT_REG_SIZE;
    if (cfg.reg_size <= 0) cfg.reg_size = DVF_DEFAULT_REG_SIZE;

    /* BAR size */
    env = getenv("DVF_BAR_SIZE");
    cfg.bar_size = (env && env[0]) ? atoi(env) : DVF_DEFAULT_BAR_SIZE;
    if (cfg.bar_size <= 0) cfg.bar_size = DVF_DEFAULT_BAR_SIZE;

    return cfg;
}

/*
 * dvf_print_config — Print the active device configuration (for debugging).
 */
static inline void dvf_print_config(const DeviceConfig *cfg) {
    fprintf(stderr, "[DVF] Device: %s  regs=%d  reg_size=%d  bar=%d bytes\n",
            cfg->device_path, cfg->reg_count, cfg->reg_size, cfg->bar_size);
}

#endif /* DVF_DEVICE_CONFIG_H */
