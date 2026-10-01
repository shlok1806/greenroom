# Live stream quality

`measure.py` measures one running VM's live screen (ADR 0011) straight from the guest
helper, the bytes the daemon relays unchanged, for issue #249 and root ADR 0045.
Throwaway: nothing imports it. Needs `tart`, `ffmpeg` and python3 with numpy.

```sh
# a clone you made yourself, booted with tart run --no-graphics --no-audio --no-clipboard
spikes/live-stream/measure.py -vm <vm> -helper /Users/admin/.greenroom/bin/greenroom-input-<N> -out /tmp/live
```

It opens TextEdit on a page of small text, runs `greenroom-input --serve` over
`tart exec -i`, and records:

| Key | What |
| --- | --- |
| `hello` | the helper's HELLO: the display in points and pixels |
| `stillKeyframe` | SSIM and PSNR of a forced keyframe against a lossless `screencapture` of the same still moment, whole screen and the text region |
| `afterScroll` | the same for what a viewer is left with once scrolling stops (no keyframe asked) |
| `motion` | bitrate, frame rate, mean frame size and the helper's CPU while text scrolls for 6 s |
| `inputToFrameMs` | a pointer move posted on the stream until the frame showing it reaches the host |
| `keyframeKB` | mean keyframe size |

`-env KEY=VALUE` passes an environment variable to the helper, for an experimental build
that reads one. The decoded frames and screenshots are written to `-out` beside
`result.json`. The guest's density follows the host's main display (ADR 0045); to measure
another, `tart set <vm> --display 2048x1536px` gives that many pixels at 1x.
