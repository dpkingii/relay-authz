"""Prints a markdown table of run-all.sh results: per rate, max staleness and mode, the median
of the runs, with the p99 range across runs so the spread is visible.

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
    # Runs from before the setting existed had none.
    runs[(r["rate"], r.get("max_staleness", "0s"), r["mode"])].append(r)


def seconds(d):
    return float(d.removesuffix("ms")) / 1000 if d.endswith("ms") else float(d.removesuffix("s"))


print("| target checks/s | max staleness | mode | achieved checks/s | cache hits | p50 | p95 | p99 (range) | dropped |")
print("|---|---|---|---|---|---|---|---|---|")
for rate, staleness, mode in sorted(runs, key=lambda k: (k[0], seconds(k[1]), MODES.index(k[2]))):
    rs = runs[(rate, staleness, mode)]
    med = lambda key: median(r[key] for r in rs)
    p99s = [r["p99"] for r in rs]
    print(
        f"| {rate:,} | {staleness} | {mode} | {med('checks') / duration:,.0f} | {med('hit_rate'):.0%} "
        f"| {med('p50'):.1f} ms | {med('p95'):.1f} ms "
        f"| {med('p99'):.1f} ms ({min(p99s):.1f}–{max(p99s):.1f}) | {med('dropped'):,.0f} |"
    )
