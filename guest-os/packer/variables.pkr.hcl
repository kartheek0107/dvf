# DVF Guest OS — Packer Variables
# Override these on the command line:
#   packer build -var 'output_dir=/custom/path' dvf-guest.pkr.hcl

variable "alpine_iso_url" {
  type    = string
  default = "https://dl-cdn.alpinelinux.org/alpine/v3.20/releases/x86_64/alpine-virt-3.20.0-x86_64.iso"
}

variable "alpine_iso_checksum" {
  type    = string
  # SHA256 of alpine-virt-3.20.0-x86_64.iso — update when Alpine version changes
  default = "sha256:sha256:0c1ef3f9dbc50da8e95b5e4e4e33fa668d6bcbe4af7e01ace5df07a5a1b7b2ab"
}

variable "disk_size_mb" {
  type    = number
  default = 4096
}

variable "memory_mb" {
  type    = number
  default = 1024
}

variable "cpus" {
  type    = number
  default = 2
}

variable "output_dir" {
  type    = string
  default = "output"
}

variable "output_filename" {
  type    = string
  default = "dvf-guest.ext4"
}

variable "ssh_timeout" {
  type    = string
  default = "20m"
}
