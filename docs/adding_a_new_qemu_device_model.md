# Adding a New QEMU Device Model to DVF

This guide explains how to add a new simulated PCIe device to the DVF testing
framework so that QEMU exposes it to guest VMs during validation runs.

**Audience:** Hardware emulation / platform engineers.  
**No QEMU source knowledge required** — the framework handles the build
integration automatically.

---

## How It Works (Overview)

```
qemu-accelerator-models/
├── hw/misc/
│   ├── gp_gpu.c          ← your device model .c file lives here
│   └── meson.build       ← lists which .c files to compile (you edit this)
├── meson.build           ← Meson project that drives the full QEMU build
├── meson_options.txt     ← build options (qemu_version, qemu_source_dir)
└── scripts/
    └── build_qemu_with_models.sh  ← does the actual work (don't edit)

builds/
└── qemu/
    └── qemu-system-x86_64   ← output: the self-contained QEMU binary
```

When you run `ninja -C builds/qemu-build`, the build script automatically:

1. Clones QEMU at the pinned version (or reuses an existing source tree)
2. Copies your `.c` files into QEMU's `hw/pci/pci-dvf/` directory
3. Removes any duplicate registrations of the same file from QEMU's build
4. Inserts a `subdir('pci-dvf')` hook at the correct point in QEMU's Meson graph
5. Configures and compiles QEMU with `./configure --target-list=x86_64-softmmu`
6. Installs the binary to `builds/qemu-build/installed/` and copies it to
   `builds/qemu/qemu-system-x86_64`

---

## Step 1 — Write Your Device Model

Create your source file in `qemu-accelerator-models/hw/misc/`:

```
qemu-accelerator-models/hw/misc/my_device.c
```

### Minimal PCI device skeleton

```c
#include "qemu/osdep.h"
#include "hw/pci/pci.h"
#include "hw/pci/pci_device.h"
#include "qemu/module.h"
#include "qemu/log.h"
#include "qapi/error.h"

/* ── Type name: must match what you pass to -device on the QEMU command line ── */
#define TYPE_MY_DEVICE  "my_device"

/* ── PCI IDs — pick values that don't conflict with real hardware ── */
#define MY_VENDOR_ID  0x1234
#define MY_DEVICE_ID  0x5678

OBJECT_DECLARE_SIMPLE_TYPE(MyDevice, MY_DEVICE)

typedef struct MyDevice {
    PCIDevice parent_obj;   /* must be first */
    MemoryRegion bar0;
    uint32_t regs[256];     /* register file */
} MyDevice;

static uint64_t my_device_read(void *opaque, hwaddr addr, unsigned size)
{
    MyDevice *d = opaque;
    uint64_t val = d->regs[addr >> 2];
    qemu_log_mask(LOG_GUEST_ERROR,
                  "MY_DEVICE: READ  addr=0x%lx val=0x%lx\n",
                  (unsigned long)addr, (unsigned long)val);
    return val;
}

static void my_device_write(void *opaque, hwaddr addr,
                             uint64_t val, unsigned size)
{
    MyDevice *d = opaque;
    qemu_log_mask(LOG_GUEST_ERROR,
                  "MY_DEVICE: WRITE addr=0x%lx val=0x%lx\n",
                  (unsigned long)addr, (unsigned long)val);
    d->regs[addr >> 2] = (uint32_t)val;

    switch (addr) {
        /* handle any special register writes here */
        default:
            break;
    }
}

static const MemoryRegionOps my_device_ops = {
    .read      = my_device_read,
    .write     = my_device_write,
    .endianness = DEVICE_LITTLE_ENDIAN,
};

static void my_device_realize(PCIDevice *pdev, Error **errp)
{
    MyDevice *d = MY_DEVICE(pdev);

    pci_config_set_vendor_id(pdev->config, MY_VENDOR_ID);
    pci_config_set_device_id(pdev->config, MY_DEVICE_ID);
    pci_config_set_class(pdev->config, PCI_CLASS_OTHERS);
    pci_config_set_revision(pdev->config, 0x00);
    pci_config_set_interrupt_pin(pdev->config, 1);

    /* BAR0: 4 KB register window */
    memory_region_init_io(&d->bar0, OBJECT(d),
                          &my_device_ops, d, "my-device-bar0", 0x1000);
    pci_register_bar(pdev, 0, PCI_BASE_ADDRESS_SPACE_MEMORY, &d->bar0);
}

static void my_device_class_init(ObjectClass *klass, void *data)
{
    PCIDeviceClass *k = PCI_DEVICE_CLASS(klass);
    k->realize   = my_device_realize;
    k->vendor_id = MY_VENDOR_ID;
    k->device_id = MY_DEVICE_ID;
    k->revision  = 0;
    k->class_id  = PCI_CLASS_OTHERS;
}

static const TypeInfo my_device_info = {
    .name          = TYPE_MY_DEVICE,
    .parent        = TYPE_PCI_DEVICE,
    .instance_size = sizeof(MyDevice),
    .class_init    = my_device_class_init,
    .interfaces    = (InterfaceInfo[]) {
        { INTERFACE_CONVENTIONAL_PCI_DEVICE },
        { }
    },
};

static void my_device_register_types(void)
{
    type_register_static(&my_device_info);
}

type_init(my_device_register_types);
```

