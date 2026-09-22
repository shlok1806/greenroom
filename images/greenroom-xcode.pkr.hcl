// greenroom-xcode<N>: the toolchain layer, built on top of greenroom-base.
//
// See docs/09-image-strategy.md. This layer adds Xcode, the simulator runtimes and
// warm caches, and asserts the things that otherwise fail silently and are only
// discovered mid-run in a customer's PR.
//
// NOT YET BUILT. This template has never run. It needs about 140 GB of virtual disk
// and a pre-fetched Xcode .xip, and the dev host had 19 GB free. See "Before you can
// build this" below.
//
// Build:
//   packer init  images/greenroom-xcode.pkr.hcl
//   packer build -var "xcode_version=26.1" \
//                -var "xip_path=$HOME/XcodesCache/Xcode_26.1.xip" \
//                images/greenroom-xcode.pkr.hcl
//
// ## Before you can build this
//
// Apple ID credentials never go in the pipeline. That is not a stylistic choice:
// `xcodes install` can log in and download for you, but that means a real Apple ID
// with a real session token inside a build that we intend to publish as an image
// layer, and layers are content-addressed and immutable. So the .xip is fetched once,
// by hand, and file-provisioned in. This is what Cirrus does too.
//
// The user must download the .xip once:
//   https://developer.apple.com/download/all/?q=Xcode
// and put it at ~/XcodesCache/Xcode_<version>.xip on the BUILD HOST.
//
// Nothing here downloads it, and the build fails immediately if it is missing rather
// than silently producing an image with no Xcode in it.

packer {
  required_plugins {
    tart = {
      version = ">= 1.16.0"
      source  = "github.com/cirruslabs/tart"
    }
  }
}

variable "base_vm" {
  type        = string
  default     = "greenroom-base"
  description = "The greenroom-base VM this layer is built on. Build that first."
}

variable "xcode_version" {
  type        = string
  description = "Xcode version, e.g. 26.1. Names the output image: greenroom-xcode<major>."
}

variable "xip_path" {
  type        = string
  description = "Host path to the pre-fetched Xcode .xip. Never an Apple ID."
}

// Split out so the image name is greenroom-xcode26, not greenroom-xcode26.1. A
// point release of Xcode is not a new image lineage.
variable "xcode_major" {
  type    = string
  default = ""
}

// Xcode plus one simulator runtime does not fit in the base image's 80 GB.
variable "disk_size_gb" {
  type    = number
  default = 140
}

locals {
  major   = var.xcode_major != "" ? var.xcode_major : split(".", var.xcode_version)[0]
  vm_name = "greenroom-xcode${local.major}"
}

source "tart-cli" "xcode" {
  // Starts from our own base VM, so the IPSW is only ever restored once and the
  // TCC rows, the first-boot daemon and the pinned display all carry forward.
  vm_base_name = var.base_vm
  vm_name      = local.vm_name

  cpu_count    = 8
  memory_gb    = 16
  disk_size_gb = var.disk_size_gb

  headless     = true
  ssh_username = "admin"
  ssh_password = "admin"
  ssh_timeout  = "120s"

  // Deleting it breaks softwareupdate inside the guest, permanently.
  recovery_partition = "keep"
}

