#include <linux/io.h>
#include <linux/kernel.h>
#include "gpgpu_dma.h"
#include "gpgpu_regs.h"
#include <linux/delay.h>

/* ===== EDMA ===== */
#define EDMA_DOORBELL_START    0x1

int gp_gpu_dma(struct gp_gpu_dev *dev,
               uint64_t srcaddr,
               uint64_t destaddr,
               uint32_t size,
               uint32_t direction)
{
    void __iomem *bar0 = dev->bar0;
    pr_info("[DMA] srcaddr=%llx destaddr=%llx size=%u dir=%d\n",
            srcaddr, destaddr, size, direction);
    if (!dev || !bar0) {
        pr_err("[DMA] invalid BAR0\n");
        return -EINVAL;
    }

    pr_info("[DMA] BAR0=%px\n", bar0);
    /* Program source */
    writel(lower_32_bits(srcaddr), bar0 + EDMA_CH0_SRC_LO);
    writel(upper_32_bits(srcaddr), bar0 + EDMA_CH0_SRC_HI);

    /* Program destination */
    writel(lower_32_bits(destaddr), bar0 + EDMA_CH0_DST_LO);
    writel(upper_32_bits(destaddr), bar0 + EDMA_CH0_DST_HI);

    /* Size */
    writel(size, bar0 + EDMA_CH0_SIZE);

    /* Start DMA */
    writel(direction, bar0 + EDMA_CH0_CTRL);
    writel(EDMA_DOORBELL_START, bar0 + EDMA_DOORBELL);

    /* Poll completion  */
    int timeout = 1000000;
    while (!(readl(bar0 + EDMA_STATUS) & 0x1))  {
        cpu_relax();
        if (--timeout == 0) {
            pr_err("[DMA] timeout waiting for completion\n");
            return -ETIMEDOUT;
        }

    }

    pr_info("[DMA] done\n");
    return 0;
}

