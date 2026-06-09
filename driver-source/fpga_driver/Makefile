# FPGA Driver — Kernel Module Build
#
# Usage:
#   make                          # build fpga_driver.ko
#   make clean                    # clean build artifacts
#   make KDIR=/path/to/kernel     # build against a specific kernel source tree
#
# For cross-compilation (e.g., building for a QEMU guest kernel):
#   make KDIR=/home/kartheekbudime/VirtualMachines/linux

obj-m := fpga_driver.o

KDIR ?= /lib/modules/$(shell uname -r)/build
PWD  := $(shell pwd)

all:
	$(MAKE) -C $(KDIR) M=$(PWD) modules

clean:
	$(MAKE) -C $(KDIR) M=$(PWD) clean
