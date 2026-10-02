# 0048. A missing default image is said, not swapped

Date: 2026-10-01
Status: accepted

Extends issue #159's image drift check (`greenroom image-status`). Keeps ADR 0018 (one image
recipe, `scripts/build-image.sh`). Fixes issue #285.

## Context

The installed daemon ran with `-image greenroom-lean-a` after that image had been deleted from
the host. `serve` started cleanly and logged `image=greenroom-lean-a`. Every `machine_create`
that named no image failed with tart's own words, `the specified VM "greenroom-lean-a" does not
exist`, and nothing said what to do. `greenroom image-status` printed `not on this host` for
both default names and nothing else, although the host held `greenroom-base-v10-r4`, the VM
suite's image of this daemon's input helper and recipe, and a create that named it was ready in
about 30 s.

Three ways out were open: refuse to serve, swap in another local image, or say it.

- Refusing to serve takes everything down for something a caller can route around: a
  `machine_create` that names an image still works, and so do every run already alive. Under
  launchd a daemon that exits is restarted, so it would restart in a loop.
- Swapping is what the issue offered as an option: pick the newest local image that matches the
  daemon's recipe. The only cheap evidence is the name (`greenroom-base-v<helper>-r<recipe>`),
  which a person can give any image with `build-image.sh -name`. The real evidence is the disk
  (`image-status` mounts it), which takes seconds, needs the image stopped, and is wrong to do on
  every create. The name also says nothing about the profile: `greenroom-lean-a` is lean, the
  suite's images are base. A daemon told `-image greenroom-lean-a` that quietly runs a base image
  verifies on a desktop the operator did not choose, and every report names an image nobody
  configured. The configured name is a decision a person made; the daemon should not overrule it.
- Saying it costs one `tart list` and changes no behaviour that works today.

## Decision

1. The daemon never replaces a configured or chosen default image with another one. The
   default choice itself (`GREENROOM_IMAGE`, then the first local `machine.PreferredImages`, then
   upstream) is unchanged.
2. `serve` checks the default image at start. When it is a local name (no `/`; tart pulls an OCI
   reference on clone) that `tart list` has no local VM for, it logs one warning: that every
   `machine_create` without an image will fail, the local greenroom images it could use, the
   command that builds the missing one, and `greenroom image-status`. It still serves. A tart
   that cannot list is not a missing image.
3. When `machine_create`'s clone fails and the image is a local name tart has no VM for, the
   error says so first (`machine.MissingImageError`): the local greenroom images, the ones named
   for this daemon's helper and recipe first and marked as such ("named for": only the name says
   it), that the caller can call again with one as `image`, and the build command. Tart's own
   error follows it. The check runs only after a clone fails, so a create that works pays nothing,
   and the run is recorded with this error as any failed clone is.
4. A local greenroom image is a local tart VM named `greenroom-*` that is not a run's clone
   (`greenroom-<runId>`), a build in progress (`<name>-building`) or the dialog gate's clones
   (`<image>-check-<tag>`).
5. `image-status` prints the build command for a default name that is absent, and then lists the
   other local greenroom images with what their disks say (current, stale, running, unreadable)
   and how to serve one. It never offers to rebuild them, and `-rebuild-args` still covers only
   the names it was asked about, so `install.sh -rebuild` never rebuilds an image it did not
   choose.

## Consequences

- An operator learns at start, in the daemon's log, that the default image is gone, and from
  `image-status` which image to point `-image` (or `GREENROOM_IMAGE`) at, or what to rebuild.
- An agent whose create fails reads the same in the tool error and can retry with an image that
  exists, without a person.
- The host stays broken for callers that name no image until a person acts. That is the price
  of never running an image nobody chose.
- A future `greenroom doctor` (issue #165) can reuse `machine.CheckImageOnHost` and
  `machine.LocalImages`.
