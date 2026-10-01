#!/usr/bin/env python3
"""Measures the live screen (ADR 0011) of one running tart VM, straight from the guest helper.

Throwaway: nothing imports it. Needs python3 with numpy, ffmpeg and tart on PATH.
See README.md for what it measures and how to read the numbers.
"""

import argparse
import base64
import json
import os
import random
import struct
import subprocess
import sys
import threading
import time

import numpy as np

HELLO, FORMAT, VIDEO, ACK, LOG = 0x01, 0x02, 0x03, 0x04, 0x05
INPUT, KEYFRAME = 0x10, 0x11

TEXT = """The quick brown fox jumps over the lazy dog. 0123456789 ilI1| O0o rn m cl d
Pack my box with five dozen liquor jugs; sphinx of black quartz, judge my vow.
""" * 60


def tart(vm, *args, stdin=None, check=True):
    return subprocess.run(["tart", "exec", *(["-i"] if stdin is not None else []), vm, *args],
                          input=stdin, capture_output=True, check=check)


def sh(vm, script, check=True):
    return tart(vm, "/bin/sh", "-c", script, check=check)


class Stream:
    """One `greenroom-input --serve` over tart exec, parsed as it arrives."""

    def __init__(self, vm, helper, env=()):
        prefix = ["/usr/bin/env", *env] if env else []
        self.proc = subprocess.Popen(["tart", "exec", "-i", vm, *prefix, helper, "--serve"],
                                     stdin=subprocess.PIPE, stdout=subprocess.PIPE, bufsize=0)
        self.lock = threading.Lock()
        self.hello = None
        self.codec = None
        self.params = []  # parameter-set NAL units, without start codes
        self.length_size = 4
        self.frames = []  # (arrival, size, keyframe, pts_us, annexb)
        self.acks = {}
        self.logs = []
        self.ready = threading.Event()
        threading.Thread(target=self.read, daemon=True).start()
        if not self.ready.wait(60):
            raise SystemExit("no HELLO from the helper")

    def read_full(self, n):
        buf = b""
        while len(buf) < n:
            chunk = self.proc.stdout.read(n - len(buf))
            if not chunk:
                return None
            buf += chunk
        return buf

    def read(self):
        while True:
            head = self.read_full(5)
            if head is None:
                return
            typ, n = head[0], struct.unpack(">I", head[1:])[0]
            body = self.read_full(n)
            if body is None:
                return
            now = time.monotonic()
            with self.lock:
                if typ == HELLO:
                    self.hello = json.loads(body)
                    self.ready.set()
                elif typ == FORMAT:
                    self.parse_format(body)
                elif typ == VIDEO:
                    key, pts = body[0] == 1, struct.unpack(">Q", body[1:9])[0]
                    self.frames.append((now, len(body) - 9, key, pts, self.annexb(body[9:], key)))
                elif typ == ACK:
                    self.acks[json.loads(body).get("id")] = now
                elif typ == LOG:
                    self.logs.append(body.decode(errors="replace"))

    def parse_format(self, rec):
        """avcC or hvcC: the parameter sets and the NAL length size."""
        if rec[0] == 1 and len(rec) > 6 and rec[1] in (0x42, 0x4D, 0x58, 0x64, 0x6E, 0x7A, 0xF4):  # avcC
            self.codec = "h264"
            self.length_size = (rec[4] & 3) + 1
            sets, i = [], 6
            for count_at in ("sps", "pps"):
                count = rec[i - 1] & 0x1F if count_at == "sps" else rec[i]
                if count_at == "pps":
                    i += 1
                for _ in range(count):
                    n = struct.unpack(">H", rec[i:i + 2])[0]
                    sets.append(rec[i + 2:i + 2 + n])
                    i += 2 + n
            self.params = sets
        else:  # hvcC
            self.codec = "hevc"
            self.length_size = (rec[21] & 3) + 1
            arrays, i, sets = rec[22], 23, []
            for _ in range(arrays):
                i += 1
                count = struct.unpack(">H", rec[i:i + 2])[0]
                i += 2
                for _ in range(count):
                    n = struct.unpack(">H", rec[i:i + 2])[0]
                    sets.append(rec[i + 2:i + 2 + n])
                    i += 2 + n
            self.params = sets

    def annexb(self, data, key):
        out = b"".join(b"\0\0\0\1" + p for p in self.params) if key else b""
        i = 0
        while i + self.length_size <= len(data):
            n = int.from_bytes(data[i:i + self.length_size], "big")
            out += b"\0\0\0\1" + data[i + self.length_size:i + self.length_size + n]
            i += self.length_size + n
        return out

    def send(self, typ, payload=b""):
        self.proc.stdin.write(bytes([typ]) + struct.pack(">I", len(payload)) + payload)
        self.proc.stdin.flush()

    def input(self, ident, actions):
        self.send(INPUT, json.dumps({"id": ident, "actions": actions}).encode())

    def count(self):
        with self.lock:
            return len(self.frames)

    def close(self):
        self.proc.stdin.close()
        try:
            self.proc.wait(5)
        except subprocess.TimeoutExpired:
            self.proc.kill()


