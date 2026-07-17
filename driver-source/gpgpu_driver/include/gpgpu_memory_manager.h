#ifndef GPGPU_MEMORY_MANAGER_H
#define GPGPU_MEMORY_MANAGER_H

#include <linux/module.h>
#include <linux/fs.h>
#include <linux/cdev.h>
#include <linux/uaccess.h>
#include <linux/slab.h>
#include <linux/mm.h>
#include <linux/io.h>

#include "uapi/linux/gpgpu_ioctl.h"

// ---- CONFIG ----
#define PAGE_SIZE_4K        4096
#define MAX_CONTEXTS        16

#define ADDRESS_SPACE_32BIT 28

#define GPU_PTE_VALID       (1ULL << 0)
// ---- PAGE TABLE ----
//
struct gpu_hw_pte {
    uint64_t entry;
};
struct gpu_pte {
    uint64_t phys;
    uint8_t  valid;
};

// Simple flat page table (for now)
struct gpu_page_table {
    uint64_t num_entries;
    struct gpu_hw_pte *cpu_ptr;
    dma_addr_t dma_handle; // for PCIe DMA
    uint64_t gpu_phys;
};

// ---- CONTEXT ----

struct gpu_context {
    uint32_t id;
    struct gpu_page_table pt;

    uint64_t vpage_next;

    spinlock_t lock;
};

struct gpu_device {
    struct cdev cdev;
    struct gp_gpu_dev *dev;

    struct gpu_context ctx[MAX_CONTEXTS];
    int ctx_count;

    uint64_t ddr_base;
    uint64_t ddr_size;
    uint64_t ddr_next;

    void __iomem *bar_virt;

    spinlock_t lock;
    uint8_t init_status;
};

int gpu_initialize_memory_manager(uint64_t ddr_base, uint64_t ddr_size, struct gp_gpu_dev *dev);
int gpu_ctx_create(struct pci_dev *pdev, struct gpu_ctx_create_req *req);
int gpu_alloc(struct gpu_alloc_req *req);
int gpu_free(struct gpu_free_req *req);
int gpu_bind_context(uint32_t ctx_id);

#endif // GPGPU_MEMORY_MANAGER_H
