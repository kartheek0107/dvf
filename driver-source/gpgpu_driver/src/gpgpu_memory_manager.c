#include <linux/pci.h>

#include "gpgpu_memory_manager.h"
#include "gpgpu_regs.h"
#include "gpgpu_dma.h"

static struct gpu_device gdev;

#define GPU_PAGE_SHIFT     12
#define GPU_PAGE_SIZE      (1ULL << GPU_PAGE_SHIFT)
#define GPU_CTX_SHIFT      48
#define GPU_CTX_MASK       0xFFFFULL
#define GPU_VPAGE_MASK     0xFFFFFFFFFULL

static inline uint32_t gpu_va_to_ctx(uint64_t va)
{
    return (va >> GPU_CTX_SHIFT) &
           GPU_CTX_MASK;
}

static inline uint64_t gpu_va_to_vpage(uint64_t va)
{
    return (va >> GPU_PAGE_SHIFT) &
           GPU_VPAGE_MASK;
}

static inline void gpu_writel(uint32_t val, uint32_t reg)
{
    writel(val, gdev.bar_virt + reg);
}

static inline void gpu_program_context(struct gpu_context *ctx)
{
    gpu_writel(lower_32_bits(ctx->pt.gpu_phys),
               GPU_REG_PT_BASE_LO);
    gpu_writel(upper_32_bits(ctx->pt.gpu_phys),
               GPU_REG_PT_BASE_HI);
    gpu_writel(ctx->id,
               GPU_REG_CTX_ID);
    gpu_writel(1,
               GPU_REG_MMU_ENABLE);

    pr_info("GPU MMU programmed\n");
    pr_info("PT base : 0x%llx\n",
            ctx->pt.gpu_phys);
}

static uint64_t gpu_ddr_alloc(uint64_t size)
{
    uint64_t addr;

    size = PAGE_ALIGN(size);

    if ((gdev.ddr_next + size) >
        (gdev.ddr_base + gdev.ddr_size)) {
        return 0;
    }

    addr = gdev.ddr_next;
    gdev.ddr_next += size;

    return addr;
}

int gpu_initialize_memory_manager(uint64_t ddr_base, uint64_t ddr_size, struct gp_gpu_dev *dev)
{
    if (gdev.init_status == 0) {
        gdev.ddr_base = ddr_base;
        gdev.ddr_next = gdev.ddr_base;
        gdev.ddr_size = ddr_size;
        gdev.bar_virt = dev->bar0;
        gdev.dev = dev;
        pr_info("Initialized memory manager with Physical Address of GPU DDR 0x%llx of size %lld\n",
                gdev.ddr_base, gdev.ddr_size);
    } else {
        pr_info("Unable to initialize memory manager as it is already initialized\n");
        return -1;
    }
    return 0;
}

int gpu_ctx_create(struct pci_dev *pdev, struct gpu_ctx_create_req *req)
{
    int i;

    spin_lock(&gdev.lock);

    for (i = 0; i < MAX_CONTEXTS; i++) {
        if (gdev.ctx[i].id == 0) {

            struct gpu_context *ctx = &gdev.ctx[i];

            ctx->id = i + 1;
            ctx->vpage_next = 0;

            ctx->pt.num_entries = gdev.ddr_size / PAGE_SIZE_4K;
            ctx->pt.cpu_ptr = dma_alloc_coherent(&pdev->dev,
                                        sizeof(struct gpu_hw_pte) *
                                               ctx->pt.num_entries,
                                        &ctx->pt.dma_handle,
                                        GFP_KERNEL);

            if (!ctx->pt.cpu_ptr) {
                spin_unlock(&gdev.lock);
                return -ENOMEM;
            }
            /*
             * Allocate GPU physical memory
             * for page table
             */
            ctx->pt.gpu_phys = gpu_ddr_alloc(sizeof(struct gpu_hw_pte) *
                                             ctx->pt.num_entries);

            if (!ctx->pt.gpu_phys) {
                dma_free_coherent(&pdev->dev,
                                  sizeof(struct gpu_hw_pte) *
                                  ctx->pt.num_entries,
                                  ctx->pt.cpu_ptr,
                                  ctx->pt.dma_handle);
                spin_unlock(&gdev.lock);
                return -ENOMEM;
            }

            spin_lock_init(&ctx->lock);
            req->ctx_id = ctx->id;
            pr_info("Created Virtual Memory \n");
            pr_info("Virtual memory id    : %d\n",ctx->id);
            pr_info("Number of Page Table entries   : %lld\n",ctx->pt.num_entries);
            spin_unlock(&gdev.lock);
            return 0;
        }
    }

    spin_unlock(&gdev.lock);
    return -ENOMEM;
}

