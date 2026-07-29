/*
 * device_helpers.h — Helper functions for interacting with DVF devices.
 *
 * Provides two API layers:
 *   1. Generic dvf_* API — Works with any register-based device via DeviceConfig.
 *      Use dvf_load_config() from device_config.h to configure at runtime.
 *   2. Legacy gpgpu_* API — Thin wrappers using hardcoded GPGPU defaults.
 *      Kept for backward compatibility; new tests should use dvf_* instead.
 *
 * Register access is done via read()/write() with lseek() for offset.
 * Each register is 32 bits (4 bytes) by default.
 */

#ifndef DVF_DEVICE_HELPERS_H
#define DVF_DEVICE_HELPERS_H

#ifdef __cplusplus
extern "C" {
#endif

#include <stdio.h>
#include <stdint.h>
#include <unistd.h>
#include <fcntl.h>
#include <errno.h>
#include <string.h>
#include <sys/stat.h>

#include "device_config.h"

/* Legacy GPGPU constants — kept for backward compatibility */
#define GPGPU_DEVICE_PATH  "/dev/gp_gpu"
#define GPGPU_REG_COUNT    256
#define GPGPU_REG_SIZE     4        /* bytes per register */
#define GPGPU_BAR_SIZE     1024     /* total BAR0 addressable bytes (256 * 4) */

/* ======================================================================
 * Generic DVF Device API (config-driven, supports QEMU + FPGA)
 * ====================================================================== */

/*
 * dvf_open_device — Open the device file specified in config.
 * Returns fd on success, -1 on failure (with perror).
 * mode: O_RDONLY, O_WRONLY, or O_RDWR
 */
static inline int dvf_open_device(const DeviceConfig *cfg, int mode) {
    int fd = open(cfg->device_path, mode);
    if (fd < 0) {
        fprintf(stderr, "dvf: failed to open %s: %s\n",
                cfg->device_path, strerror(errno));
    }
    return fd;
}

/*
 * dvf_device_exists — Check if the device node exists.
 * Returns 1 if it exists, 0 otherwise.
 */
static inline int dvf_device_exists(const DeviceConfig *cfg) {
    struct stat st;
    return (stat(cfg->device_path, &st) == 0);
}

/*
 * dvf_write_reg — Write a 32-bit value to register at index reg_idx.
 * reg_idx is the register number (0..reg_count-1), NOT the byte offset.
 * Returns 0 on success, -1 on failure.
 */
static inline int dvf_write_reg(int fd, int reg_idx, uint32_t value, const DeviceConfig *cfg) {
    off_t offset = (off_t)(reg_idx * cfg->reg_size);

    if (lseek(fd, offset, SEEK_SET) == (off_t)-1) {
        fprintf(stderr, "dvf: lseek to offset %ld failed: %s\n",
                (long)offset, strerror(errno));
        return -1;
    }

    ssize_t n = write(fd, &value, sizeof(value));
    if (n != sizeof(value)) {
        fprintf(stderr, "dvf: write at reg %d failed: %s (wrote %zd)\n",
                reg_idx, strerror(errno), n);
        return -1;
    }

    return 0;
}

/*
 * dvf_read_reg — Read a 32-bit value from register at index reg_idx.
 * Returns the value on success, or sets *err = -1 on failure.
 */
static inline uint32_t dvf_read_reg(int fd, int reg_idx, int *err, const DeviceConfig *cfg) {
    off_t offset = (off_t)(reg_idx * cfg->reg_size);
    uint32_t value = 0;

    if (lseek(fd, offset, SEEK_SET) == (off_t)-1) {
        fprintf(stderr, "dvf: lseek to offset %ld failed: %s\n",
                (long)offset, strerror(errno));
        if (err) *err = -1;
        return 0;
    }

    ssize_t n = read(fd, &value, sizeof(value));
    if (n != sizeof(value)) {
        fprintf(stderr, "dvf: read at reg %d failed: %s (read %zd)\n",
                reg_idx, strerror(errno), n);
        if (err) *err = -1;
        return 0;
    }

    if (err) *err = 0;
    return value;
}

/*
 * dvf_write_raw — Write raw bytes at a byte offset (for unaligned tests).
 * Returns bytes written, or -1 on error.
 */
static inline ssize_t dvf_write_raw(int fd, off_t byte_offset,
                                     const void *buf, size_t len) {
    if (lseek(fd, byte_offset, SEEK_SET) == (off_t)-1)
        return -1;
    return write(fd, buf, len);
}

/*
 * dvf_read_raw — Read raw bytes at a byte offset (for unaligned tests).
 * Returns bytes read, or -1 on error.
 */
static inline ssize_t dvf_read_raw(int fd, off_t byte_offset,
                                    void *buf, size_t len) {
    if (lseek(fd, byte_offset, SEEK_SET) == (off_t)-1)
        return -1;
    return read(fd, buf, len);
}

/* ======================================================================
 * Legacy GPGPU API (backward compatibility wrappers)
 *
 * These use hardcoded GPGPU defaults. New tests should prefer dvf_* API.
 * ====================================================================== */

static inline int gpgpu_open_device(int mode) {
    int fd = open(GPGPU_DEVICE_PATH, mode);
    if (fd < 0) {
        fprintf(stderr, "gpgpu: failed to open %s: %s\n",
                GPGPU_DEVICE_PATH, strerror(errno));
    }
    return fd;
}

static inline int gpgpu_device_exists(void) {
    struct stat st;
    return (stat(GPGPU_DEVICE_PATH, &st) == 0);
}

static inline int gpgpu_write_reg(int fd, int reg_idx, uint32_t value) {
    off_t offset = (off_t)(reg_idx * GPGPU_REG_SIZE);

    if (lseek(fd, offset, SEEK_SET) == (off_t)-1) {
        fprintf(stderr, "gpgpu: lseek to offset %ld failed: %s\n",
                (long)offset, strerror(errno));
        return -1;
    }

    ssize_t n = write(fd, &value, sizeof(value));
    if (n != sizeof(value)) {
        fprintf(stderr, "gpgpu: write at reg %d failed: %s (wrote %zd)\n",
                reg_idx, strerror(errno), n);
        return -1;
    }

    return 0;
}

static inline uint32_t gpgpu_read_reg(int fd, int reg_idx, int *err) {
    off_t offset = (off_t)(reg_idx * GPGPU_REG_SIZE);
    uint32_t value = 0;

    if (lseek(fd, offset, SEEK_SET) == (off_t)-1) {
        fprintf(stderr, "gpgpu: lseek to offset %ld failed: %s\n",
                (long)offset, strerror(errno));
        if (err) *err = -1;
        return 0;
    }

    ssize_t n = read(fd, &value, sizeof(value));
    if (n != sizeof(value)) {
        fprintf(stderr, "gpgpu: read at reg %d failed: %s (read %zd)\n",
                reg_idx, strerror(errno), n);
        if (err) *err = -1;
        return 0;
    }

    if (err) *err = 0;
    return value;
}

static inline ssize_t gpgpu_write_raw(int fd, off_t byte_offset,
                                       const void *buf, size_t len) {
    if (lseek(fd, byte_offset, SEEK_SET) == (off_t)-1)
        return -1;
    return write(fd, buf, len);
}

static inline ssize_t gpgpu_read_raw(int fd, off_t byte_offset,
                                      void *buf, size_t len) {
    if (lseek(fd, byte_offset, SEEK_SET) == (off_t)-1)
        return -1;
    return read(fd, buf, len);
}

#ifdef __cplusplus
} /* extern "C" */
#endif

#endif /* DVF_DEVICE_HELPERS_H */
