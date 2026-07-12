# DVF Guest OS — Makefile
#
# Builds the QEMU guest image for the DVF orchestrator.
#
# Usage:
#   make build           — run packer build (downloads Alpine ISO, ~10-15 min)
#   make validate        — validate the Packer template without building
#   make clean           — remove built image
#   make install         — copy image to the vm_image_dir from global_config.json

PACKER         := packer
PACKER_DIR     := packer
OUTPUT_DIR     := output
IMAGE_NAME     := dvf-guest.ext4
INSTALL_TARGET ?= /var/lib/dvf/images

.PHONY: all build validate clean install fmt

all: build

## Download deps and build the guest image
build:
	@echo "=== DVF Guest Image Build ==="
	@echo "  This downloads Alpine Linux and takes 10-15 min..."
	cd $(PACKER_DIR) && $(PACKER) init . && $(PACKER) build \
		-var "output_dir=../$(OUTPUT_DIR)" \
		dvf-guest.pkr.hcl
	@echo ""
	@echo "Image: $(OUTPUT_DIR)/$(IMAGE_NAME)"
	@ls -lh $(OUTPUT_DIR)/$(IMAGE_NAME) 2>/dev/null || true

## Validate the template without building
validate:
	cd $(PACKER_DIR) && $(PACKER) validate -var "output_dir=../$(OUTPUT_DIR)" dvf-guest.pkr.hcl

## Format all HCL files
fmt:
	cd $(PACKER_DIR) && $(PACKER) fmt .

## Remove the built image
clean:
	@rm -rf $(OUTPUT_DIR)
	@echo "Cleaned."

## Copy the image to the vm_image_dir specified in global_config.json
install: $(OUTPUT_DIR)/$(IMAGE_NAME)
	@echo "Installing to $(INSTALL_TARGET)..."
	install -d $(INSTALL_TARGET)
	install -m 644 $(OUTPUT_DIR)/$(IMAGE_NAME) $(INSTALL_TARGET)/$(IMAGE_NAME)
	@echo "Installed: $(INSTALL_TARGET)/$(IMAGE_NAME)"
