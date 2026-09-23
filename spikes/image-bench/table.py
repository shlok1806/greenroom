#!/usr/bin/env python3
"""Print a Markdown comparison table from bench.py's raw JSON files.

    spikes/image-bench/table.py docs/image-experiment/raw/greenroom-base.json docs/image-experiment/raw/greenroom-lean-a.json

Each cell is the median over the image's completed runs, with every run's value after
it. The last column compares the last file against the first.
"""

import json
import sys


def med(xs):
    xs = sorted(x for x in xs if isinstance(x, (int, float)) and not isinstance(x, bool))
    if not xs:
        return None
    n = len(xs)
    return xs[n // 2] if n % 2 else (xs[n // 2 - 1] + xs[n // 2]) / 2


def fmt(x, digits=1):
    if x is None:
        return "-"
    if isinstance(x, bool):
        return "yes" if x else "no"
    if isinstance(x, float):
        return f"{x:.{digits}f}"
    return str(x)


ROWS = [
    # label, summary key, digits, lower is better
    ("Image size, allocated (GB)", None, 1, True),
    ("Boot to ready (s)", "bootToReadySeconds", 1, True),
    ("Idle CPU busy, 10 min mean (%)", "idleCpuBusyPct", 1, True),
    ("Idle CPU busy, last 5 min (%)", "idleCpuBusySettledPct", 1, True),
    ("Idle memory used, last 5 min (MB)", "idleMemUsedMB", 0, True),
    ("Idle processes", "idleProcs", 0, True),
    ("launchd jobs, user domain", "idleUserJobs", 0, True),
    ("launchd jobs running, user domain", "idleUserRunning", 0, True),
    ("launchd jobs, system domain", "idleSystemJobs", 0, True),
    ("Task wall time (s)", "taskWallSeconds", 1, True),
    ("Task verifier steps", "taskSteps", 0, True),
    ("Task misclicks", "taskMisclicks", 0, True),
    ("First machine_ui (s)", "firstUiSeconds", 2, True),
    ("machine_screenshot median (s)", "screenshotMedianSeconds", 3, True),
    ("machine_ui median (s)", "uiMedianSeconds", 3, True),
    ("Popups seen", "popups", 0, True),
    ("Desktop widgets seen", "widgets", 0, True),
    ("Reboot: agent back (s)", "rebootAgentBackSeconds", 1, True),
]


def main(paths):
    data = [json.load(open(p)) for p in paths]
    names = [d["image"]["name"] for d in data]
    head = "| Metric | " + " | ".join(names) + (" | Change |" if len(data) > 1 else " |")
    print(head)
    print("|" + " --- |" * (len(names) + 1 + (len(data) > 1)))
    for label, key, digits, _ in ROWS:
        cells, meds = [], []
        for d in data:
            if key is None:
                v = d["image"].get("diskImgAllocatedGB", d["image"].get("sizeGB"))
                cells.append(fmt(v, digits))
                meds.append(v)
                continue
            vals = d["summary"].get(key) or []
            m = med(vals)
            meds.append(m)
            runs = ", ".join(fmt(v, digits) for v in vals)
            cells.append(f"**{fmt(m, digits)}** ({runs})" if len(vals) > 1 else fmt(m, digits))
        change = ""
        if len(data) > 1:
            a, b = meds[0], meds[-1]
            if isinstance(a, (int, float)) and isinstance(b, (int, float)) and a:
                change = f"{(b - a) / a * 100:+.0f}%"
            else:
                change = "-"
        print(f"| {label} | " + " | ".join(cells) + (f" | {change} |" if len(data) > 1 else " |"))
    for label, key in (("Verdict (correct is fail)", "taskVerdict"), ("Survives reboot, swiftc works", "rebootOK")):
        cells = [", ".join(fmt(v) for v in (d["summary"].get(key) or [])) for d in data]
        print(f"| {label} | " + " | ".join(cells) + (" | |" if len(data) > 1 else " |"))


if __name__ == "__main__":
    main(sys.argv[1:])
