#include <linux/module.h>
#include <linux/pci.h>
#include <linux/fs.h>
#include <linux/cdev.h>
#include <linux/device.h>
#include <linux/uaccess.h>
#include <linux/ioctl.h>
#include <linux/types.h>
#include <linux/delay.h>

#include "uapi/linux/gpgpu_ioctl.h"
#include "gpgpu_memory_manager.h"
#include "gpgpu_dma.h"
#include "gpgpu_regs.h"
#define VENDOR_ID 0x1222
#define DEVICE_ID 0x2221

#define DEVICE_NAME "gp_gpu"
#define CLASS_NAME  "gp_gpu_class"

#define GPU_DDR_BASE_PHYS   0x80000000ULL   // From BAR
#define GPU_DDR_SIZE        (128ULL * 1024 * 1024) // 128MB

#define GPU_PAGE_SIZE  4096

#define SM_START_TRIGGER 1

static int major;
static struct class *gp_gpu_class;
static struct cdev gp_gpu_cdev;
static struct gp_gpu_dev *gdev;


#include <linux/slab.h>
#include <linux/wait.h>
#include <linux/spinlock.h>
#include <linux/list.h>

static LIST_HEAD(buffer_list);
static LIST_HEAD(job_list);

static int next_handle = 1;
static uint32_t next_job_id = 1;

static DEFINE_SPINLOCK(gpu_lock);

static struct gp_gpu_job_entry *active_job = NULL;


/* MSI-X vector setup */
#define GP_GPU_MSIX_VEC_COUNT 1

static struct msix_entry
msix_entries[GP_GPU_MSIX_VEC_COUNT];

/* ISR Declaration */
static irqreturn_t gp_gpu_msix_handler(int irq, void *data);

static inline u32 gpu_reg_read(u32 reg)
{
    return ioread32(gdev->bar0 + reg);
}

static inline void gpu_reg_write(u32 reg, u32 val)
{
    iowrite32(val, gdev->bar0 + reg);
}


static struct gp_gpu_buffer *find_buffer(int handle)
{
    struct gp_gpu_buffer *buf;

    list_for_each_entry(buf, &buffer_list, list) {

        if (buf->handle == handle)
            return buf;
    }
    return NULL;
}

static struct gp_gpu_job_entry *find_job(int job_id)
{
    struct gp_gpu_job_entry *job;

    list_for_each_entry(job, &job_list, list) {
        if (job->job_id == job_id)
            return job;
    }
    return NULL;
}
/* ---------- Character device operations ---------- */
static int gp_gpu_open(struct inode *inode, struct file *file)
{
    pr_info("gp_gpu: device opened\n");
    return 0;
}

static int gp_gpu_release(struct inode *inode, struct file *file)
{
    pr_info("gp_gpu: device closed\n");
    return 0;
}

