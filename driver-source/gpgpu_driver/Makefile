MODULE_NAME=gpgpu_pcie_ep_driver

$(MODULE_NAME)-y := src/gpgpu_pcie_ep_driver.o \
                    src/gpgpu_memory_manager.o \
                    src/gpgpu_dma.o

obj-m := $(MODULE_NAME).o

ccflags-y := -I. -Iinclude

KDIR := /home/kartheekbudime/VirtualMachines/linux

all: 
	make -C $(KDIR) M=$(CURDIR) modules 
clean: 
	make -C $(KDIR) M=$(CURDIR) clean 
