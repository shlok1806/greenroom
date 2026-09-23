#!/usr/bin/env python3
"""Benchmark a greenroom VM image the way an agent uses it.

For one image this builds the daemon from this checkout, serves it on its own port and
root, and runs N fresh machines one after another over MCP (HTTP JSON-RPC on /mcp). Per
machine it records boot-to-ready, idle guest load, a verifier task on TipSplit,
screenshot and machine_ui latency, and whether the machine survives `sudo reboot` with
a working swiftc. Raw JSON goes to --out. Throwaway: nothing imports this.

    spikes/image-bench/bench.py -image greenroom-lean-a -out docs/image-experiment/raw/greenroom-lean-a.json

It never touches a VM it did not create, and keeps at most one of its own alive. See
README.md next to it for what each number means.
"""

import argparse
import datetime as dt
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
DAEMON = os.path.join(REPO, "apps", "daemon")
PINNED_TART = os.path.expanduser("~/.local/tart-2.37.0/tart.app/Contents/MacOS/tart")
TIPSPLIT = "/Users/shlokthakkar/.claude/jobs/711b26c4/tmp/demo/TipSplit/main.swift"
BUGGY_LINE = "var perPerson: Double { tip / Double(people) }"

# The demo task, word for word (issue #35's demo run). The right verdict is fail: the
# app divides only the tip, so Each pays shows $8.00, not $48.00.
TASK = (
    "TipSplit, a tip calculator I just built, is running on screen. Verify it through the UI only "
    "(do not read the source, rebuild or relaunch): set the bill to 120, choose the 20% tip, set People to 3. "
    "Each pays should be each person's share of bill plus tip, (120 + 24) / 3 = $48.00. Then click 25% and "
    "check Each pays becomes $50.00. Propose a pass or fail verdict with screenshot evidence."
)

# On-screen windows that are the desktop, not a popup. Anything else seen during idle or
# the task is reported as a popup (a banner, an alert, a setup or sign-in window).
EXPECTED_OWNERS = {
    "Window Server", "Dock", "Control Center", "SystemUIServer", "Spotlight", "Finder",
    "TextInputMenuAgent", "Wallpaper", "WindowManager", "TipSplit", "Terminal",
}

# Lists every on-screen window with its owner, name and layer. JXA, so nothing is compiled.
WINDOWS_JXA = r"""
ObjC.import('CoreGraphics');
var ws = ObjC.deepUnwrap(ObjC.castRefToObject($.CGWindowListCopyWindowInfo(1 | 16, 0))) || [];
JSON.stringify(ws.filter(function (w) { return (w.kCGWindowAlpha || 0) > 0; }).map(function (w) {
  var b = w.kCGWindowBounds || {};
  return {owner: w.kCGWindowOwnerName || '', name: w.kCGWindowName || '', layer: w.kCGWindowLayer,
          w: Math.round(b.Width || 0), h: Math.round(b.Height || 0)};
}));
"""

# One idle sample: whole-guest CPU (second top sample, the first is since boot), memory,
# processes, and launchd jobs in the user's and the system domain.
SAMPLE_SH = r"""
top -l 2 -n 0 -s 1 | awk '/^CPU usage/ {cpu=$0} /^PhysMem/ {mem=$0} END {print cpu; print mem}'
echo "procs $(ps -ax -o pid= | wc -l)"
echo "userjobs $(launchctl list | tail -n +2 | wc -l)"
echo "userrunning $(launchctl list | tail -n +2 | awk '$1 != "-"' | wc -l)"
echo "systemjobs $(sudo -n launchctl list | tail -n +2 | wc -l)"
echo "load $(sysctl -n vm.loadavg)"
"""


def now():
    return time.monotonic()


def iso():
    return dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds")


def log(*a):
    print(f"[{dt.datetime.now().strftime('%H:%M:%S')}]", *a, flush=True)