### Important rules for device model .c files

| Rule | Why |
|---|---|
| **No non-`static` globals** | The linker links all device models into one binary. Non-static globals with common names (`dma_size`, `device_mem`, etc.) cause duplicate symbol errors at link time. Make all module-level state `static`. |
| **Use unique type names** | `#define TYPE_MY_DEVICE "my_device"` — this is the string passed to `-device my_device`. Must not collide with any existing QEMU device name. |
| **Use unique PCI IDs** | Check [https://pci-ids.ucw.cz](https://pci-ids.ucw.cz) or pick IDs in the 0x1234/test range. |
| **`parent_obj` must be first field** | Required by QEMU's object model. |
| **Use `pci_ss` — not `softmmu_ss`** | These are PCI devices; they live at the `hw/pci/` level of QEMU's Meson build graph. The `meson.build` fragment already uses `pci_ss.add()` — don't change it. |

---

## Step 2 — Register It in `hw/misc/meson.build`

Open `qemu-accelerator-models/hw/misc/meson.build` and add your file to
`dvf_pci_sources`:

```meson
dvf_pci_sources = files(
  'gp_gpu.c',
  'my_device.c',    # ← add this line
)

pci_ss.add(when : 'CONFIG_PCI', if_true : dvf_pci_sources)
```

That's the only file you edit. Everything else is automatic.

---

## Step 3 — Build

Run the build script from the **repo root**. You don't need a QEMU source tree
— the script clones it for you:

```bash
bash qemu-accelerator-models/scripts/build_qemu_with_models.sh \
    $(pwd)/qemu-accelerator-models \   # models root
    $(pwd)/builds/qemu-build \         # where to put build artefacts
    v8.2.0 \                           # QEMU version to clone
    ""                                 # leave empty → script clones QEMU automatically
```

That's it. The script handles everything:

| What it does automatically |
|---|
| Clones QEMU v8.2.0 into `builds/qemu-build/qemu-src/` (one-time) |
| Copies your `.c` files into the QEMU source tree (`hw/pci/pci-dvf/`) |
| Strips any duplicate registrations of those files |
| Wires the new subdirectory into QEMU's Meson build graph |
| Runs `./configure --target-list=x86_64-softmmu` |
| Compiles QEMU with all parallel cores |
| Puts the finished binary at `builds/qemu/qemu-system-x86_64` |

**First run:** ~10-15 minutes (clone + compile).  
**Subsequent runs** (after editing a `.c` file): ~30 seconds (incremental, no re-clone).

> **Already have QEMU cloned on your machine?**  
> Pass its path as the 4th argument to skip the clone and save time:
> ```bash
> bash qemu-accelerator-models/scripts/build_qemu_with_models.sh \
>     $(pwd)/qemu-accelerator-models \
>     $(pwd)/builds/qemu-build \
>     v8.2.0 \
>     $HOME/VirtualMachines/qemu    # ← use existing checkout, skip clone
> ```

The binary lands at:
```
builds/qemu/qemu-system-x86_64
```

---

## Step 4 — Verify

```bash
export DVF_ROOT=$(pwd)   # repo root

$DVF_ROOT/builds/qemu/qemu-system-x86_64 -device my_device,help 2>&1
# Expected output:
#   my_device options:
#     acpi-index=<uint32>   (default: 0)
#     addr=<int32>  ...
```

If you see `my_device options:` the device is compiled in correctly.

---

## Step 5 — Register the Device in the Orchestrator

Open `go-orchestrator/configs/device_registry.json` and add an entry:

```json
{
  "id": "my_device",
  "name": "My Custom Device",
  "qemu_device_name": "my_device",
  "driver_module": "my_device_driver.ko",
  "dev_node": "/dev/my_device",
  "pci_vendor_id": "0x1234",
  "pci_device_id": "0x5678",
  "test_suites": []
}
```

The `qemu_device_name` value is passed directly to QEMU's `-device` flag by
`vm/manager.go:BuildQEMUArgs()`. It must exactly match `TYPE_MY_DEVICE` in
your `.c` file.

---

## Step 6 — Write Tests Against the Device

Follow [docs/adding_a_new_test.md](adding_a_new_test.md) (Scenario B). Quick
summary:

```
c-test-binaries/
└── my_category/
    ├── my_test.c       ← uses dvf_open_device() / dvf_read_reg() / dvf_write_reg()
    └── my_test.json    ← sidecar: "device_id": "my_device"
```

Add to the Makefile and push — CI handles the rest.

---

## Step 7 — Commit

```bash
git add qemu-accelerator-models/hw/misc/my_device.c
git add qemu-accelerator-models/hw/misc/meson.build
git add go-orchestrator/configs/device_registry.json
git add c-test-binaries/my_category/

git commit -m "feat(qemu): add my_device PCI device model"
git push
```

CI rebuilds QEMU with the new device on the next run.

---

## Troubleshooting

### `multiple definition of 'foo'` linker error

Your `.c` file has a non-`static` global variable with the same name as another
device model. **Fix:** add `static` to every module-level variable.

```c
/* Bad — leaks into global symbol table */
uint32_t dma_size = 0;

/* Good — scoped to this translation unit */
static uint32_t dma_size = 0;
```

### `Nonexistent build file 'hw/pci/pci-dvf/meson.build'`

The `subdir('pci-dvf')` line was appended to `hw/pci/meson.build` instead of
inserted before `system_ss.add_all()`. The build script handles this
automatically — if you see this error, wipe the build dir and retry:

```bash
rm -rf builds/qemu-build/qemu-build-inner
ninja -C builds/qemu-build
```

### `Tried to use 'add' after querying the source set`

Same root cause as above — `pci_ss.add()` was called after `pci_ss` was
finalised. Same fix: wipe `qemu-build-inner` and let the script reconfigure.

### Device not found: `unknown device my_device`

The `TYPE_MY_DEVICE` string in your `.c` file doesn't match what you passed
to `-device`. They must be identical. Check:

```c
#define TYPE_MY_DEVICE "my_device"   /* this string is what -device expects */
```

---

## Quick Reference

| File to edit | What to do |
|---|---|
| `hw/misc/my_device.c` | **[NEW]** Write the device model |
| `hw/misc/meson.build` | Add `'my_device.c',` to `dvf_pci_sources` |
| `go-orchestrator/configs/device_registry.json` | Add device entry with `qemu_device_name` |
| `c-test-binaries/<category>/<test>.c` | Write tests using the DVF test framework |
| `c-test-binaries/Makefile` | Add test binary to `TESTS` list |

| Command | When |
|---|---|
| `meson setup ../builds/qemu-build -Dqemu_source_dir=<path>` | First time on a machine |
| `ninja -C ../builds/qemu-build` | Every time you add or change a `.c` file |
| `builds/qemu/qemu-system-x86_64 -device <name>,help` | Verify device is compiled in |
| `export DVF_ROOT=$(pwd)` | Before starting the orchestrator |
