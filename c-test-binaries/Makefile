# DVF Test Binaries — Top-level Makefile
#
# Builds all C and C++ test binaries for the driver validation suite.
# Binaries are statically linked so they can run inside minimal guest VMs.
#
# Usage:
#   make              — build all test binaries
#   make clean        — remove all built binaries
#   make install SHARE_DIR=/path/to/share  — copy binaries to 9p share

CC      ?= gcc
CXX     ?= g++
CFLAGS  := -Wall -Wextra -O2 -std=gnu11 -I common
CXXFLAGS := -Wall -Wextra -O2 -std=c++17 -I common
# -Wl,-rpath,'$$ORIGIN/lib': bake a relative RPATH into every binary so the
# dynamic linker automatically searches a lib/ directory placed next to the
# binary at runtime — no LD_LIBRARY_PATH changes needed.
LDFLAGS := -lpthread -Wl,-rpath,'$$ORIGIN/lib'

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
	error_injection/test_boundaries \
	interrupt_handling/test_irq_basic \
	abctest/test

# Default share directory (override with: make install SHARE_DIR=...)
SHARE_DIR ?= $(HOME)/qemu-rootfs/share

.PHONY: all clean install list

all: $(TESTS)
	@echo ""
	@echo "=== Built $(words $(TESTS)) test binaries ==="
	@for t in $(TESTS); do echo "  $$t"; done

# Pattern rule: build each C test binary from its .c + common framework
%: %.c $(COMMON_SRC) common/test_framework.h common/device_helpers.h
	@echo "  CC  $@"
	@$(CC) $(CFLAGS) -o $@ $< $(COMMON_SRC) $(LDFLAGS)

# Pattern rule: build each C++ test binary from its .cpp + common framework
# test_framework.c is compiled as C (gcc handles it) and linked in by g++.
%: %.cpp $(COMMON_SRC) common/test_framework.h common/device_helpers.h
	@echo "  CXX $@"
	@$(CXX) $(CXXFLAGS) -o $@ $< $(COMMON_SRC) $(LDFLAGS)

clean:
	@echo "Cleaning test binaries..."
	@rm -f $(TESTS)
	@echo "Done."

# Copy all binaries (and their bundled lib/ dirs) to the 9p shared folder
install: all
	@echo "Installing test binaries to $(SHARE_DIR)/dvf_tests/"
	@mkdir -p $(SHARE_DIR)/dvf_tests
	@for t in $(TESTS); do \
		dir=$$(dirname $$t); \
		mkdir -p $(SHARE_DIR)/dvf_tests/$$dir; \
		cp $$t $(SHARE_DIR)/dvf_tests/$$t; \
		if [ -d $$dir/lib ]; then \
			cp -r $$dir/lib $(SHARE_DIR)/dvf_tests/$$dir/; \
		fi; \
	done
	@echo "Installed $(words $(TESTS)) binaries."

list:
	@echo "Test binaries:"
	@for t in $(TESTS); do echo "  $$t"; done
