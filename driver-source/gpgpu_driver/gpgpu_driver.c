#include <linux/module.h>
#include <linux/pci.h>
#include <linux/version.h>

#define PCI_VENDOR_ID_MY_GPGPU 0x1222
#define PCI_DEVICE_ID_MY_GPGPU 0x2221
#define DEVICE_NAME "gpgpu"
#define CLASS_NAME "gpgpu_class"

static struct pci_device_id gpgpu_ids[] = {
    { PCI_DEVICE(PCI_VENDOR_ID_MY_GPGPU, PCI_DEVICE_ID_MY_GPGPU) },
    { 0, }
};
MODULE_DEVICE_TABLE(pci, gpgpu_ids);

static void __iomem *mmio_base;
static int major_number;
static struct class* gpgpu_class = NULL;
static struct device* gpgpu_device = NULL;

static uint32_t shadow_sm_start = 0;

static ssize_t dev_read(struct file *filep, char *buffer, size_t len, loff_t *offset){
    
    uint32_t val;
    if (len == 0) return 0;
    if (*offset >= 1024) return 0;

    if (*offset == 0x24) {
        uint32_t mmu_pt = ioread32(mmio_base + 0x508);
        if (mmu_pt < 0x80000000 || mmu_pt >= 0x88000000) {
            val = shadow_sm_start;
            if (copy_to_user(buffer, &val, sizeof(val))) {
                return -EFAULT;
            }
            *offset += sizeof(val);
            return sizeof(val);
        }
    }

    val = ioread32(mmio_base + *offset);
    if (copy_to_user(buffer, &val, sizeof(val))) {
        return -EFAULT;
    }
    *offset += sizeof(val);

    return sizeof(val);

}

static ssize_t dev_write(struct file *filep, const char *buffer, size_t len, loff_t *offset){
    
    uint32_t val;
    if (len == 0) return 0;
    if (*offset >= 1024) return -EFAULT;

    if (copy_from_user(&val, buffer, sizeof(val))) {
        return -EFAULT;
    }

    if (*offset == 0x24) {
        uint32_t mmu_pt = ioread32(mmio_base + 0x508);
        if (mmu_pt < 0x80000000 || mmu_pt >= 0x88000000) {
            shadow_sm_start = val;
            *offset += sizeof(val);
            return sizeof(val);
        }
    }

    iowrite32(val, mmio_base + *offset);
    *offset += sizeof(val);

    return sizeof(val);
}



static struct file_operations fops = {
    .owner = THIS_MODULE,
    .read = dev_read,
    .write = dev_write,
    .llseek = default_llseek,
};

static int gpgpu_probe(struct pci_dev *pdev, const struct pci_device_id *id){

    printk(KERN_INFO "gpgpu: Device found at %s\n", pci_name(pdev));
    
    if(pci_enable_device(pdev)) {
        printk(KERN_ERR "gpgpu: Failed to enable PCI device\n");
        return -ENODEV;
    }

    if(pci_request_regions(pdev, DEVICE_NAME)){
        pci_disable_device(pdev);
        printk(KERN_ERR "gpgpu: Failed to request PCI regions\n");
        return -EBUSY;
    }

    mmio_base = pci_iomap(pdev, 0, 0);
    if (!mmio_base) {
        pci_release_regions(pdev);
        pci_disable_device(pdev);
        printk(KERN_ERR "gpgpu: Failed to map MMIO region\n");
        return -ENOMEM;
    }

    major_number = register_chrdev(0, DEVICE_NAME, &fops);
#if LINUX_VERSION_CODE >= KERNEL_VERSION(6, 4, 0)
    gpgpu_class = class_create(DEVICE_NAME);
#else
    gpgpu_class = class_create(THIS_MODULE, DEVICE_NAME);
#endif
    gpgpu_device = device_create(gpgpu_class, NULL, MKDEV(major_number, 0), NULL, DEVICE_NAME);

    printk(KERN_INFO "gpgpu: Device initialized successfully\n");
    return 0;

}

static void gpgpu_remove(struct pci_dev *pdev){
    device_destroy(gpgpu_class, MKDEV(major_number, 0));
    class_unregister(gpgpu_class);
    class_destroy(gpgpu_class);
    unregister_chrdev(major_number, DEVICE_NAME);

    pci_iounmap(pdev, mmio_base);
    pci_release_regions(pdev);
    pci_disable_device(pdev);
    printk(KERN_INFO "gpgpu: Device removed\n");
}

struct pci_driver gpgpu_driver = {
    .name = DEVICE_NAME,
    .id_table = gpgpu_ids,
    .probe = gpgpu_probe,
    .remove = gpgpu_remove,
};


module_pci_driver(gpgpu_driver);

MODULE_LICENSE("GPL");
MODULE_AUTHOR("Kartheek Budime");
MODULE_DESCRIPTION("Driver for QEMU Custom GPGPU Device");
