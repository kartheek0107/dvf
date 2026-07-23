#include "qemu/osdep.h"
#include "hw/pci/pci.h"
#include "hw/pci/pci_device.h"
#include "qemu/module.h"
#include "qemu/log.h"
#include "qapi/error.h"
#include "hw/pci/msix.h"


#define TYPE_CDAC_GPGPU "gp_gpu"

#define GPU_VENDOR_ID (0x1222)
#define GPU_DEVICE_ID (0x2221)

#define KERNEL_SM_CTRL (0x201c)

#define DMA_SRC_LO         (0x100c)
#define DMA_SRC_HI         (0x1010)
#define DMA_DST_LO         (0x1014)
#define DMA_DST_HI         (0x1018)
#define DMA_SIZE           (0x1008)
#define DMA_CH0_CTRL       (0x1000)
#define DMA_DOORBELL       (0x1100)
#define DMA_STATUS         (0x1104)

#define REG_MMU_PT          (0x508)

#define REG_SM_START        (0x24)
#define REG_SM_ARG_LO       (0x08)
#define REG_SM_ARG_HI       (0x0C)

uint64_t dma_src_addr = 0;
uint64_t dma_src_addr_l0 = 0;
uint64_t dma_dest_addr = 0;
uint64_t dma_dest_addr_l0 = 0;
uint32_t dma_direction;
uint32_t dma_size;

uint32_t dma_completion_status = 0;

uint64_t ddr_base = 0x80000000;
uint8_t* device_mem;

OBJECT_DECLARE_SIMPLE_TYPE(GPGPU, CDAC_GPGPU)

typedef struct GPGPU {
    PCIDevice parent_obj;
    MemoryRegion bar0;
    uint64_t mmu_pt_base;
    uint32_t current_ctx;
    uint8_t *ddr_mem;
    uint64_t ddr_size;
    uint32_t regs[4096];
} GPGPU;

typedef struct GPUPTE {
    uint64_t entry;
} GPUPTE;


#define GPU_DDR_BASE 0x80000000ULL
#define GPU_PTE_VALID         (1ULL << 0)

static uint64_t gpgpu_read(void *opaque,
                          hwaddr addr,
                          unsigned size)
{
    GPGPU *d = opaque;
    uint64_t val = d->regs[addr >> 2];

    qemu_log_mask(LOG_GUEST_ERROR,
                  "GPGPU: BAR0 READ addr=0x%lx size=%u val=0x%lx\n",
                  (unsigned long)addr, size, (unsigned long)val);
    return val;
}

static void *gpu_local_ddr_address(uint64_t va) {
    int offset = va - ddr_base;
    return device_mem + offset;
}

static uint64_t gpu_translate_va(GPGPU *s,
                                 uint64_t va)
{
    uint64_t vpage;
    uint64_t offset;
    uint64_t pte_addr;
    GPUPTE pte;
    uint64_t phys;

    vpage = (va >> 12) & 0xFFFFFFFFF;
    offset = va & 0xFFF;

    s->mmu_pt_base = s->regs[REG_MMU_PT >> 2];
    s->mmu_pt_base = (uint64_t)gpu_local_ddr_address(s->mmu_pt_base);

    pte_addr =
        s->mmu_pt_base +
        (vpage * sizeof(GPUPTE));

    memcpy(&pte,
           (void*) pte_addr,
           sizeof(pte));

    if (!(pte.entry & GPU_PTE_VALID)) {
        return UINT64_MAX;
    }

    phys = (pte.entry & ~0xFFFULL);
    return phys + offset;
}

static void process_vector_addition(GPGPU *d, uint64_t kernel_args_addr) {
    qemu_log_mask(LOG_GUEST_ERROR, "kernel args addr=%lx\n", kernel_args_addr);
    uint64_t patrans = gpu_translate_va(d, kernel_args_addr);
    qemu_log_mask(LOG_GUEST_ERROR, "Args Va = %lx Pa = %lx\n", kernel_args_addr, patrans);
    uint64_t *data = (uint64_t*) gpu_local_ddr_address(patrans);
    uint64_t srcA = *(data);
    uint64_t srcB = *(data + 1);
    uint64_t dest = *(data + 2);
    uint32_t *psrcA = (uint32_t*) gpu_local_ddr_address(gpu_translate_va(d, srcA));
    uint32_t *psrcB = (uint32_t*) gpu_local_ddr_address(gpu_translate_va(d, srcB));
    uint32_t *pdest  = (uint32_t*) gpu_local_ddr_address(gpu_translate_va(d, dest));
    for (int i = 0; i < 1024; i++) {
        *(pdest + i) = *(psrcA + i) + *(psrcB + i);
    }
}

