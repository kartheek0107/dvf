# DVF Test Binaries — Top-level Makefile
#
# Builds all C test binaries for the driver validation suite.
# Binaries are statically linked so they can run inside minimal guest VMs.
#
# Usage:
#   make              — build all test binaries
#   make clean        — remove all built binaries
#   make install SHARE_DIR=/path/to/share  — copy binaries to 9p share

CC      ?= gcc
CFLAGS  := -Wall -Wextra -O2 -std=gnu11 -I common
LDFLAGS := -lpthread

# Common source (linked into every test binary)
COMMON_SRC := common/test_framework.c

# All test binaries (source -> binary, same path minus .c)
TESTS := \
	read_write/test_register_rw \
	read_write/test_offset_sweep \
	data_integrity/test_patterns \
	data_integrity/test_persistence \
	concurrency/test_concurrent_rw \
	stress_performance/test_throughput \
	error_injection/test_boundaries

# Default share directory (override with: make install SHARE_DIR=...)
SHARE_DIR ?= /home/kartheekbudime/qemu-rootfs/share

.PHONY: all clean install list

all: $(TESTS)
	@echo ""
	@echo "=== Built $(words $(TESTS)) test binaries ==="
	@for t in $(TESTS); do echo "  $$t"; done

# Pattern rule: build each test binary from its .c + common framework
%: %.c $(COMMON_SRC) common/test_framework.h common/device_helpers.h
	@echo "  CC  $@"
	@$(CC) $(CFLAGS) -o $@ $< $(COMMON_SRC) $(LDFLAGS)

clean:
	@echo "Cleaning test binaries..."
	@rm -f $(TESTS)
	@echo "Done."

# Copy all binaries to the 9p shared folder for the guest VM
install: all
	@echo "Installing test binaries to $(SHARE_DIR)/dvf_tests/"
	@mkdir -p $(SHARE_DIR)/dvf_tests
	@for t in $(TESTS); do \
		dir=$$(dirname $$t); \
		mkdir -p $(SHARE_DIR)/dvf_tests/$$dir; \
		cp $$t $(SHARE_DIR)/dvf_tests/$$t; \
	done
	@echo "Installed $(words $(TESTS)) binaries."

list:
	@echo "Test binaries:"
	@for t in $(TESTS); do echo "  $$t"; done
