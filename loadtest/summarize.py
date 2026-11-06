"""Prints a markdown table of run-all.sh results: per rate and mode, the median of the runs,
with the p99 range across runs so the spread is visible.

    python3 loadtest/summarize.py loadtest/results/m4pro.jsonl 60   # 60 = DURATION in seconds
"""

import json
import sys
from collections import defaultdict
from statistics import median

MODES = ["uncached", "cached", "cached-at-least", "cached-writes"]

path, duration = sys.argv[1], int(sys.argv[2])
runs = defaultdict(list)
for line in open(path):
    r = json.loads(line)
    runs[(r["rate"], r["mode"])].append(r)

print("| target checks/s | mode | achieved checks/s | cache hits | p50 | p95 | p99 (range) | dropped |")
print("|---|---|---|---|---|---|---|---|")
for rate in sorted({rate for rate, _ in runs}):
    for mode in MODES:
        rs = runs[(rate, mode)]
        med = lambda key: median(r[key] for r in rs)
        p99s = [r["p99"] for r in rs]
        print(
            f"| {rate:,} | {mode} | {med('checks') / duration:,.0f} | {med('hit_rate'):.0%} "
            f"| {med('p50'):.1f} ms | {med('p95'):.1f} ms "
            f"| {med('p99'):.1f} ms ({min(p99s):.1f}–{max(p99s):.1f}) | {med('dropped'):,.0f} |"
        )