class MCP:
    """Streamable HTTP MCP client: initialize, notifications/initialized, tools/call."""

    def __init__(self, url):
        self.url = url
        self.session = None
        self.version = None
        self.next_id = 1

    def _post(self, body, timeout=120):
        headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
        if self.session:
            headers["Mcp-Session-Id"] = self.session
        if self.version:
            headers["MCP-Protocol-Version"] = self.version
        req = urllib.request.Request(self.url, data=json.dumps(body).encode(), headers=headers, method="POST")
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            if resp.headers.get("Mcp-Session-Id"):
                self.session = resp.headers["Mcp-Session-Id"]
            raw = resp.read().decode()
            if "text/event-stream" in (resp.headers.get("Content-Type") or ""):
                msgs = [json.loads(line[5:]) for line in raw.splitlines() if line.startswith("data:") and line[5:].strip()]
                for m in msgs:
                    if m.get("id") == body.get("id"):
                        return m
                return msgs[-1] if msgs else None
            return json.loads(raw) if raw.strip() else None

    def initialize(self):
        r = self._post({"jsonrpc": "2.0", "id": self._id(), "method": "initialize", "params": {
            "protocolVersion": "2025-06-18", "capabilities": {},
            "clientInfo": {"name": "greenroom-image-bench", "version": "1"}}})
        self.version = r["result"]["protocolVersion"]
        self._post({"jsonrpc": "2.0", "method": "notifications/initialized"})

    def _id(self):
        self.next_id += 1
        return self.next_id

    def call(self, tool, args=None, timeout=180):
        """Returns (structured output, error text or None, seconds)."""
        t = now()
        r = self._post({"jsonrpc": "2.0", "id": self._id(), "method": "tools/call",
                        "params": {"name": tool, "arguments": args or {}}}, timeout=timeout)
        secs = now() - t
        if r is None:
            return None, "no response", secs
        if "error" in r:
            return None, r["error"].get("message", str(r["error"])), secs
        res = r["result"]
        text = " ".join(c.get("text", "") for c in res.get("content", []) if c.get("type") == "text")
        if res.get("isError"):
            return res.get("structuredContent"), text or "tool error", secs
        return res.get("structuredContent"), None, secs


class Tart:
    def __init__(self, bin_):
        self.bin = bin_

    def run(self, *args, timeout=60, stdin=None):
        return subprocess.run([self.bin, *args], capture_output=True, text=True, timeout=timeout, input=stdin)

    def vms(self):
        out = self.run("list", "--format", "json").stdout
        return json.loads(out) if out.strip() else []

    def running(self):
        return [v["Name"] for v in self.vms() if v.get("State") == "running"]

    def exec_sh(self, vm, script, timeout=60):
        """Runs a script in the guest over tart exec (no daemon step, no activity)."""
        return self.run("exec", "-i", vm, "/bin/sh", "-s", timeout=timeout, stdin=script)


def parse_sample(text):
    s = {}
    m = re.search(r"CPU usage: ([\d.]+)% user, ([\d.]+)% sys, ([\d.]+)% idle", text)
    if m:
        s["cpuUser"], s["cpuSys"], s["cpuIdle"] = map(float, m.groups())
        s["cpuBusy"] = round(s["cpuUser"] + s["cpuSys"], 2)
    m = re.search(r"PhysMem: (\d+)([MG]) used .*?(\d+)([MG]) unused", text)
    if m:
        def mb(n, u):
            return int(n) * (1024 if u == "G" else 1)
        s["memUsedMB"] = mb(m.group(1), m.group(2))
        s["memUnusedMB"] = mb(m.group(3), m.group(4))
    m = re.search(r"\((\d+)M wired", text)
    if m:
        s["memWiredMB"] = int(m.group(1))
    for key in ("procs", "userjobs", "userrunning", "systemjobs"):
        m = re.search(rf"^{key} +(\d+)", text, re.M)
        if m:
            s[key] = int(m.group(1))
    m = re.search(r"^load \{ ([\d.]+) ([\d.]+) ([\d.]+) \}", text, re.M)
    if m:
        s["load1"] = float(m.group(1))
    return s


def mean(xs):
    xs = [x for x in xs if x is not None]
    return round(sum(xs) / len(xs), 2) if xs else None


