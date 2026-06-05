/*
 * device_helpers.h — Helper functions for interacting with the GPGPU device.
 *
 * The gpgpu_driver exposes /dev/gpgpu as a character device.
 * Register access is done via read()/write() with lseek() for offset.
 * Each register is 32 bits (4 bytes). The device has 256 registers
 * at offsets 0..1020 (0x000..0x3FC).
 */

#ifndef DVF_DEVICE_HELPERS_H
#define DVF_DEVICE_HELPERS_H

#include <stdio.h>
#include <stdint.h>
#include <unistd.h>
#include <fcntl.h>
#include <errno.h>
#include <string.h>
#include <sys/stat.h>

#define GPGPU_DEVICE_PATH  "/dev/gpgpu"
#define GPGPU_REG_COUNT    256
#define GPGPU_REG_SIZE     4        /* bytes per register */
#define GPGPU_BAR_SIZE     1024     /* total BAR0 addressable bytes (256 * 4) */

/*
 * gpgpu_open_device — Open the device file.
 * Returns fd on success, -1 on failure (with perror).
 * mode: O_RDONLY, O_WRONLY, or O_RDWR
 */
static inline int gpgpu_open_device(int mode) {
    int fd = open(GPGPU_DEVICE_PATH, mode);
    if (fd < 0) {
        fprintf(stderr, "gpgpu: failed to open %s: %s\n",
                GPGPU_DEVICE_PATH, strerror(errno));
    }
    return fd;
}

/*
 * gpgpu_device_exists — Check if /dev/gpgpu exists.
 * Returns 1 if it exists, 0 otherwise.
 */
static inline int gpgpu_device_exists(void) {
    struct stat st;
    return (stat(GPGPU_DEVICE_PATH, &st) == 0);
}

/*
 * gpgpu_write_reg — Write a 32-bit value to register at index reg_idx.
 * reg_idx is the register number (0..255), NOT the byte offset.
 * Returns 0 on success, -1 on failure.
 */
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

/*
 * gpgpu_read_reg — Read a 32-bit value from register at index reg_idx.
 * Returns the value on success, or sets *err = -1 on failure.
 */
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

/*
 * gpgpu_write_raw — Write raw bytes at a byte offset (for unaligned tests).
 * Returns bytes written, or -1 on error.
 */
static inline ssize_t gpgpu_write_raw(int fd, off_t byte_offset,
                                       const void *buf, size_t len) {
    if (lseek(fd, byte_offset, SEEK_SET) == (off_t)-1)
        return -1;
    return write(fd, buf, len);
}

/*
 * gpgpu_read_raw — Read raw bytes at a byte offset (for unaligned tests).
 * Returns bytes read, or -1 on error.
 */
static inline ssize_t gpgpu_read_raw(int fd, off_t byte_offset,
                                      void *buf, size_t len) {
    if (lseek(fd, byte_offset, SEEK_SET) == (off_t)-1)
        return -1;
    return read(fd, buf, len);
}

#endif /* DVF_DEVICE_HELPERS_H */
