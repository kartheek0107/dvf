#ifndef GP_GPU_REGS_H
#define GP_GPU_REGS_H

/* ===== DMA (eDMA) Registers ===== */
#define EDMA_BASE          0x1000

#define EDMA_CH0_CTRL      (EDMA_BASE + 0x000)
#define EDMA_CH0_SIZE      (EDMA_BASE + 0x008)
#define EDMA_CH0_SRC_LO    (EDMA_BASE + 0x00C)
#define EDMA_CH0_SRC_HI    (EDMA_BASE + 0x010)
#define EDMA_CH0_DST_LO    (EDMA_BASE + 0x014)
#define EDMA_CH0_DST_HI    (EDMA_BASE + 0x018)

#define EDMA_DOORBELL      (EDMA_BASE + 0x100)
#define EDMA_STATUS        (EDMA_BASE + 0x104)

#define GPU_REG_MMU_BASE            0x0500
#define GPU_REG_MMU_ENABLE          (GPU_REG_MMU_BASE + 0x00)

#define GPU_REG_PT_BASE_LO          (GPU_REG_MMU_BASE + 0x08)
#define GPU_REG_PT_BASE_HI          (GPU_REG_MMU_BASE + 0x0C)

#define GPU_REG_CTX_ID              (GPU_REG_MMU_BASE + 0x10)

#define GPU_REG_FAULT_STATUS        (GPU_REG_MMU_BASE + 0x14)
#define GPU_REG_FAULT_ADDR_LO       (GPU_REG_MMU_BASE + 0x18)
#define GPU_REG_FAULT_ADDR_HI       (GPU_REG_MMU_BASE + 0x1C)

#define REG_SM_PC_LO         0x00
#define REG_SM_PC_HI         0x04
#define REG_SM_ARG_LO        0x08
#define REG_SM_ARG_HI        0x0C
#define REG_SM_TB_SIZE       0x10
#define REG_SM_GRID_SIZE     0x14
#define REG_SM_SHARED_MEM    0x18
#define REG_SM_GLOBAL_X      0x1C
#define REG_SM_GLOBAL_Y      0x20
#define REG_SM_START         0x24
#define REG_SM_JOB_ID        0x28


#endif /* GP_GPU_REGS_H */



