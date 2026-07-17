#ifndef __GPGPU_DRV_H__
#define __GPGPU_DRV_H__

#include <linux/pci.h>

struct gp_gpu_dev {
    struct pci_dev *pdev;
    void __iomem *bar0;
};

struct gp_gpu_buffer {
    int handle;
    size_t size;
    void *cpu_addr;
    dma_addr_t dma_handle;

    uint64_t gpu_va;
    uint64_t gpu_pa;
    uint32_t ctx_id;

    struct list_head list;
};

struct gp_gpu_job_entry {
    uint32_t job_id;
    int done;
    wait_queue_head_t wq;
    struct list_head list;
};


#endif