build {
  sources = ["source.tart-cli.xcode"]

  # 0. Fail before spending an hour if the premise is wrong.
  provisioner "shell" {
    inline = [
      "set -euo pipefail",
      "FREE_MB=$(df -m / | awk 'NR==2 {print $4}')",
      "echo \"free before Xcode: $${FREE_MB} MB\"",
      // Xcode expands to roughly 4x the .xip, and the runtime lands on top.
      "[ \"$${FREE_MB}\" -gt 60000 ] || { echo 'FAIL: need >60 GB free before installing Xcode'; exit 1; }",
    ]
  }

  # 1. The pre-fetched .xip. Provisioning it is the slow step; it is ~11 GB over
  #    the guest's virtio network.
  provisioner "file" {
    source      = var.xip_path
    destination = "/tmp/Xcode.xip"
  }

  # 2. Install with xcodes from the local path. --path means no Apple ID, no
  #    network fetch, and no session token anywhere in this build.
  provisioner "shell" {
    inline = [
      "set -euo pipefail",
      "command -v xcodes >/dev/null || brew install xcodesorg/made/xcodes",
      "xcodes install --path /tmp/Xcode.xip --experimental-unxip --select --empty-trash",
      "rm -f /tmp/Xcode.xip",
      "xcodebuild -version",
      "xcode-select -p",
    ]
  }

  # 3. First launch. This installs the additional components and accepts the
  #    licence. Without it the first xcodebuild of a run pays several minutes and
  #    may block on a licence prompt that nobody is there to answer.
  provisioner "shell" {
    inline = [
      "set -euo pipefail",
      "sudo xcodebuild -license accept",
      "sudo xcodebuild -runFirstLaunch",
      // The Metal toolchain is a separate download in Xcode 26 and is not part of
      // -runFirstLaunch. A shader build fails without it, late and unhelpfully.
      "sudo xcodebuild -downloadComponent MetalToolchain",
    ]
  }

  # 4. Simulator runtimes, then the assertion that they are what we expect.
  provisioner "file" {
    source      = "${path.root}/data/expected-simulator-runtimes.txt"
    destination = "/tmp/expected-simulator-runtimes.txt"
  }
  provisioner "shell" {
    inline = [
      "set -euo pipefail",
      "xcodebuild -downloadAllPlatforms || sudo xcodebuild -downloadAllPlatforms",
      // Diff the installed runtimes against a checked-in list. Apple ships and
      // withdraws runtimes between point releases, so an image can quietly stop
      // having the runtime a customer's test plan names. Failing here is cheap;
      // finding out during a verification run is not.
      "xcrun simctl runtime list -j | /usr/bin/python3 -c \"import json,sys; print('\\n'.join(sorted(r['runtimeIdentifier'] for r in json.load(sys.stdin).values())))\" > /tmp/actual-simulator-runtimes.txt",
      "echo '--- actual ---'; cat /tmp/actual-simulator-runtimes.txt",
      // The expected list ships empty because this template has never been built,
      // and a guessed list is worse than no list: it would fail every build for a
      // reason that is not real. The first successful build prints the actual set
      // and stops, so a person checks it in deliberately. After that the diff is a
      // real assertion.
      "if ! grep -q '[^[:space:]]' /tmp/expected-simulator-runtimes.txt; then",
      "  echo 'FAIL: data/expected-simulator-runtimes.txt is empty (never seeded).'",
      "  echo 'Copy the actual list above into that file, review it, and re-run.'",
      "  exit 1",
      "fi",
      "echo '--- expected ---'; cat /tmp/expected-simulator-runtimes.txt",
      "diff -u /tmp/expected-simulator-runtimes.txt /tmp/actual-simulator-runtimes.txt || { echo 'FAIL: simulator runtimes drifted from data/expected-simulator-runtimes.txt. If the change is intended, update that file.'; exit 1; }",
    ]
  }

  # 5. Warm the caches at build time, not at agent time.
  provisioner "shell" {
    inline = [
      "set -euo pipefail",
      // Otherwise update_dyld_sim_shared_cache runs on first simulator boot and
      // takes minutes of CPU that the agent is waiting on.
      "xcrun simctl runtime dyld_shared_cache update --all",
      "xcrun simctl list runtimes",
    ]
  }

  # 6. Housekeeping and the free-space floor.
  provisioner "shell" {
    inline = [
      "set -euo pipefail",
      "sudo rm -rf /tmp/Xcode.xip /tmp/expected-simulator-runtimes.txt /tmp/actual-simulator-runtimes.txt",
      "rm -rf ~/Library/Caches/com.apple.dt.Xcode ~/Library/Developer/Xcode/DerivedData",
      "sync",
      "FREE_MB=$(df -m / | awk 'NR==2 {print $4}')",
      "echo \"free after: $${FREE_MB} MB\"",
      // A machine that boots with no room to build is worse than one that fails here.
      "[ \"$${FREE_MB}\" -gt 15000 ] || { echo 'FAIL: under 15 GB free'; exit 1; }",
    ]
  }

  # 7. The base image's smoke test still has to pass with Xcode on top.
  provisioner "file" {
    source      = "${path.root}/scripts/smoke-test.sh"
    destination = "/tmp/smoke-test.sh"
  }
  provisioner "shell" {
    inline = [
      "chmod +x /tmp/smoke-test.sh && /tmp/smoke-test.sh",
      "rm -f /tmp/smoke-test.sh",
      // PrepareGuest's lesson: tart stop does not flush the guest's dirty pages by
      // itself, and an unsynced image can come back with files missing. Measured
      // again on 2026-09-21 while testing the first-boot daemon: without this the
      // LaunchDaemon plist and its script were simply gone after a reboot.
      "sync",
    ]
  }
}