def median(xs):
    xs = sorted(x for x in xs if x is not None)
    if not xs:
        return None
    n = len(xs)
    return round(xs[n // 2] if n % 2 else (xs[n // 2 - 1] + xs[n // 2]) / 2, 3)


class WindowWatcher(threading.Thread):
    """Polls the guest's on-screen windows over tart exec and keeps every unexpected one."""

    def __init__(self, tart, vm, interval=10):
        super().__init__(daemon=True)
        self.tart, self.vm, self.interval = tart, vm, interval
        self.phase = "boot"
        self.seen = {}  # (phase, owner, name) -> first seen iso
        self.polls = 0
        self.errors = 0
        self.owners = set()  # every owner of an on-screen window, expected or not
        self.stop_ev = threading.Event()
        self.paused = threading.Event()

    def run(self):
        while not self.stop_ev.is_set():
            if not self.paused.is_set():
                self.poll()
            self.stop_ev.wait(self.interval)

    def poll(self):
        try:
            r = self.tart.run("exec", self.vm, "osascript", "-l", "JavaScript", "-e", WINDOWS_JXA, timeout=30)
            wins = json.loads(r.stdout) if r.returncode == 0 and r.stdout.strip() else None
        except Exception:
            wins = None
        if wins is None:
            self.errors += 1
            return
        self.polls += 1
        for w in wins:
            self.owners.add(w["owner"])
            if w["owner"] in EXPECTED_OWNERS:
                continue
            if w["w"] < 2 or w["h"] < 2:
                continue
            key = (self.phase, w["owner"], w["name"])
            if key not in self.seen:
                # Desktop widgets are Notification Center windows below the desktop icons
                # (a hugely negative layer); a banner from it sits above the windows.
                kind = "widget" if w["owner"] == "Notification Center" and w["layer"] < 0 else "popup"
                self.seen[key] = {"kind": kind, "at": iso(), "layer": w["layer"], "size": f"{w['w']}x{w['h']}"}
                log(f"  {kind} seen during {self.phase}: {w['owner']} {w['name']!r} layer {w['layer']} {w['w']}x{w['h']}")

    def report(self, phase=None):
        return [{"phase": p, "owner": o, "name": n, **v} for (p, o, n), v in self.seen.items() if phase in (None, p)]


def read_jsonl(path):
    if not os.path.exists(path):
        return []
    with open(path) as f:
        return [json.loads(line) for line in f if line.strip()]


def is_container(el):
    return el.get("role") in ("Window", "ScrollArea", "Group", "SplitGroup", "Application", "Sheet")


def analyse_task(run_dir, sent_at, verdict_at):
    """Counts the verifier's steps and misclicks between the task and its verdict.

    A misclick is a verifier click whose point lies in no non-container element of the
    verifier's latest machine_ui tree (or, with no tree, any coordinate click).
    """
    steps = read_jsonl(os.path.join(run_dir, "steps.jsonl"))
    tree, apps_seen = None, set()
    out = {"steps": 0, "clicks": 0, "misclicks": 0, "uiReads": 0, "screenshots": 0, "tools": {}, "misclickDetail": []}
    for s in steps:
        at = dt.datetime.fromisoformat(s["at"].replace("Z", "+00:00"))
        if at < sent_at or (verdict_at and at > verdict_at):
            continue
        inp = s.get("input") or {}
        if inp.get("holder") not in (None, "verifier") or inp.get("reader") not in (None, "verifier"):
            continue
        out["steps"] += 1
        out["tools"][s["tool"]] = out["tools"].get(s["tool"], 0) + 1
        if s["tool"] == "machine_ui":
            out["uiReads"] += 1
            o = s.get("output") or {}
            if o.get("elements"):
                tree = o
            apps_seen.update(o.get("apps") or [])
        elif s["tool"] == "machine_screenshot":
            out["screenshots"] += 1
        elif s["tool"] == "machine_input":
            for a in inp.get("actions") or []:
                if a.get("type") not in ("click", "doubleclick", "rightclick"):
                    continue
                out["clicks"] += 1
                x, y = a.get("x"), a.get("y")
                hit = False
                for el in (tree or {}).get("elements", []):
                    if is_container(el):
                        continue
                    if abs(x - el["x"]) <= el["w"] / 2 + 0.002 and abs(y - el["y"]) <= el["h"] / 2 + 0.002:
                        hit = True
                        break
                if not hit:
                    out["misclicks"] += 1
                    out["misclickDetail"].append({"step": s["seq"], "x": x, "y": y})
    out["appsSeenByVerifier"] = sorted(apps_seen)
    return out


POPUP_WORDS = re.compile(r"\b(notification|banner|alert|dialog|pop-?up|sign in|apple account|software update|"
                         r"what's new|welcome to|setup assistant|siri|icloud)\b", re.I)


class Bench:
    def __init__(self, a):
        self.a = a
        self.tart = Tart(a.tart)
        self.scratch = a.scratch
        self.root = os.path.join(self.scratch, "root")
        self.proc = None
        self.mcp = None
        self.mine = set()

    # ---- daemon ----------------------------------------------------------------------
    def build(self):
        os.makedirs(self.scratch, exist_ok=True)
        grx = os.path.join(self.scratch, "grx")
        log("building the daemon from", DAEMON)
        subprocess.run(["go", "build", "-o", grx, "."], cwd=DAEMON, check=True)
        return grx

    def serve(self, grx):
        os.makedirs(self.root, exist_ok=True)
        self.daemon_log = open(os.path.join(self.scratch, "daemon.log"), "a")
        args = [grx, "serve", "-addr", self.a.addr, "-root", self.root, "-env-file", self.a.env_file,
                "-open-viewer=false", "-verifier", "nim", "-image", self.a.image, "-tart", self.a.tart]
        log("serving:", " ".join(args))
        self.proc = subprocess.Popen(args, stdout=self.daemon_log, stderr=subprocess.STDOUT)
        for _ in range(60):
            try:
                urllib.request.urlopen(f"http://{self.a.addr}/healthz", timeout=2)
                break
            except Exception:
                time.sleep(0.5)
        else:
            raise SystemExit("the daemon never answered /healthz; see " + self.daemon_log.name)
        self.mcp = MCP(f"http://{self.a.addr}/mcp")
        self.mcp.initialize()

    def stop(self):
        if self.proc and self.proc.poll() is None:
            self.proc.send_signal(signal.SIGINT)
            try:
                self.proc.wait(20)
            except subprocess.TimeoutExpired:
                self.proc.kill()

    # ---- one machine -----------------------------------------------------------------
    def wait_for_slot(self):
        waited = 0
        while True:
            running = self.tart.running()
            mine = [v for v in running if v in self.mine]
            if mine:
                raise SystemExit(f"one of this bench's machines is still running: {mine}")
            if len(running) < 2:
                return waited
            if waited % 60 == 0:
                log(f"host is at its VM limit ({running}); waiting")
            time.sleep(15)
            waited += 15

    def create(self):
        while True:
            self.wait_for_slot()
            t0 = now()
            out, err, _ = self.mcp.call("machine_create", {"image": self.a.image})
            if err and ("limit" in err.lower() or "exceeds" in err.lower()):
                log("create refused by the host limit, retrying:", err)
                time.sleep(30)
                continue
            if err:
                raise RuntimeError("machine_create: " + err)
            run_id = out["runId"]
            self.mine.add("greenroom-" + run_id)
            while True:
                w, err, _ = self.mcp.call("machine_wait", {"runId": run_id, "timeoutSeconds": 50}, timeout=90)
                if err:
                    raise RuntimeError("machine_wait: " + err)
                if w["status"] == "ready":
                    return run_id, round(now() - t0, 2), w
                if w["status"] == "failed":
                    msg = w.get("error", "")
                    if "limit" in msg.lower() or "exceeds" in msg.lower():
                        log("boot refused by the host limit, retrying:", msg)
                        self.destroy(run_id)
                        time.sleep(30)
                        break
                    raise RuntimeError("boot failed: " + msg)

    def destroy(self, run_id):
        self.mcp.call("machine_destroy", {"runId": run_id}, timeout=180)
        vm = "greenroom-" + run_id
        for _ in range(60):
            if vm not in [v["Name"] for v in self.tart.vms()]:
                break
            time.sleep(2)
        self.mine.discard(vm)

    def exec(self, run_id, command, cwd=None, timeout=300):
        args = {"runId": run_id, "command": command, "timeoutSeconds": timeout}
        if cwd:
            args["cwd"] = cwd
        return self.mcp.call("machine_exec", args, timeout=timeout + 60)

    def idle(self, vm, seconds):
        samples = []
        t0 = now()
        while now() - t0 < seconds:
            tick = now()
            try:
                r = self.tart.exec_sh(vm, SAMPLE_SH, timeout=60)
                s = parse_sample(r.stdout)
            except subprocess.TimeoutExpired:
                s = {}
            s["t"] = round(tick - t0, 1)
            samples.append(s)
            time.sleep(max(0, self.a.sample_every - (now() - tick)))
        half = [s for s in samples if s["t"] >= seconds / 2]
        summary = {}
        for key in ("cpuBusy", "memUsedMB", "memWiredMB", "procs", "userjobs", "userrunning", "systemjobs", "load1"):
            summary[key + "Mean"] = mean([s.get(key) for s in samples])
            summary[key + "SettledMean"] = mean([s.get(key) for s in half])
        return {"seconds": seconds, "sampleEvery": self.a.sample_every, "summary": summary, "samples": samples}

    def prepare_tipsplit(self):
        d = os.path.join(self.scratch, "TipSplit")
        os.makedirs(d, exist_ok=True)
        src = open(self.a.tipsplit).read()
        src = re.sub(r"var perPerson: Double \{[^}\n]*\}", BUGGY_LINE, src, count=1)
        if BUGGY_LINE not in src:
            raise SystemExit("could not set the perPerson line in " + self.a.tipsplit)
        with open(os.path.join(d, "main.swift"), "w") as f:
            f.write(src)
        return d

    def latency(self, run_id):
        shots, uis = [], []
        for _ in range(self.a.latency_reps):
            _, err, secs = self.mcp.call("machine_screenshot", {"runId": run_id})
            shots.append(None if err else round(secs, 3))
            _, err, secs = self.mcp.call("machine_ui", {"runId": run_id, "app": "TipSplit"})
            uis.append(None if err else round(secs, 3))
        return {"screenshotSeconds": shots, "uiSeconds": uis,
                "screenshotMedian": median(shots), "uiMedian": median(uis)}

    def task(self, run_id, watcher):
        res = {}
        src = self.prepare_tipsplit()
        out, err, secs = self.mcp.call("machine_sync", {"runId": run_id, "source": src, "dest": "TipSplit"})
        if err:
            raise RuntimeError("machine_sync: " + err)
        out, err, secs = self.exec(run_id, "swiftc -parse-as-library -O main.swift -o TipSplit", cwd="~/TipSplit")
        res["buildSeconds"] = round(secs, 2)
        res["buildOK"] = not err and out and out.get("exitCode") == 0
        if not res["buildOK"]:
            res["buildError"] = err or (out or {}).get("stderr")
            return res
        self.exec(run_id, "./TipSplit >/tmp/tipsplit.log 2>&1 &", cwd="~/TipSplit", timeout=30)
        # The first read is also the warm-up: an image without the current input helper
        # compiles it here (~28 s), so it never lands inside the verifier's timing.
        t0 = now()
        first = None
        while now() - t0 < 120:
            out, err, secs = self.mcp.call("machine_ui", {"runId": run_id, "app": "TipSplit"})
            if first is None:
                first = round(secs, 2)
            if not err and any("Each pays" in json.dumps(e) for e in (out or {}).get("elements", [])):
                break
            time.sleep(2)
        else:
            res["launchError"] = "TipSplit's window never showed Each pays"
            return res
        res["firstUiSeconds"] = first
        res["appUpSeconds"] = round(now() - t0, 2)
        res["latency"] = self.latency(run_id)

        watcher.phase = "task"
        # A model outage (timeouts, 404, 429 from NIM) is not the image's fault. When the
        # verifier gives up on a turn, wait and send the task again, and time the task
        # from the send that got a verdict. Outages are recorded, never hidden.
        outages, sends = [], 0
        verdict, questions, texts = None, 0, []
        while not verdict and sends <= self.a.outage_retries:
            sends += 1
            sent, err, _ = self.mcp.call("agent_send", {"runId": run_id, "kind": "task", "text": TASK})
            if err:
                raise RuntimeError("agent_send: " + err)
            sent_at = dt.datetime.fromisoformat(sent["at"].replace("Z", "+00:00"))
            t0 = now()
            after, gave_up, reasons = sent["seq"], False, []
            questions, texts = 0, []
            while now() - t0 < self.a.task_timeout and not verdict and not gave_up:
                w, err, _ = self.mcp.call("agent_wait", {"runId": run_id, "after": after, "timeoutSeconds": 50},
                                          timeout=90)
                if err:
                    raise RuntimeError("agent_wait: " + err)
                after = w["last"]
                for m in w["messages"]:
                    text = m.get("text", "")
                    if m["from"] == "system" and "verifier turn failed" in text:
                        reasons.append(text[:200])
                    if m["from"] == "system" and "verifier gave up" in text:
                        gave_up = True
                    if m["from"] != "verifier":
                        continue
                    texts.append(text)
                    if m["kind"] == "verdict":
                        verdict = m
                    elif m["kind"] == "question":
                        questions += 1
                        self.mcp.call("agent_send", {"runId": run_id, "kind": "answer", "replyTo": m["seq"],
                                                     "text": "Use your judgement from the UI alone; do not read the source."})
            if gave_up or reasons:
                outages.append({"send": sends, "gaveUp": gave_up, "seconds": round(now() - t0, 1), "errors": reasons})
            if gave_up and not verdict:
                log(f"  verifier model unavailable ({reasons[-1] if reasons else 'gave up'}); "
                    f"resending in {self.a.outage_wait} s")
                time.sleep(self.a.outage_wait)
            elif not verdict:
                break
        res["sends"] = sends
        res["modelOutages"] = outages
        res["wallSeconds"] = round(now() - t0, 1)
        res["questions"] = questions
        if not verdict:
            res["verdict"] = None
            res["timedOut"] = True
        else:
            res["verdict"] = verdict.get("verdict")
            res["verdictText"] = verdict.get("text", "")[:600]
            res["correct"] = verdict.get("verdict") == "fail"
            self.mcp.call("agent_send", {"runId": run_id, "kind": "accept", "replyTo": verdict["seq"]})
        verdict_at = dt.datetime.fromisoformat(verdict["at"].replace("Z", "+00:00")) if verdict else None
        run_dir = os.path.join(self.root, "runs", run_id)
        res.update(analyse_task(run_dir, sent_at, verdict_at))
        # Context, not just the word: "no dialog is open" is not a popup. Read these by hand.
        res["popupWordsInVerifierText"] = sorted({t[max(0, m.start() - 60):m.end() + 60].replace("\n", " ")
                                                  for t in texts for m in POPUP_WORDS.finditer(t)})
        watcher.phase = "after-task"
        return res

    def reboot(self, run_id, vm):
        res = {}
        before = self.tart.run("exec", vm, "sysctl", "-n", "kern.boottime").stdout
        t0 = now()
        # The exec never returns cleanly: the guest goes down under it.
        self.exec(run_id, "sudo -n /sbin/reboot", timeout=10)
        res["execReturnedSeconds"] = round(now() - t0, 1)
        back = None
        while now() - t0 < 300:
            try:
                r = self.tart.run("exec", vm, "sysctl", "-n", "kern.boottime", timeout=15)
                if r.returncode == 0 and r.stdout.strip() and r.stdout != before:
                    back = now() - t0
                    break
            except subprocess.TimeoutExpired:
                pass
            time.sleep(2)
        res["rebooted"] = back is not None
        res["agentBackSeconds"] = round(back, 1) if back else None
        if back is None:
            return res
        # Ready again means the same as at boot: exec, swiftc, a screenshot and a UI read.
        t1 = now()
        ok = None
        while now() - t1 < 180:
            out, err, _ = self.exec(run_id, "swiftc --version >/dev/null && printf 'print(\"ok\")' > /tmp/h.swift "
                                    "&& swiftc /tmp/h.swift -o /tmp/h && /tmp/h", timeout=180)
            if not err and out and out.get("exitCode") == 0 and "ok" in out.get("stdout", ""):
                ok = True
                break
            time.sleep(3)
        res["swiftcOK"] = bool(ok)
        res["swiftcSeconds"] = round(now() - t1, 1)
        _, err, secs = self.mcp.call("machine_screenshot", {"runId": run_id})
        res["screenshotOK"], res["screenshotSeconds"] = err is None, round(secs, 2)
        if err:
            res["screenshotError"] = err
        _, err, secs = self.mcp.call("machine_ui", {"runId": run_id})
        res["uiOK"], res["uiSeconds"] = err is None, round(secs, 2)
        if err:
            res["uiError"] = err
        return res

    def one(self, i):
        log(f"run {i}: creating a machine from {self.a.image}")
        run_id, boot, ready = self.create()
        vm = "greenroom-" + run_id
        log(f"run {i}: {run_id} ready in {boot} s")
        rec = {"run": i, "runId": run_id, "createdAt": iso(), "bootToReadySeconds": boot}
        steps = read_jsonl(os.path.join(self.root, "runs", run_id, "steps.jsonl"))
        rec["bootStep"] = next((s.get("output") for s in steps if s["tool"] == "machine_boot"), None)
        watcher = WindowWatcher(self.tart, vm)
        watcher.start()
        try:
            watcher.phase = "idle"
            log(f"run {i}: idle for {self.a.idle_seconds} s")
            rec["idle"] = self.idle(vm, self.a.idle_seconds)
            shot, err, _ = self.mcp.call("machine_screenshot", {"runId": run_id})
            if not err and shot:
                dest = os.path.join(self.scratch, f"run{i}-after-idle.png")
                shutil.copyfile(shot["path"], dest)
                rec["idleScreenshot"] = dest
            log(f"run {i}: idle cpu {rec['idle']['summary']['cpuBusyMean']}% "
                f"procs {rec['idle']['summary']['procsMean']}")
            log(f"run {i}: TipSplit task")
            rec["task"] = self.task(run_id, watcher)
            t = rec["task"]
            log(f"run {i}: verdict {t.get('verdict')} in {t.get('wallSeconds')} s, {t.get('steps')} steps, "
                f"{t.get('misclicks')} misclicks")
            watcher.paused.set()
            log(f"run {i}: reboot")
            rec["reboot"] = self.reboot(run_id, vm)
            log(f"run {i}: reboot {rec['reboot']}")
        finally:
            watcher.stop_ev.set()
            rec["popups"] = watcher.report()
            rec["windowPolls"] = {"ok": watcher.polls, "failed": watcher.errors, "owners": sorted(watcher.owners)}
            log(f"run {i}: destroying {run_id}")
            self.destroy(run_id)
        return rec

    def image_facts(self):
        for v in self.tart.vms():
            if v["Name"] == self.a.image:
                facts = {"name": v["Name"], "sizeGB": v.get("Size"), "diskGB": v.get("Disk"), "source": v.get("Source")}
                # Allocated bytes of the disk image, finer than tart's whole GB. An APFS
                # clone shares its blocks, so this is the image's own size, not new disk used.
                img = os.path.expanduser(f"~/.tart/vms/{v['Name']}/disk.img")
                if os.path.exists(img):
                    facts["diskImgAllocatedGB"] = round(os.stat(img).st_blocks * 512 / 1e9, 2)
                return facts
        raise SystemExit(f"no image named {self.a.image}")

    def main(self):
        started = iso()
        facts = self.image_facts()
        grx = self.build()
        self.serve(grx)
        result = {"image": facts, "started": started, "host": os.uname().nodename,
                  "daemonCommit": subprocess.run(["git", "rev-parse", "HEAD"], cwd=REPO, capture_output=True,
                                                 text=True).stdout.strip(),
                  "settings": {"runs": self.a.runs, "idleSeconds": self.a.idle_seconds,
                               "sampleEvery": self.a.sample_every, "latencyReps": self.a.latency_reps},
                  "runs": []}
        try:
            for i in range(1, self.a.runs + 1):
                try:
                    result["runs"].append(self.one(i))
                except Exception as e:  # a failed run is data too
                    log(f"run {i} failed: {e!r}")
                    result["runs"].append({"run": i, "error": repr(e)})
                self.write(result)
        finally:
            self.stop()
            for vm in list(self.mine):
                log("cleaning up", vm)
                self.tart.run("stop", vm, timeout=60)
                self.tart.run("delete", vm, timeout=60)
        result["finished"] = iso()
        result["summary"] = summarise(result)
        self.write(result)
        log("wrote", self.a.out)
        print(json.dumps(result["summary"], indent=2))

    def write(self, result):
        os.makedirs(os.path.dirname(os.path.abspath(self.a.out)), exist_ok=True)
        tmp = self.a.out + ".tmp"
        with open(tmp, "w") as f:
            json.dump(result, f, indent=1, default=str)
        os.replace(tmp, self.a.out)


def summarise(result):
    runs = [r for r in result["runs"] if "error" not in r]

    def col(f):
        vals = []
        for r in runs:
            try:
                vals.append(f(r))
            except (KeyError, TypeError):
                vals.append(None)
        return vals

    s = {"runs": len(result["runs"]), "completed": len(runs),
         "bootToReadySeconds": col(lambda r: r["bootToReadySeconds"]),
         "idleCpuBusyPct": col(lambda r: r["idle"]["summary"]["cpuBusyMean"]),
         "idleCpuBusySettledPct": col(lambda r: r["idle"]["summary"]["cpuBusySettledMean"]),
         "idleMemUsedMB": col(lambda r: r["idle"]["summary"]["memUsedMBSettledMean"]),
         "idleProcs": col(lambda r: r["idle"]["summary"]["procsSettledMean"]),
         "idleUserJobs": col(lambda r: r["idle"]["summary"]["userjobsSettledMean"]),
         "idleUserRunning": col(lambda r: r["idle"]["summary"]["userrunningSettledMean"]),
         "idleSystemJobs": col(lambda r: r["idle"]["summary"]["systemjobsSettledMean"]),
         "taskWallSeconds": col(lambda r: r["task"]["wallSeconds"]),
         "taskSteps": col(lambda r: r["task"]["steps"]),
         "taskMisclicks": col(lambda r: r["task"]["misclicks"]),
         "taskVerdict": col(lambda r: r["task"]["verdict"]),
         "taskCorrect": col(lambda r: r["task"]["correct"]),
         "firstUiSeconds": col(lambda r: r["task"]["firstUiSeconds"]),
         "screenshotMedianSeconds": col(lambda r: r["task"]["latency"]["screenshotMedian"]),
         "uiMedianSeconds": col(lambda r: r["task"]["latency"]["uiMedian"]),
         "popups": col(lambda r: len([p for p in r["popups"] if p["kind"] == "popup"])),
         "widgets": col(lambda r: len({p["name"] for p in r["popups"] if p["kind"] == "widget"})),
         "rebootOK": col(lambda r: r["reboot"]["rebooted"] and r["reboot"]["swiftcOK"] and r["reboot"]["screenshotOK"]),
         "rebootAgentBackSeconds": col(lambda r: r["reboot"]["agentBackSeconds"]),
         "imageSizeGB": result["image"].get("sizeGB")}
    for k in list(s):
        if isinstance(s[k], list) and s[k] and all(isinstance(x, (int, float)) and not isinstance(x, bool)
                                                    for x in s[k] if x is not None):
            s[k + "Median"] = median(s[k])
    return s


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("-image", required=True)
    p.add_argument("-out", required=True, help="raw JSON path")
    p.add_argument("-runs", type=int, default=3)
    p.add_argument("-idle-seconds", dest="idle_seconds", type=int, default=600)
    p.add_argument("-sample-every", dest="sample_every", type=int, default=30)
    p.add_argument("-latency-reps", dest="latency_reps", type=int, default=5)
    p.add_argument("-task-timeout", dest="task_timeout", type=int, default=900)
    p.add_argument("-outage-retries", dest="outage_retries", type=int, default=3,
                   help="resend the task this many times after the verifier gives up on a model outage")
    p.add_argument("-outage-wait", dest="outage_wait", type=int, default=120)
    p.add_argument("-addr", default="127.0.0.1:7861")
    p.add_argument("-scratch", default=None, help="daemon binary and root; default /tmp/greenroom-bench-<image>")
    p.add_argument("-env-file", dest="env_file", default="/Users/shlokthakkar/projects/greenroom/.env")
    p.add_argument("-tipsplit", default=TIPSPLIT)
    p.add_argument("-tart", default=os.environ.get("GREENROOM_TART") or
                   (PINNED_TART if os.path.exists(PINNED_TART) else shutil.which("tart") or "tart"))
    a = p.parse_args()
    a.scratch = a.scratch or f"/tmp/greenroom-bench-{a.image}"
    os.environ.setdefault("TART_NO_AUTO_PRUNE", "1")
    Bench(a).main()


if __name__ == "__main__":
    sys.exit(main())