static long gp_gpu_ioctl(struct file *file,
                         unsigned int cmd,
                         unsigned long arg)
{
    switch (cmd) {
        case GP_GPU_IOCTL_REQ_BUF:
        {
            struct gp_gpu_buf ubuf;
            struct gp_gpu_buffer *kbuf;

            if (copy_from_user(&ubuf, (void __user *)arg, sizeof(ubuf)))
                return -EFAULT;

            if (ubuf.ctx_id == 0) {
                pr_err("Invalid ctx_id\n");
                return -EINVAL;
            }

            kbuf = kzalloc(sizeof(*kbuf), GFP_KERNEL);
            if (!kbuf)
                return -ENOMEM;

            kbuf->size = ubuf.size;
#if 0
            kbuf->cpu_addr = kmalloc(ubuf.size, GFP_KERNEL);
            if (!kbuf->cpu_addr) {
                kfree(kbuf);
                return -ENOMEM;
            }
#else
            kbuf->cpu_addr = dma_alloc_coherent(&gdev->pdev->dev,
                                        ubuf.size,
                                        &kbuf->dma_handle,
                                        GFP_KERNEL);
            struct gpu_alloc_req alloc_req;

            kbuf->ctx_id = ubuf.ctx_id;

            alloc_req.ctx_id = ubuf.ctx_id;
            alloc_req.size   = ubuf.size;

            if (gpu_alloc(&alloc_req) != 0) {
                dma_free_coherent(&gdev->pdev->dev,
                                  ubuf.size,
                                  kbuf->cpu_addr,
                                  kbuf->dma_handle);
                kfree(kbuf);
                return -ENOMEM;
            }

            kbuf->gpu_va = alloc_req.va;
            kbuf->gpu_pa = alloc_req.pa;

            pr_info("GPU buffer allocated\n");
            pr_info("ctx_id = %d\n", ubuf.ctx_id);
            pr_info("host_va = 0x%px\n", kbuf->cpu_addr);
            pr_info("gpu_va = 0x%llx\n", kbuf->gpu_va);
            pr_info("gpu_pa = 0x%llx\n", kbuf->gpu_pa);
#endif

            spin_lock(&gpu_lock);
            kbuf->handle = next_handle++;
            list_add(&kbuf->list, &buffer_list);
            spin_unlock(&gpu_lock);

            ubuf.handle = kbuf->handle;

            if (copy_to_user((void __user *)arg, &ubuf, sizeof(ubuf)))
                return -EFAULT;

            pr_info("Buffer allocated: handle=%d size=%u \n",
                     ubuf.handle, ubuf.size);
            break;
        }

        case GP_GPU_IOCTL_WAIT:
        {
            struct gp_gpu_wait uwait;
            struct gp_gpu_job_entry *job;

            if (copy_from_user(&uwait, (void __user *)arg, sizeof(uwait)))
                return -EFAULT;

            job = find_job(uwait.job_id);
            if (!job)
                return -EINVAL;

            if (!job->done) {
                long ret = wait_event_interruptible_timeout(
                                job->wq,
                                job->done,
                                msecs_to_jiffies(uwait.timeout_ms)
                            );

                if (ret == 0) {
                    pr_err("WAIT TIMEOUT for job_id=%u\n", (unsigned) job->job_id);
                    return -ETIMEDOUT;
                }
            }
            pr_info("Job completed: job_id=%d\n", job->job_id);

            /*Debug*/
            pr_info("[DRIVER] freeing job entry job_id=%u ptr=%px\n", job->job_id, job);

            /* Free memory */
            list_del(&job->list);
            kfree(job);

            /*Debug*/
            pr_info("[DRIVER] job entry freed\n");

            break;
        }

        case GP_GPU_IOCTL_WRITE_BUF:
        {
            struct gp_gpu_buf_xfer xfer;
            struct gp_gpu_buffer *buf;

            if (copy_from_user(&xfer, (void __user *)arg, sizeof(xfer)))
                return -EFAULT;

            buf = find_buffer(xfer.handle);
            if (!buf)
                return -EINVAL;

            if (xfer.size > buf->size)
                return -EINVAL;

            if (copy_from_user(buf->cpu_addr,
                               (void __user *)xfer.user_ptr,
                               xfer.size))
                return -EFAULT;

            gp_gpu_dma(gdev,
                       buf->dma_handle,
                       buf->gpu_pa,
                       xfer.size,
                       DMA_H2D);

            break;
        }

        case GP_GPU_IOCTL_READ_BUF:
        {
            struct gp_gpu_buf_xfer xfer;
            struct gp_gpu_buffer *buf;

            if (copy_from_user(&xfer, (void __user *)arg, sizeof(xfer)))
                return -EFAULT;

            buf = find_buffer(xfer.handle);
            if (!buf)
                return -EINVAL;

            if (xfer.size > buf->size)
                return -EINVAL;

            gp_gpu_dma(gdev,
                       buf->gpu_pa,
                       buf->dma_handle,
                       xfer.size,
                       DMA_D2H);

            if (copy_to_user((void __user *)xfer.user_ptr,
                             buf->cpu_addr,
                             xfer.size))
                return -EFAULT;

            break;
        }

        case GP_GPU_IOCTL_GET_INFO:
        {
            struct gp_gpu_info info;

            memset(&info, 0, sizeof(info));

            /* PCI */
            info.vendor_id = gdev->pdev->vendor;
            info.device_id = gdev->pdev->device;

            /* BAR */
            info.bar0_addr = pci_resource_start(gdev->pdev, 0);
            info.bar0_size = pci_resource_len(gdev->pdev, 0);

            /* Memory */
            info.gpu_ddr_base = GPU_DDR_BASE_PHYS;
            info.gpu_ddr_size = GPU_DDR_SIZE;

            /* System */
            info.page_size = GPU_PAGE_SIZE;
            info.dma_bits =
                dma_get_mask(&gdev->pdev->dev) == DMA_BIT_MASK(64) ? 64 : 32;

            info.has_mmu  = 1;
            info.has_edma = 1;

            /* Capabilities */
            info.version       = 1;
            info.num_threads   = 8;
            info.num_warps     = 32;
            info.num_cores     = 64;
            info.num_barriers  = 4;

            info.global_mem_size = 512;
            info.local_mem_size  = 512;
            info.local_mem_addr  = 0xC0000000;

            info.isa_caps = 0x1;

            if (copy_to_user((void __user *)arg, &info, sizeof(info)))
                return -EFAULT;
            pr_info("Filled Info Details\n");
            break;
        }

        case GPU_IOCTL_CTX_CREATE:
        {
            struct gpu_ctx_create_req req;
            if( copy_from_user(&req, (void __user *)arg, sizeof(req)))
                return -EFAULT;

            gpu_ctx_create(gdev->pdev, &req);

            if (copy_to_user((void __user *)arg, &req, sizeof(req)))
                return -EFAULT;
            break;
        }

        case GPU_IOCTL_CTX_DESTROY:
        {
            break;
        }

        case GPU_IOCTL_FREE:
        {
            struct gpu_free_req req;
            if( copy_from_user(&req, (void __user *)arg, sizeof(req)))
                return -EFAULT;

            gpu_free(&req);
            break;
        }

        case GPU_IOCTL_GET_VA:
        {
            struct gpu_get_va_req req;
            struct gp_gpu_buffer *kbuf = NULL;
            if (copy_from_user(&req, (void __user *)arg, sizeof(req)))
                return -EFAULT;

            kbuf = find_buffer(req.handle);

            if (!kbuf)
                return -EINVAL;

            req.gpu_va = kbuf->gpu_va;

            if (copy_to_user((void __user *)arg, &req, sizeof(req)))
                return -EFAULT;
            return 0;
        }

        case GP_GPU_IOCTL_SUBMIT:
        {
            struct gp_gpu_kernel_job job;

            if (copy_from_user(&job, (void __user *)arg, sizeof(job))) {
                return -EFAULT;
            }
#ifdef GET_VIRTUAL_ADDRESS_FROM_HANDLE
            struct gp_gpu_buffer *prog_buf = find_buffer(job.kernel_handle);

            if (!prog_buf) {
                pr_err("Kernel buffer not found for handle = %llu\n", (unsigned long long)job.kernel_handle);
                return -EINVAL;
            }

            uint64_t kernel_va = (uint64_t)prog_buf->gpu_va;

            struct gp_gpu_buffer *arg_buf = find_buffer(job.arg_handle);
            if (!arg_buf) {
                pr_err("arg buffer not found for handle = %llu\n",(unsigned long long)job.arg_handle);
                return -EINVAL;
            }
            uint64_t arg_va = (uint64_t)arg_buf->gpu_va;
#else
            uint64_t arg_va = job.arg_handle;
            uint64_t kernel_va = job.kernel_handle;
#endif

            pr_info("gp_gpu: kernel job received\n");

            pr_info("  kernel_va   = 0x%llx\n", (unsigned long long)kernel_va);
            pr_info("  arg_va      = 0x%llx\n", (unsigned long long)arg_va);

            pr_info("  tb_size    = %u\n", job.tb_size);
            pr_info("  grid_size  = %u\n", job.grid_size);
            pr_info("  shared_mem = %u\n", job.shared_mem);
            pr_info("  global_x   = %u\n", job.global_tds_x);
            pr_info("  global_y   = %u\n", job.global_tds_y);


            /* Program registers */
            gpu_reg_write(REG_SM_PC_LO, lower_32_bits(kernel_va));
            gpu_reg_write(REG_SM_PC_HI, upper_32_bits(kernel_va));

            gpu_reg_write(REG_SM_ARG_LO, lower_32_bits(arg_va));
            gpu_reg_write(REG_SM_ARG_HI, upper_32_bits(arg_va));

            gpu_reg_write(REG_SM_TB_SIZE, job.tb_size);
            gpu_reg_write(REG_SM_GRID_SIZE, job.grid_size);
            gpu_reg_write(REG_SM_SHARED_MEM, job.shared_mem);

            gpu_reg_write(REG_SM_GLOBAL_X, job.global_tds_x);
            gpu_reg_write(REG_SM_GLOBAL_Y, job.global_tds_y);

            /* update page table before sending job*/
            gpu_bind_context(kernel_va >> 48);

            pr_info("gp_gpu: registers programmed successfully\n");

            /* Job entry */
            struct gp_gpu_job_entry *kjob;

            kjob = kmalloc(sizeof(*kjob), GFP_KERNEL);
            if(!kjob) {
                return -ENOMEM;
            }

            kjob->job_id = next_job_id++;
            kjob->done = 0;
            init_waitqueue_head(&kjob->wq);

            list_add_tail(&kjob->list, &job_list);

            /* send job id back */
            job.job_id = kjob->job_id;

            if(copy_to_user((void __user *)arg, &job, sizeof(job)))
                return -EFAULT;

            active_job = kjob;
            pr_info("[DRIVER] submit job_id=%u ptr=%px\n",(unsigned)kjob->job_id, kjob);

            /*Writing job_id */
            gpu_reg_write(REG_SM_JOB_ID, kjob->job_id);

            /* Trigger execution */
            gpu_reg_write(REG_SM_START, SM_START_TRIGGER);

            return 0;
        }

        default:
            return -EINVAL;
    }
    return 0;
}

