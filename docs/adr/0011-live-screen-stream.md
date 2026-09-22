# 0011. A live screen: H.264 from inside the guest over one exec pipe

Date: 2026-09-22
Status: accepted

## Context

The companion's Screen tab showed screenshots: the daemon ran `tart exec screencapture`
every 0.5-2 s, got a ~4 MB PNG as base64, and the app downloaded each frame. Clicks
landed through a fresh `tart exec` of the input helper per batch (~80 ms). A person
driving the machine saw their click 1-2 s later.

Measured on this host (macOS 26.6.2 guest, Tart 2.37.0, `--no-graphics`):

| Path | Result |
| --- | --- |
| ScreenCaptureKit in the guest, launched via `tart exec` | 52-56 fps, works headless, no prompt |
| Guest VideoToolbox H.264 | hardware (`paravirtualized:Apple Video Encoder - AVC family`); encode p50 7.6 ms at 1024x768, 19 ms at 2048x1536; ~5 Mbit/s |
| `tart exec -i` stdin/stdout pipe | RTT p50 0.24 ms, ~1 Gbit/s |
| Keypress in, encoded frame out on the host (full pipeline, 2048x1536) | p50 26 ms, p95 32 ms |
| Tart's `--vnc-experimental` (`_VZVNCServer`), keypress to RFB update | p50 11 ms, p95 28 ms; needs graphics mode; a client that omitted one pseudo-encoding crashed the VM |

## Decision

1. The guest input helper gains a long-running mode, `greenroom-input --serve`. It
   captures the main display with ScreenCaptureKit at its pixel size, encodes H.264
   with VideoToolbox (hardware, real time, no frame reordering), and takes input
   batches on stdin. One process per machine carries both directions.
2. The daemon runs it with `tart exec -i` (no network port, no graphics mode, no
   private API) and relays it: video fans out to viewers over HTTP; input from the
   lease holder goes in over the same pipe instead of a new exec per batch.
3. The companion decodes in hardware (`AVSampleBufferDisplayLayer`) and shows the live
   picture while following live. Playback of the past stays on the recorded frames.
4. The lease (ADR 0009) and step recording are unchanged: all input still goes
   through `Manager.Input`.
5. VNC is rejected as the primary path: graphics mode, a private API, and a VM that a
   malformed client can crash. It stays available for a person via `-watch`.

## Wire protocol

Every message in both directions is `[type u8][length u32 big-endian][payload]`.

Guest to daemon (helper stdout):

| Type | Name | Payload |
| --- | --- | --- |
| 0x01 | HELLO | JSON `{"version":"greenroom-input 3","screen":{"width":1024,"height":768},"pixels":{"width":2048,"height":1536}}`; first message, once |
| 0x02 | FORMAT | `AVCDecoderConfigurationRecord` (avcC) bytes; before the first keyframe and whenever the format changes |
| 0x03 | VIDEO | `[flags u8: bit0 = keyframe][pts u64 big-endian, microseconds][AVCC sample: 4-byte-length-prefixed NAL units]` |
| 0x04 | ACK | JSON `{"id":7}` or `{"id":7,"error":"..."}`; one per INPUT, after its events are posted |
| 0x05 | LOG | UTF-8 diagnostic text |

Daemon to guest (helper stdin):

| Type | Name | Payload |
| --- | --- | --- |
| 0x10 | INPUT | JSON `{"id":7,"actions":[...]}`; actions exactly as `--json-base64` takes them (guest points) |
| 0x11 | KEYFRAME | empty; re-encode the latest captured frame as an IDR, preceded by FORMAT |

EOF on stdin ends the helper with exit 0. ScreenCaptureKit only delivers frames when
the screen changes, so the helper keeps the latest pixel buffer to answer KEYFRAME on a
still screen.

Daemon to companion: `GET /api/runs/{id}/screen/live` answers 200 with
`Content-Type: application/x-greenroom-screen` and a body of the same framing, carrying
only HELLO, FORMAT and VIDEO. It writes HELLO immediately (URLSession holds a response
until its first bytes), then the latest FORMAT, then frames from the next keyframe on.
409 when the machine is not ready. Input stays `POST /api/runs/{id}/input`.

## Consequences

- One helper version bump (`inputHelperVersion` 3): first use on an old image compiles
  once, as today; `scripts/build-image.sh` bakes it.
- The stream runs only while someone watches, and stops 30 s after the last viewer
  leaves, so agents that never look cost the guest nothing.
- Input from agents uses the stream when it is running and a one-shot exec otherwise.
- Recording keeps its JPEG frames for now. Writing the H.264 itself as the run's
  recording is a follow-up.
- A slow viewer drops frames up to the next keyframe; it never slows the others.