int gpu_alloc(struct gpu_alloc_req *req)
{
    struct gpu_context *ctx = &gdev.ctx[req->ctx_id - 1];

    uint64_t size = PAGE_ALIGN(req->size);
    uint64_t pages = size / PAGE_SIZE_4K;
    uint64_t base_vpage;
    uint64_t va;

    spin_lock(&ctx->lock);

    uint64_t pa = gpu_ddr_alloc(size);

    base_vpage = ctx->vpage_next;
    va = ((uint64_t)ctx->id << 48) |
            (base_vpage << 12);

    if (!pa) {
        spin_unlock(&ctx->lock);
        return -ENOMEM;
    }
    // Map pages
    for (uint64_t i = 0; i < pages; i++) {
        struct gpu_hw_pte *pte;
        uint64_t vpage;
        vpage = base_vpage + i;

        pte = &ctx->pt.cpu_ptr[vpage];
        pte->entry = ((pa + i * PAGE_SIZE_4K) & ~0xFFFULL) | GPU_PTE_VALID;
    }
    ctx->vpage_next += pages;
    /* DMA sync of page table to GPU */
    gp_gpu_dma(gdev.dev,
               ctx->pt.dma_handle,
               ctx->pt.gpu_phys,
               (sizeof(struct gpu_hw_pte) *
                     ctx->pt.num_entries),
               DMA_H2D);

    req->va = va;
    req->pa = pa;

    spin_unlock(&ctx->lock);
    return 0;
}

int gpu_free(struct gpu_free_req *req)
{
    struct gpu_context *ctx;
    uint32_t ctx_id;
    uint64_t start_vpage;
    uint64_t pages;
    uint64_t i;

    if (!req)
        return -EINVAL;

    /*
     * Extract context ID from VA
     */
    ctx_id = gpu_va_to_ctx(req->va);
    if (!ctx_id || ctx_id > MAX_CONTEXTS)
        return -EINVAL;

    ctx = &gdev.ctx[ctx_id - 1];
    /*
     * Convert VA -> virtual page
     */
    start_vpage =
        gpu_va_to_vpage(req->va);

    /*
     * Convert size -> page count
     */
    pages =
        PAGE_ALIGN(req->size) /
        GPU_PAGE_SIZE;

    spin_lock(&ctx->lock);
    for (i = 0; i < pages; i++) {
        uint64_t vpage;
        struct gpu_hw_pte *pte;
        vpage = start_vpage + i;
        if (vpage >= ctx->pt.num_entries) {
            spin_unlock(&ctx->lock);
            return -EINVAL;
        }

        pte = &ctx->pt.cpu_ptr[vpage];
        /*
         * Already free?
         */
        if (!(pte->entry & GPU_PTE_VALID)) {
            spin_unlock(&ctx->lock);
            return -EINVAL;
        }

        /*
         * Invalidate PTE
         */
        pte->entry = 0;
    }

    /*
     * flush/sync page table
     * invalidate GPU TLB
     */

    spin_unlock(&ctx->lock);
    return 0;
}

int gpu_bind_context(uint32_t ctx_id)
{
    struct gpu_context *ctx;

    if (!ctx_id || ctx_id > MAX_CONTEXTS)
        return -EINVAL;

    ctx = &gdev.ctx[ctx_id - 1];
    gpu_program_context(ctx);

    return 0;
} 
