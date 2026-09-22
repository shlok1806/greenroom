// greenroom-base: our layer on top of Cirrus's SIP-disabled macOS base.
//
// See docs/09-image-strategy.md. This layer adds, in order:
//   1. greenroom's SSH key, so the daemon never needs a password
//   2. TCC rows for greenroom's own guest binary. The base image already covers
//      sshd-keygen-wrapper, osascript and tart-guest-agent.
//   3. the screen-recording reminder fix, which TCC does not cover
//   4. the first-boot LaunchDaemon that reads a per-VM seed disk
//   5. a smoke test that fails the build if screenshots or events do not work
//
// Build:
//   packer init  images/greenroom-base.pkr.hcl
//   packer build -var "ssh_pubkey=$(cat ~/.greenroom/id_ed25519.pub)" \
//                images/greenroom-base.pkr.hcl

packer {
  required_plugins {
    tart = {
      version = ">= 1.16.0"
      source  = "github.com/cirruslabs/tart"
    }
  }
}

variable "base_image" {
  type    = string
  default = "ghcr.io/cirruslabs/macos-tahoe-base:latest"
}

variable "vm_name" {
  type    = string
  default = "greenroom-base"
}

variable "ssh_pubkey" {
  type        = string
  description = "greenroom daemon's public key, authorized for the admin user"
}

// Pinned in the image so screenshots match across runs and pixel coordinates mean
// the same thing every time. The "pt" suffix controls Retina.
variable "display" {
  type    = string
  default = "1512x982"
}

source "tart-cli" "greenroom" {
  vm_base_name = var.base_image
  vm_name      = var.vm_name

  cpu_count    = 4
  memory_gb    = 8
  disk_size_gb = 80
  display      = var.display

  // No boot_command needed. The base image already has sshd and auto-login. Only
  // the vanilla layer has to type through Setup Assistant.
  headless     = true
  ssh_username = "admin"
  ssh_password = "admin"
  ssh_timeout  = "120s"

  // Deleting it breaks softwareupdate inside the guest, permanently.
  recovery_partition = "keep"
}

build {
  sources = ["source.tart-cli.greenroom"]

  # 1. greenroom's SSH key.
  provisioner "shell" {
    inline = [
      "mkdir -p ~/.ssh && chmod 700 ~/.ssh",
      "echo '${var.ssh_pubkey}' >> ~/.ssh/authorized_keys",
      "chmod 600 ~/.ssh/authorized_keys",
    ]
  }

  # 2 + 3. TCC rows for our own binaries, and the screen-recording reminder fix.
  provisioner "file" {
    source      = "${path.root}/scripts/greenroom-tcc.sh"
    destination = "/tmp/greenroom-tcc.sh"
  }
  provisioner "shell" {
    inline = ["chmod +x /tmp/greenroom-tcc.sh && /tmp/greenroom-tcc.sh"]
  }

  # 4. First-boot daemon. Installed with root inside the guest, which is the point.
  #    launchd refuses a plist that is not root:wheel and says nothing useful, and a
  #    host-side offline write cannot set that ownership without host sudo.
  provisioner "file" {
    source      = "${path.root}/scripts/firstboot.sh"
    destination = "/tmp/firstboot.sh"
  }
  provisioner "file" {
    source      = "${path.root}/data/com.greenroom.firstboot.plist"
    destination = "/tmp/com.greenroom.firstboot.plist"
  }
  provisioner "shell" {
    inline = [
      "sudo mkdir -p /usr/local/greenroom",
      "sudo install -o root -g wheel -m 755 /tmp/firstboot.sh /usr/local/greenroom/firstboot.sh",
      "sudo install -o root -g wheel -m 644 /tmp/com.greenroom.firstboot.plist /Library/LaunchDaemons/com.greenroom.firstboot.plist",
      "sudo xattr -d com.apple.quarantine /Library/LaunchDaemons/com.greenroom.firstboot.plist 2>/dev/null || true",
      "plutil -lint /Library/LaunchDaemons/com.greenroom.firstboot.plist",
      // Do not bootstrap it now. It has to fire on the next boot, in a VM that has
      // a seed disk attached.
    ]
  }

  # 5. Smoke test. TCC never returns an error, it returns black frames and
  #    AXIsProcessTrusted false. Without this a broken image ships.
  provisioner "file" {
    source      = "${path.root}/scripts/smoke-test.sh"
    destination = "/tmp/smoke-test.sh"
  }
  provisioner "shell" {
    inline = ["chmod +x /tmp/smoke-test.sh && /tmp/smoke-test.sh"]
  }

  # Housekeeping.
  provisioner "shell" {
    inline = [
      "sudo rm -f /tmp/greenroom-tcc.sh /tmp/firstboot.sh /tmp/smoke-test.sh /tmp/com.greenroom.firstboot.plist",
      // Free space floor. Cheap to check, expensive to miss.
      "FREE_MB=$(df -m / | awk 'NR==2 {print $4}'); echo \"free: $${FREE_MB} MB\"; [ \"$${FREE_MB}\" -gt 15000 ] || { echo 'FAIL: under 15 GB free'; exit 1; }",
    ]
  }
}
