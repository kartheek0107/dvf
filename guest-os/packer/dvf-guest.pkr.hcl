# DVF Guest OS — Packer Template
#
# Builds a minimal Alpine Linux image with the DVF Python agent pre-installed
# and configured to start automatically via systemd/OpenRC.
#
# Prerequisites:
#   - packer >= 1.10
#   - QEMU installed (qemu-system-x86_64)
#
# Usage:
#   cd guest-os/packer
#   packer init .
#   packer build dvf-guest.pkr.hcl
#
# Output: ../output/dvf-guest.ext4 (raw ext4 image, ~500MB)

packer {
  required_plugins {
    qemu = {
      version = ">= 1.0.9"
      source  = "github.com/hashicorp/qemu"
    }
  }
}

source "qemu" "dvf_guest" {
  # --- ISO ---
  iso_url      = var.alpine_iso_url
  iso_checksum = var.alpine_iso_checksum

  # --- Disk ---
  disk_size        = var.disk_size_mb
  format           = "raw"
  disk_compression = true

  # --- VM resources ---
  memory   = var.memory_mb
  cpus     = var.cpus
  headless = true

  # --- Output ---
  output_directory = "${var.output_dir}"
  vm_name          = var.output_filename

  # --- SSH (Packer communicates with the VM via SSH during provisioning) ---
  ssh_username     = "root"
  ssh_password     = "dvf_root"
  ssh_timeout      = var.ssh_timeout
  shutdown_command = "poweroff"

  # --- Boot sequence: Alpine auto-install answer file ---
  # The boot_command types the Alpine setup answers interactively.
  boot_wait = "30s"
  boot_command = [
    # Login as root (no password on Alpine virt ISO)
    "root<enter><wait5>",

    # Configure network
    "setup-interfaces -a<enter><wait3>",
    "ifup eth0<enter><wait5>",

    # Install Alpine to disk non-interactively
    # ALPINE_SETUP answers: keyboard=us, hostname=dvf-guest, disk=vda, no swap
    "KEYMAPOPTS='us us' ",
    "HOSTNAMEOPTS='-n dvf-guest' ",
    "INTERFACESOPTS='auto lo\\niface lo inet loopback\\nauto eth0\\niface eth0 inet dhcp' ",
    "DNSOPTS='-d local -n 8.8.8.8' ",
    "TIMEZONEOPTS='-z UTC' ",
    "PROXYOPTS='none' ",
    "APKREPOSOPTS='-f' ",
    "SSHDOPTS='-c openssh' ",
    "NTPOPTS='-c none' ",
    "DISKOPTS='-m sys /dev/vda' ",
    "setup-alpine -f /dev/stdin <<'ALPINE_EOF'<enter><wait>",
    "ALPINE_EOF<enter><wait60>",

    # Set root password
    "echo 'root:dvf_root' | chpasswd<enter><wait3>",

    # Allow SSH root login (needed for Packer provisioners)
    "echo 'PermitRootLogin yes' >> /etc/ssh/sshd_config<enter>",
    "rc-service sshd restart<enter><wait5>",
  ]

  # Enable KVM if available for faster builds
  qemuargs = [
    ["-machine", "type=q35,accel=kvm:tcg"],
    ["-cpu", "host"],
  ]
}

build {
  name    = "dvf-guest"
  sources = ["source.qemu.dvf_guest"]

  # --- Step 1: Copy the Python agent source into the VM ---
  provisioner "file" {
    source      = "../../python-agent/"
    destination = "/tmp/python-agent/"
  }

  # --- Step 2: Copy the systemd unit ---
  provisioner "file" {
    source      = "../systemd/dvf-agent.service"
    destination = "/tmp/dvf-agent.service"
  }

  # --- Step 3: Run provision scripts ---
  provisioner "shell" {
    scripts = [
      "../scripts/00_base.sh",
      "../scripts/01_agent.sh",
      "../scripts/02_systemd.sh",
      "../scripts/03_finalize.sh",
    ]
    environment_vars = [
      "DVF_SHARE_DIR=/mnt/share",
    ]
    execute_command = "chmod +x {{ .Path }}; {{ .Path }}"
  }

  # --- Post-processing: convert raw image to ext4 ---
  post-processor "shell-local" {
    inline = [
      "echo '=== Converting raw image to ext4 ==='",
      "# The raw disk image is already ext4-formatted by setup-alpine.",
      "# Rename to the expected filename.",
      "mv '${var.output_dir}/${var.output_filename}' '${var.output_dir}/${var.output_filename}.tmp' || true",
      "echo 'Image ready: ${var.output_dir}/${var.output_filename}'",
    ]
  }
}
