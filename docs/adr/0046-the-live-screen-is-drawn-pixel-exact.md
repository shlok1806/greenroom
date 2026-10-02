# 0046. The live screen is drawn pixel-exact

Date: 2026-10-01
Status: accepted. Amends ADR 0011 decision 3 (the Companion's decoder and display layer).
Part of issue #249.

## Context

Small text on the Companion's live screen looked soft and blurry (issue #249). Measured on
this host (MacBook Pro M3 Pro in clamshell mode, a 2560x1440 BenQ at scale 1 as the main
display, Tart 2.32.1 for the probes and the daemon's pinned 2.37.0 for the end-to-end run,
macOS 26.6.2 guest), with `spikes/live-stream/measure.py` reading
`greenroom-input --serve` straight from the guest and comparing against a lossless
`screencapture` of the same still moment (SSIM and PSNR on a TextEdit window of 11 pt text):

| Guest display | Text SSIM, still keyframe | Text SSIM, after a scroll | Motion | Keyframe | Input to frame out |
| --- | --- | --- | --- | --- | --- |
| 1024x768 at 1x (this host) | 0.9996 (47.3 dB) | 0.9996 (47.3 dB) | 1.5 Mbit/s, 22 fps | 156 KB | p50 26 ms, p90 46 ms |
| 2048x1536 pixels | 0.998 to 0.9996 | 0.997 to 0.9996 | 3.4 Mbit/s, 23 fps | 313 KB | p50 30 ms, p90 34 ms |

At 2048x1536, raising `AverageBitRate` from 8 to 32 Mbit/s changed text SSIM by less than
0.0001. The helper used 1% of a guest core (the encoder is the paravirtualized hardware one).
The stream is effectively lossless for text. The blur is made after it:

1. **The guest's pixel density follows the host's main display.** The image's display is
   `1024x768` in Tart's default unit, points. Tart builds a macOS VM's display with
   `VZMacGraphicsDisplayConfiguration(for: NSScreen.main, sizeInPoints:)`, so the guest is
   2048x1536 pixels at 2x when the host's main display is Retina (ADR 0011 measured that) and
   1024x768 at 1x when it is not (this host now). In pixel units Tart hardcodes 72 pixels per
   inch, and macOS then offers no HiDPI mode at all: probed at 1024x768 and 2048x1536, with
   and without a display override plist and `DisplayResolutionEnabled`.
2. **The Companion resamples the picture by whatever factor the stage gives.**
   `AVSampleBufferDisplayLayer` was stretched over `ScreenGeometry.fitted`, with a filter we
   do not choose, at a fractional origin. Measured with the app running in a 2560x1440 1x
   guest display (the shape of this host's monitor) watching a 1024x768 1x machine: the
   frame was drawn 605 pixels wide in the default window (0.59x, every other text line
   visibly thinner from point sampling) and 1482 wide in a full-screen one (1.45x, a soft
   bilinear upscale). Text rendered at 1x does not survive a non-integer resample.

## Decision

1. **The guest display keeps Tart's point units.** It is the only way to get a 2x guest with
   the pinned Tart, and where the host's main display is Retina the guest already is 2x. A
   2x guest on every host needs a pixel-density option Tart does not have; that is a
   follow-up upstream, not a workaround here (a private `CGVirtualDisplay` in the guest was
   considered and rejected: a private API, a process that must outlive every capture, and a
   second display under every screenshot, frame and render check). No image recipe change.
2. **The encoder stays as it is** (H.264 High, low-latency rate control, 8 Mbit/s, 60 fps):
   the numbers above leave nothing for a higher bitrate or HEVC to win, and the Companion's
   decoder and every viewer keep one codec. No helper version bump.
3. **The Companion decodes and draws the live screen itself.** VideoToolbox decodes each
   frame to a BGRA pixel buffer (hardware, as before); Metal draws it into a `CAMetalLayer`
   sized to the picture in device pixels, in the stream's colour space. A frame is copied
   when the drawable is its size, repeated by whole pixels (nearest) when the drawable is a
   whole multiple of it, and scaled with Lanczos (Metal Performance Shaders) otherwise. The
   layer sits on whole device pixels. `AVSampleBufferDisplayLayer` is gone from the app.
4. **The picture is never resampled up by a fraction.** Its largest size is one picture
   pixel per device pixel, or the guest's natural size (one guest point per host point)
   when that is larger, which is then a whole multiple (a 1x guest on a Retina display is
   pixel-doubled). Below that it fills the stage, scaled with Lanczos. So a 1x guest on a 1x
   display shows 1:1 even in a maximized window, smaller than the stage, and sharp.
5. **One size for a run's live and recorded pictures.** The stage takes the run's screen
   geometry from the live stream's HELLO, or from a recorded picture's own size until a
   HELLO arrives, and sizes the picture by decision 4 whether it is live or recorded, so
   following live and scrubbing never change its size. Recorded pictures use a high-quality
   filter when scaled and none at a whole multiple.
6. **Clicks are unchanged.** Input is fractions of the drawn picture (ADR 0009); the
   picture view is the placed rectangle, so `ScreenGeometry` maps the same rectangle the
   pixels land on. The daemon turns fractions into guest points, which do not depend on the
   guest's pixel density. Tests pin both at 1x and 2x.
7. **The daemon logs the stream's density** when a live screen starts (`screen`, `pixels`,
   `scale`), naming the host's main display when the guest is 1x, so a soft picture can be
   explained from the log.

## Consequences

- Measured after, in the same setup: the full-screen window draws the frame at exactly
  1024x768 pixels on whole pixels (its text region against the machine's own lossless
  screenshot: SSIM 0.9983, mean difference 1.7 of 255, the residue being the stream and
  colour matching), and the default window draws it at 0.59x with Lanczos, every line of
  text the same weight.

- On a 1x host a maximized window shows a 1x guest at 1024x768 instead of stretching it;
  the remaining blur on such a host is the guest's own 1x rendering, which only a 2x guest
  fixes (decision 1's follow-up).
- The verifier is unaffected: its screenshots are already capped at 1024 pixels wide
  (`maxVisionWidth`), whatever the guest's density.
- The live layer can be read back (`still()`, the glyph moments) from the decoded buffer
  without a display, so its tests no longer need an awake screen.
- A decoder failure still ends the connection, and the reconnect asks for a keyframe.
