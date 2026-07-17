#ifndef _UAPI_GP_GPU_IOCTL_H_
#define _UAPI_GP_GPU_IOCTL_H_

#include <linux/types.h>


struct gp_gpu_kernel_job {
    uint32_t job_id;
    uint64_t kernel_handle;
    uint64_t arg_handle;

    uint32_t tb_size;
    uint32_t grid_size;
    uint32_t shared_mem;

    uint32_t global_tds_x;
    uint32_t global_tds_y;
};


struct gp_gpu_wait {
    __u32 job_id;
    __u32 timeout_ms;
};

struct gp_gpu_buf_xfer {
    __u32 handle;
    __u64 user_ptr;
    __u32 size;
    __u32 ctx_id;
};

struct gp_gpu_buf {
    __u32 size;
    __u32 handle;
    __u32 ctx_id;
};

struct gp_gpu_info {
    __u32 vendor_id;
    __u32 device_id;

    __u64 bar0_addr;
    __u32 bar0_size;

    __u64 gpu_ddr_base;
    __u64 gpu_ddr_size;

    __u32 page_size;
    __u32 dma_bits;

    __u32 has_mmu;
    __u32 has_edma;

    /*  Capability fields */
    __u32 version;
    __u32 num_threads;
    __u32 num_warps;
    __u32 num_cores;
    __u32 num_barriers;

    __u64 global_mem_size;
    __u64 local_mem_size;
    __u64 local_mem_addr;

    __u64 isa_caps;

    __u32 reserved[8];
};

struct gpu_ctx_create_req {
    uint32_t ctx_id;
};

struct gpu_alloc_req {
    uint32_t ctx_id;
    uint64_t size;
    uint64_t va;
    uint64_t pa;
};

struct gpu_free_req {
    uint32_t ctx_id;
    uint32_t size;
    uint64_t va;
};

struct gpu_get_va_req {
    __u32 handle;
    __u64 gpu_va;
};

/* IOCTL definitions */
#define GP_GPU_IOCTL_MAGIC  'G'
#define GP_GPU_IOCTL_REQ_BUF   _IOWR(GP_GPU_IOCTL_MAGIC, 3, struct gp_gpu_buf)
#define GP_GPU_IOCTL_SUBMIT _IOWR(GP_GPU_IOCTL_MAGIC, 4, struct gp_gpu_kernel_job)
#define GP_GPU_IOCTL_WAIT _IOW(GP_GPU_IOCTL_MAGIC, 5, struct gp_gpu_wait)
#define GP_GPU_IOCTL_WRITE_BUF _IOW(GP_GPU_IOCTL_MAGIC, 6, struct gp_gpu_buf_xfer)
#define GP_GPU_IOCTL_READ_BUF  _IOWR(GP_GPU_IOCTL_MAGIC, 7, struct gp_gpu_buf_xfer)
#define GP_GPU_IOCTL_GET_INFO _IOR(GP_GPU_IOCTL_MAGIC, 12, struct gp_gpu_info)
#define GPU_IOCTL_CTX_CREATE    _IOWR(GP_GPU_IOCTL_MAGIC, 13, struct gpu_ctx_create_req)
#define GPU_IOCTL_CTX_DESTROY   _IOW(GP_GPU_IOCTL_MAGIC,  14, uint32_t)
#define GPU_IOCTL_FREE          _IOW(GP_GPU_IOCTL_MAGIC,  16, struct gpu_free_req)
#define GPU_IOCTL_GET_VA      _IOWR(GP_GPU_IOCTL_MAGIC, 18, struct gpu_get_va_req)


#endif