static const struct file_operations gp_gpu_fops = {
    .owner          = THIS_MODULE,
    .open           = gp_gpu_open,
    .release        = gp_gpu_release,
    .unlocked_ioctl = gp_gpu_ioctl,
};


/* ---------- PCI Driver ---------- */
static int gp_gpu_probe(struct pci_dev *pdev,
                        const struct pci_device_id *id)
{
    dev_t dev;
    int ret;

    printk("CDAC GPGPU device detected\n");


    gdev = kzalloc(sizeof(*gdev), GFP_KERNEL);
    if (!gdev)
       return -ENOMEM;
    gdev->pdev = pdev;

    ret = pci_enable_device(pdev);
    if (ret) {
        pr_err("pci_enable_device failed\n");
        return ret;
    }
    /* Enable bus mastering */
    pci_set_master(pdev);

    gdev->bar0 = pci_iomap(pdev, 0, 0);
    if (!gdev->bar0)
        return -ENOMEM;

    /* Setup MSI-X interrupt */

    msix_entries[0].entry = 0;

    ret = pci_enable_msix_exact(pdev, msix_entries, 1);
    if(ret){
        pr_err("pci_enable_msix_exact failed\n");
        return ret;
    }

    ret = request_irq(msix_entries[0].vector,gp_gpu_msix_handler,0,"gp_gpu_msix",gdev);

    if(ret) {
        pr_err("request_irq failed\n");
        pci_disable_msix(pdev);
        return ret;
    }

    pr_info("gp_gpu: MSI-X enabled, vector = %d\n",msix_entries[0].vector);

    /* Create character device */
    ret = alloc_chrdev_region(&dev, 0, 1, DEVICE_NAME);
    if (ret)
        return ret;

    major = MAJOR(dev);

    cdev_init(&gp_gpu_cdev, &gp_gpu_fops);
    ret = cdev_add(&gp_gpu_cdev, dev, 1);
    if (ret)
       goto err_chrdev;

    gp_gpu_class = class_create(CLASS_NAME);
    if (IS_ERR(gp_gpu_class)) {
        ret = PTR_ERR(gp_gpu_class);
        goto err_cdev;
    }

    device_create(gp_gpu_class, NULL, dev, NULL, DEVICE_NAME);

    pr_info("gp_gpu: character device /dev/gp_gpu created\n");
    gpu_initialize_memory_manager(GPU_DDR_BASE_PHYS, GPU_DDR_SIZE, gdev);
    return 0;

err_cdev:
    cdev_del(&gp_gpu_cdev);
err_chrdev:
    unregister_chrdev_region(dev, 1);

    return ret;
}

