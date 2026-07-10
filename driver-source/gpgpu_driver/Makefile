# Build the gpgpu_driver kernel module.
#
# The kernel module source is gpgpu_driver.c, but the registry and share
# directory expect the output to be named gpgpu_pcie_ep_driver.ko.
# The 'install' target copies/renames it to SHARE_DIR.
#
# Usage:
#   make                           — build the module
#   make KDIR=/path/to/linux       — build against a specific kernel
#   make install                   — build + copy to SHARE_DIR
#   make clean                     — clean build artifacts

obj-m += gpgpu_driver.o

KDIR     ?= /home/kartheekbudime/VirtualMachines/linux
SHARE_DIR ?= /home/kartheekbudime/qemu-rootfs/share

# Output name expected by device_registry.json and the guest agent
TARGET_KO = gpgpu_pcie_ep_driver.ko

all:
	$(MAKE) -C $(KDIR) M=$(shell pwd) modules

# Copy and rename the .ko to the 9p share directory
install: all
	cp $(shell pwd)/gpgpu_driver.ko $(SHARE_DIR)/$(TARGET_KO)
	@echo "Installed: $(SHARE_DIR)/$(TARGET_KO)"

clean:
	$(MAKE) -C $(KDIR) M=$(shell pwd) clean
