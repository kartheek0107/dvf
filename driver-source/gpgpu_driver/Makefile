# Build the gpgpu_driver kernel module.
#
# KDIR should point to your built Linux kernel tree.
# Usage:
#   make                           — build the module
#   make KDIR=/path/to/linux       — build against a specific kernel
#   make clean                     — clean build artifacts

obj-m += gpgpu_driver.o

KDIR ?= /home/kartheekbudime/VirtualMachines/linux

all:
	$(MAKE) -C $(KDIR) M=$(PWD) modules

clean:
	$(MAKE) -C $(KDIR) M=$(PWD) clean