def decode_last(stream, upto, path):
    """Decodes frames[0:upto] and writes the last picture as gray8 PNG-free raw to path."""
    with stream.lock:
        frames = stream.frames[:upto]
    # Start at the newest keyframe at or before the end: a decoder needs nothing older.
    start = max(i for i, f in enumerate(frames) if f[2])
    es = b"".join(f[4] for f in frames[start:])
    w, h = stream.hello["pixels"]["width"], stream.hello["pixels"]["height"]
    fmt = "h264" if stream.codec == "h264" else "hevc"
    raw = subprocess.run(["ffmpeg", "-v", "error", "-f", fmt, "-i", "pipe:", "-f", "rawvideo",
                          "-pix_fmt", "gray", "pipe:"], input=es, capture_output=True, check=True).stdout
    n = len(raw) // (w * h)
    img = np.frombuffer(raw[(n - 1) * w * h:n * w * h], np.uint8).reshape(h, w)
    subprocess.run(["ffmpeg", "-v", "error", "-y", "-f", "rawvideo", "-pix_fmt", "gray", "-s", f"{w}x{h}",
                    "-i", "pipe:", path], input=img.tobytes(), check=True)
    return img


def screenshot(vm, path):
    out = sh(vm, "f=/tmp/gr-measure.png; screencapture -x $f && base64 -i $f; rm -f $f").stdout
    png = base64.b64decode(out)
    with open(path, "wb") as f:
        f.write(png)
    probe = json.loads(subprocess.run(["ffprobe", "-v", "error", "-show_streams", "-of", "json", path],
                                      capture_output=True, check=True).stdout)["streams"][0]
    w, h = probe["width"], probe["height"]
    raw = subprocess.run(["ffmpeg", "-v", "error", "-i", path, "-f", "rawvideo", "-pix_fmt", "gray", "pipe:"],
                         capture_output=True, check=True).stdout
    return np.frombuffer(raw, np.uint8).reshape(h, w)


def box(img, r):
    k = 2 * r + 1
    c = np.cumsum(np.cumsum(np.pad(img, ((r + 1, r), (r + 1, r)), mode="edge"), 0), 1)
    return (c[k:, k:] - c[:-k, k:] - c[k:, :-k] + c[:-k, :-k]) / (k * k)


def ssim(a, b):
    """Mean SSIM over 7x7 windows on gray images (Wang et al., uniform window)."""
    a, b = a.astype(np.float64), b.astype(np.float64)
    c1, c2 = (0.01 * 255) ** 2, (0.03 * 255) ** 2
    ma, mb = box(a, 3), box(b, 3)
    va, vb, cov = box(a * a, 3) - ma * ma, box(b * b, 3) - mb * mb, box(a * b, 3) - ma * mb
    s = ((2 * ma * mb + c1) * (2 * cov + c2)) / ((ma * ma + mb * mb + c1) * (va + vb + c2))
    return float(s.mean())


def psnr(a, b):
    mse = float(np.mean((a.astype(np.float64) - b.astype(np.float64)) ** 2))
    return 99.0 if mse == 0 else 10 * np.log10(255 ** 2 / mse)


