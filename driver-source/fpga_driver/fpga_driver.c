/*
 * fpga_driver.c — Linux PCI driver for a custom FPGA accelerator device.
 *
 * This driver follows the same interface pattern as gpgpu_driver.c:
 *   - Matches PCI vendor/device IDs for the FPGA
 *   - Maps BAR0 for MMIO register access
 *   - Creates /dev/fpga0 as a character device
 *   - Supports read()/write()/lseek() for register access
 *
 * Usage:
 *   insmod fpga_driver.ko
 *   echo "hello" > /dev/fpga0   # write to register
 *   cat /dev/fpga0              # read from register
 *
 * NOTE: Update PCI_VENDOR_ID_FPGA and PCI_DEVICE_ID_FPGA to match
 *       your actual FPGA board's PCI IDs.
 */

#include <linux/module.h>
#include <linux/pci.h>

/* TODO: Replace with your FPGA's actual PCI vendor/device IDs */
#define PCI_VENDOR_ID_FPGA  0x10EE   /* Xilinx default — change as needed */
#define PCI_DEVICE_ID_FPGA  0x9038   /* Example — change to match your board */
#define DEVICE_NAME "fpga0"
#define CLASS_NAME  "fpga_class"

/* BAR0 size — update to match your FPGA's register space */
#define FPGA_BAR0_SIZE  4096   /* 1024 x 32-bit registers = 4KB */

static struct pci_device_id fpga_ids[] = {
    { PCI_DEVICE(PCI_VENDOR_ID_FPGA, PCI_DEVICE_ID_FPGA) },
    { 0, }
};
MODULE_DEVICE_TABLE(pci, fpga_ids);

static void __iomem *mmio_base;
static int major_number;
static struct class *fpga_class = NULL;
static struct device *fpga_device = NULL;
static unsigned long bar0_size = FPGA_BAR0_SIZE;

static ssize_t dev_read(struct file *filep, char *buffer, size_t len, loff_t *offset)
{
    uint32_t val;

    if (len == 0) return 0;
    if (*offset >= bar0_size) return 0;

    val = ioread32(mmio_base + *offset);
    if (copy_to_user(buffer, &val, sizeof(val)))
        return -EFAULT;

    *offset += sizeof(val);
    return sizeof(val);
}

static ssize_t dev_write(struct file *filep, const char *buffer, size_t len, loff_t *offset)
{
    uint32_t val;

    if (len == 0) return 0;
    if (*offset >= bar0_size) return -EFAULT;

    if (copy_from_user(&val, buffer, sizeof(val)))
        return -EFAULT;

    iowrite32(val, mmio_base + *offset);
    *offset += sizeof(val);
    return sizeof(val);
}

static struct file_operations fops = {
    .owner  = THIS_MODULE,
    .read   = dev_read,
    .write  = dev_write,
    .llseek = default_llseek,
};

static int fpga_probe(struct pci_dev *pdev, const struct pci_device_id *id)
{
    printk(KERN_INFO "fpga: Device found at %s\n", pci_name(pdev));

    if (pci_enable_device(pdev)) {
        printk(KERN_ERR "fpga: Failed to enable PCI device\n");
        return -ENODEV;
    }

    if (pci_request_regions(pdev, DEVICE_NAME)) {
        pci_disable_device(pdev);
        printk(KERN_ERR "fpga: Failed to request PCI regions\n");
        return -EBUSY;
    }

    mmio_base = pci_iomap(pdev, 0, 0);
    if (!mmio_base) {
        pci_release_regions(pdev);
        pci_disable_device(pdev);
        printk(KERN_ERR "fpga: Failed to map MMIO region\n");
        return -ENOMEM;
    }

    /* Detect actual BAR0 size from PCI config */
    bar0_size = pci_resource_len(pdev, 0);
    if (bar0_size == 0)
        bar0_size = FPGA_BAR0_SIZE;

    major_number = register_chrdev(0, DEVICE_NAME, &fops);
    fpga_class = class_create(DEVICE_NAME);
    fpga_device = device_create(fpga_class, NULL, MKDEV(major_number, 0), NULL, DEVICE_NAME);

    printk(KERN_INFO "fpga: Device initialized (BAR0 = %lu bytes)\n", bar0_size);
    return 0;
}

static void fpga_remove(struct pci_dev *pdev)
{
    device_destroy(fpga_class, MKDEV(major_number, 0));
    class_unregister(fpga_class);
    class_destroy(fpga_class);
    unregister_chrdev(major_number, DEVICE_NAME);

    pci_iounmap(pdev, mmio_base);
    pci_release_regions(pdev);
    pci_disable_device(pdev);
    printk(KERN_INFO "fpga: Device removed\n");
}

struct pci_driver fpga_driver = {
    .name     = DEVICE_NAME,
    .id_table = fpga_ids,
    .probe    = fpga_probe,
    .remove   = fpga_remove,
};

module_pci_driver(fpga_driver);

MODULE_LICENSE("GPL");
MODULE_AUTHOR("Kartheek Budime");
MODULE_DESCRIPTION("Driver for Custom FPGA PCIe Accelerator Device");
