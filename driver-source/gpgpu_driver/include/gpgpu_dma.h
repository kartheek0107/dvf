#ifndef GPGPU_DMA_H
#define GPGPU_DMA_H
#include <linux/pci.h>
#include <linux/types.h>
#include "gpgpu_drv.h"

#define DMA_H2D 0
#define DMA_D2H 1



int gp_gpu_dma(struct gp_gpu_dev *dev,
               uint64_t srcaddr,
               uint64_t destaddr,
               uint32_t size,
               uint32_t direction);



#endif
