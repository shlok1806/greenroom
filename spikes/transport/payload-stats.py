#!/usr/bin/env python3
"""Describe a payload tree: file count, bytes, and how much of it is small files.

The small-file share is the number that predicts which transport mechanisms struggle,
because per-file overhead dominates once most files are smaller than a few blocks.
"""
import os
import sys

root = sys.argv[1] if len(sys.argv) > 1 else "."
files = dirs = links = total = small = small_bytes = 0

for dirpath, dirnames, filenames in os.walk(root):
    dirs += len(dirnames)
    for name in filenames:
        path = os.path.join(dirpath, name)
        try:
            st = os.lstat(path)
        except OSError:
            continue
        if os.path.islink(path):
            links += 1
            continue
        files += 1
        total += st.st_size
        if st.st_size < 16384:
            small += 1
            small_bytes += st.st_size

if not files:
    sys.exit(f"no files under {root}")

print(f"files        {files}")
print(f"directories  {dirs}")
print(f"symlinks     {links}")
print(f"total        {total} bytes ({total / 2**20:.0f} MiB)")
print(f"under 16 KiB {small} ({100 * small / files:.1f}%), {small_bytes / 2**20:.1f} MiB between them")
print(f"mean size    {total / files / 1024:.1f} KiB")