static void gp_gpu_remove(struct pci_dev *pdev)
{
    dev_t dev = MKDEV(major, 0);

    device_destroy(gp_gpu_class, dev);
    class_destroy(gp_gpu_class);
    cdev_del(&gp_gpu_cdev);
    unregister_chrdev_region(dev, 1);

    pci_disable_device(pdev);

    printk("CDAC GPGPU removed\n");
}

static struct pci_device_id gp_gpu_ids[] = {
    { PCI_DEVICE(VENDOR_ID, DEVICE_ID) },
    { 0 }
};

MODULE_DEVICE_TABLE(pci, gp_gpu_ids);

static struct pci_driver gp_gpu_pci_driver = {
    .name     = "cdac_gpgpu",
    .id_table = gp_gpu_ids,
    .probe    = gp_gpu_probe,
    .remove   = gp_gpu_remove,
};

static irqreturn_t gp_gpu_msix_handler(int irq, void *data)
{
    struct gp_gpu_job_entry *kjob;

    pr_info("gp_gpu: MSI-X interrupt received\n");
    uint32_t completed_job_id = gpu_reg_read(REG_SM_JOB_ID);

    list_for_each_entry(kjob, &job_list, list) {
        if(kjob->job_id == completed_job_id) {
            kjob->done = 1;
            wake_up(&kjob->wq);
            pr_info("gp_gpu: job %u completed\n", (unsigned)kjob->job_id);
            break;
        }
    }

    return IRQ_HANDLED;
}
module_pci_driver(gp_gpu_pci_driver);

MODULE_LICENSE("GPL");
MODULE_AUTHOR("HCLTech");
MODULE_DESCRIPTION("CDAC GPGPU PCIe endpoint driver");