def compare(name, frame, shot, region, out):
    if frame.shape != shot.shape:
        out[name] = {"error": f"frame {frame.shape} vs screenshot {shot.shape}"}
        return
    x0, y0, x1, y1 = region
    out[name] = {
        "ssimFull": round(ssim(frame, shot), 4),
        "ssimText": round(ssim(frame[y0:y1, x0:x1], shot[y0:y1, x0:x1]), 4),
        "psnrText": round(psnr(frame[y0:y1, x0:x1], shot[y0:y1, x0:x1]), 2),
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-vm", required=True)
    ap.add_argument("-helper", required=True, help="the helper's path in the guest")
    ap.add_argument("-out", required=True)
    ap.add_argument("-latency-reps", type=int, default=30)
    ap.add_argument("-env", action="append", default=[], help="KEY=VALUE for the helper (experiments)")
    args = ap.parse_args()
    os.makedirs(args.out, exist_ok=True)
    vm, res = args.vm, {}

    # A text-heavy scene: TextEdit with small plain text, the window at a known place.
    tart(vm, "/bin/sh", "-c", "cat > /tmp/gr-measure.txt", stdin=TEXT.encode())
    sh(vm, "osascript -e 'tell application \"TextEdit\" to quit' >/dev/null 2>&1; sleep 1; open -a TextEdit /tmp/gr-measure.txt", check=False)
    time.sleep(4)
    sh(vm, "osascript -e 'tell application \"TextEdit\" to set bounds of front window to {40, 60, 940, 700}'", check=False)
    time.sleep(1)

    s = Stream(vm, args.helper, args.env)
    res["hello"] = s.hello
    pts_w, px_w = s.hello["screen"]["width"], s.hello["pixels"]["width"]
    scale = px_w / pts_w
    region = tuple(int(v * scale) for v in (60, 120, 900, 680))  # inside the TextEdit window, in pixels
    park = {"type": "move", "x": pts_w - 5, "y": s.hello["screen"]["height"] - 5}
    s.input(1, [park])
    time.sleep(2)

    # A: a still screen, the keyframe a new viewer gets.
    n0 = s.count()
    s.send(KEYFRAME)
    deadline = time.monotonic() + 5
    while s.count() == n0 and time.monotonic() < deadline:
        time.sleep(0.05)
    time.sleep(0.3)
    shot = screenshot(vm, f"{args.out}/still-shot.png")
    frame = decode_last(s, s.count(), f"{args.out}/still-frame.png")
    compare("stillKeyframe", frame, shot, region, res)

    # B: what a viewer is left with after scrolling stops (no keyframe asked for).
    for i in range(12):
        s.input(10 + i, [{"type": "scroll", "x": 400, "y": 400, "deltaY": -120 if i < 6 else 120}])
        time.sleep(0.12)
    s.input(30, [park])
    time.sleep(2.5)
    shot = screenshot(vm, f"{args.out}/settled-shot.png")
    frame = decode_last(s, s.count(), f"{args.out}/settled-frame.png")
    compare("afterScroll", frame, shot, region, res)

    # C: bitrate and frame rate while text scrolls continuously.
    n0, t0 = s.count(), time.monotonic()
    cpu = []

    def sample_cpu():
        time.sleep(2)
        for _ in range(3):
            out = sh(vm, "ps -A -o %cpu=,command= | grep -e '--serve' | grep -v grep", check=False).stdout.decode()
            cpu.extend(float(line.split()[0]) for line in out.splitlines() if line.strip())
            time.sleep(0.8)

    sampler = threading.Thread(target=sample_cpu)
    sampler.start()
    i = 0
    while time.monotonic() - t0 < 6:
        s.input(100 + i, [{"type": "scroll", "x": 400, "y": 400, "deltaY": -60 if (i // 10) % 2 == 0 else 60}])
        i += 1
        time.sleep(0.05)
    t1 = time.monotonic()
    sampler.join()
    time.sleep(0.5)
    with s.lock:
        window = [f for f in s.frames[n0:] if t0 <= f[0] <= t1]
    secs = t1 - t0
    res["motion"] = {
        "seconds": round(secs, 2),
        "fps": round(len(window) / secs, 1),
        "mbps": round(sum(f[1] for f in window) * 8 / secs / 1e6, 2),
        "meanFrameKB": round(sum(f[1] for f in window) / max(1, len(window)) / 1024, 1),
        "helperCpuPercent": round(sum(cpu) / len(cpu), 1) if cpu else None,
    }
    time.sleep(2)

    # D: input to frame out: a pointer move posted on the stream until the frame showing it
    # arrives on the host (the daemon relays these bytes unchanged).
    lat = []
    for i in range(args.latency_reps):
        n0 = s.count()
        sent = time.monotonic()
        s.input(1000 + i, [{"type": "move", "x": random.randint(100, pts_w - 100), "y": random.randint(100, 600)}])
        deadline = sent + 3
        while s.count() == n0 and time.monotonic() < deadline:
            time.sleep(0.001)
        with s.lock:
            if len(s.frames) > n0:
                lat.append((s.frames[n0][0] - sent) * 1000)
        time.sleep(0.4)
    lat.sort()
    res["inputToFrameMs"] = {"n": len(lat), "median": round(lat[len(lat) // 2], 1),
                             "p90": round(lat[int(len(lat) * 0.9)], 1)} if lat else None

    with s.lock:
        keys = [f[1] for f in s.frames if f[2]]
    res["keyframeKB"] = round(sum(keys) / max(1, len(keys)) / 1024, 1)
    res["codec"] = s.codec
    res["helperLog"] = s.logs
    s.close()
    with open(f"{args.out}/result.json", "w") as f:
        json.dump(res, f, indent=2)
    json.dump(res, sys.stdout, indent=2)
    print()


if __name__ == "__main__":
    main()