static void gpgpu_write(void *opaque,
                       hwaddr addr,
                       uint64_t val,
                       unsigned size)
{
    GPGPU *d = opaque;
    qemu_log_mask(LOG_GUEST_ERROR,
                  "GPGPU: BAR0 WRITE addr=0x%lx size=%u val=0x%lx index=%d\n",
                  (unsigned long)addr, size, (unsigned long)val, (int)(addr >> 2));
    d->regs[addr >> 2] = val;

    switch (addr) {
        case DMA_DOORBELL:
            dma_src_addr  = d->regs[DMA_SRC_LO >> 2] | ((uint64_t)(d->regs[DMA_SRC_HI >> 2]) << 32);
            dma_dest_addr = d->regs[DMA_DST_LO >> 2] | ((uint64_t)(d->regs[DMA_DST_HI >> 2]) << 32);
            dma_size      = d->regs[DMA_SIZE >> 2];
            dma_direction = d->regs[DMA_CH0_CTRL >> 2];

            qemu_log_mask(LOG_GUEST_ERROR, "DMA src addr = %lx\n", dma_src_addr);
            qemu_log_mask(LOG_GUEST_ERROR, "DMA dest addr = %lx\n", dma_dest_addr);
            d->regs[DMA_STATUS >> 2] = 0;
            if (dma_direction == 0) {
                qemu_log_mask(LOG_GUEST_ERROR, "Src is host DMA address\n");
                uint32_t ddr_index = dma_dest_addr - ddr_base;
                if (device_mem) {
                    pci_dma_read(&d->parent_obj, dma_src_addr, device_mem + ddr_index, dma_size);
                }
            } else {
                qemu_log_mask(LOG_GUEST_ERROR, "Dest is host DMA address\n");
                uint32_t ddr_index = dma_src_addr - ddr_base;
                if (device_mem) {
                    pci_dma_write(&d->parent_obj, dma_dest_addr, device_mem + ddr_index, dma_size);
                }
            }
            d->regs[DMA_STATUS >> 2] = 1;
            break;

        case KERNEL_SM_CTRL:
        case REG_SM_START: {
            qemu_log_mask(LOG_GUEST_ERROR, "--------------------------------start job processing\n");
            uint64_t kernel_args_addr =
                d->regs[REG_SM_ARG_LO >> 2] |
                ((uint64_t)(d->regs[REG_SM_ARG_HI >> 2]) << 32);

            process_vector_addition(d, kernel_args_addr);
            qemu_log_mask(LOG_GUEST_ERROR, "--------------------------------end   job processing\n");

            if (!msix_enabled(&d->parent_obj)) {
                qemu_log_mask(LOG_GUEST_ERROR, "GPGPU: MSI-X NOT ENABLED\n");
            } else {
                qemu_log_mask(LOG_GUEST_ERROR, "GPGPU: MSI-X enabled and Trigger MSIx\n");
                msix_notify(&d->parent_obj, 0);
            }
            break;
        }
        default:
            break;
    }
}

static const MemoryRegionOps gpgpu_ops = {
    .read = gpgpu_read,
    .write = gpgpu_write,
    .endianness = DEVICE_LITTLE_ENDIAN,
};

static void gpgpu_realize(PCIDevice *pdev, Error **errp)
{
    qemu_log_mask(LOG_GUEST_ERROR, "GPGPU %s %d\n", __func__, __LINE__);
    GPGPU *d = CDAC_GPGPU(pdev);
    pci_config_set_vendor_id(pdev->config, GPU_VENDOR_ID);
    pci_config_set_device_id(pdev->config, GPU_DEVICE_ID);
    pci_config_set_class(pdev->config, PCI_CLASS_PROCESSOR_CO);
    pci_config_set_revision(pdev->config, 0x00);
    pci_config_set_interrupt_pin(pdev->config, 1);

    memory_region_init_io(&d->bar0, OBJECT(d), &gpgpu_ops, d, "gpgpu-bar0", 0x4000);
    pci_register_bar(pdev, 0, PCI_BASE_ADDRESS_SPACE_MEMORY, &d->bar0);

    msix_init_exclusive_bar(pdev, 4, 4, &error_fatal);
    msix_vector_use(pdev, 0);

    /* Allocate 128 MB of simulated device-local DDR */
    device_mem = malloc(128 * 1024 * 1024);
}

static void gpgpu_class_init(ObjectClass *klass, void *data)
{
    PCIDeviceClass *k = PCI_DEVICE_CLASS(klass);
    k->realize   = gpgpu_realize;
    k->vendor_id = GPU_VENDOR_ID;
    k->device_id = GPU_DEVICE_ID;
    k->revision  = 0;
    k->class_id  = PCI_CLASS_OTHERS;

    qemu_log_mask(LOG_GUEST_ERROR, "GPGPU %s %d\n", __func__, __LINE__);
}

static const TypeInfo gpgpu_info = {
    .name          = TYPE_CDAC_GPGPU,
    .parent        = TYPE_PCI_DEVICE,
    .instance_size = sizeof(GPGPU),
    .class_init    = gpgpu_class_init,
    .interfaces    = (InterfaceInfo[]) {
        { INTERFACE_CONVENTIONAL_PCI_DEVICE },
        { }
    },
};

static void gpgpu_register_types(void)
{
    type_register_static(&gpgpu_info);
}

type_init(gpgpu_register_types);
