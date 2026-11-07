# Benchmarks and load test

Everything here ran on an Apple M4 Pro (12 cores, 24 GB) with Postgres 17 and Redis 7 in Docker Desktop on the same machine.

## Check benchmarks

`go test ./internal/cache -run '^$' -bench . -benchtime 2000x`, median of 3 runs. Each document check asks for `viewer`.

| graph | queries per uncached check | uncached | cached, latest | cached, `at_least` |
|---|---|---|---|---|
| direct tuple | 1 | 0.34 ms | 0.31 ms | 0.15 ms |
| 5 nested groups | 6 | 1.09 ms | 0.29 ms | 0.14 ms |
| 5 nested parent folders | 29 | 4.61 ms | 0.31 ms | 0.11 ms |
| 5 nested parent folders, denied | 30 | 4.83 ms | 0.31 ms | 0.15 ms |

Uncached and "cached, latest" checks also read the current revision, which isn't counted in the query column. The cached numbers are all hits, since nothing writes during the benchmark. The real hit rate depends on the write rate and on how often the same check repeats, which the [load test](#load-test) measures.

## Lookup benchmark

`go test ./internal/check -run '^$' -bench Lookup -benchtime 5x` runs both approaches on the load test's dataset in Postgres, looking up every document a user can view. Median of 3 runs, with the range in parentheses:

| user | documents found | backward walk | check every document |
|---|---|---|---|
| in a group nested in a root folder's viewers | 400 | 3.5 ms (3.0–5.4), 7 queries | 11.9 s (10.0–24.6), 45,760 queries |
| in a group nested in a child folder's editors | 84 | 1.5 ms (1.4–2.9), 6 queries | 16.1 s (10.4–17.9), 45,996 queries |

The query counts are exact; the times are noisy, especially for checking every document, which has to walk every path to all 2,000 documents and most of them are denies.

## Load test

`loadtest/check.js` seeds 4,705 tuples, then sends checks at a constant rate with [k6](https://k6.io):

- **Data:** 500 users in 50 groups, half of them nested in the other half. 5 root folders, 25 child folders and 125 leaf folders, with groups as root viewers and child editors. 2,000 documents spread evenly across the leaf folders, each with an owner. Most allowed checks walk document → leaf → child → root → group → nested group → user.
- **Traffic:** a random document and user per check, skewed so the top 1% of each gets about 22% of picks, as popular objects do. 80% ask for `viewer` and 20% for `editor`.
- **Modes:** `loadtest/run-all.sh` runs each rate in four modes, with Redis flushed before every cached run:
  - `uncached`: the server without Redis, the baseline
  - `cached`: default checks, which with the default `MAX_STALENESS=0s` need the latest revision
  - `cached-at-least`: checks that accept any result from the seed revision on
  - `cached-writes`: default checks with 1% of requests writing a tuple

  `MODES` picks a subset and `MAX_STALENESS` sets the server's snapshot staleness. Each run appends a JSON line to `loadtest/results/<LABEL>.jsonl`.

- **Setup:** the server, Postgres, Redis and k6 all run in Docker Compose, on one machine. k6 runs inside Docker's network because on a Mac, Docker Desktop's port forwarding becomes the bottleneck before the server does: with k6 or the server on the host, checks slowed to tens or hundreds of milliseconds at rates the all-Docker setup handled easily.

```
docker compose up -d
LABEL=m4pro RATES="500 1000 2000 3000" RUNS=3 DURATION=60s loadtest/run-all.sh
python3 loadtest/summarize.py loadtest/results/m4pro.jsonl 60
```

### Results

Docker Desktop's VM given all 12 cores and 8 GB, Go 1.27, k6 1.4. Each row is the median of 3 runs of 60 seconds; the p99 range is across those runs. No run had a failed request.

| target checks/s | mode | achieved checks/s | cache hits | p50 | p95 | p99 (range) | dropped |
|---|---|---|---|---|---|---|---|
| 500 | uncached | 500 | 0% | 1.5 ms | 1.8 ms | 2.1 ms (2.0–7.3) | 0 |
| 500 | cached | 500 | 17% | 1.6 ms | 1.9 ms | 2.4 ms (2.2–4.4) | 0 |
| 500 | cached-at-least | 500 | 17% | 1.6 ms | 1.9 ms | 2.2 ms (2.1–2.2) | 0 |
| 500 | cached-writes | 495 | 1% | 1.6 ms | 1.9 ms | 2.2 ms (2.1–2.3) | 0 |
| 1,000 | uncached | 999 | 0% | 1.5 ms | 2.0 ms | 6.6 ms (2.9–16.4) | 44 |
| 1,000 | cached | 1,000 | 23% | 1.5 ms | 2.0 ms | 2.3 ms (2.3–3.0) | 0 |
| 1,000 | cached-at-least | 1,000 | 23% | 1.5 ms | 1.9 ms | 2.6 ms (2.3–2.7) | 0 |
| 1,000 | cached-writes | 990 | 1% | 1.6 ms | 2.1 ms | 4.6 ms (2.5–21.3) | 6 |
| 2,000 | uncached | 2,000 | 0% | 1.6 ms | 2.2 ms | 3.0 ms (3.0–3.5) | 0 |
| 2,000 | cached | 2,000 | 30% | 1.5 ms | 2.2 ms | 2.8 ms (2.8–2.9) | 0 |
| 2,000 | cached-at-least | 2,000 | 30% | 1.5 ms | 2.2 ms | 3.0 ms (2.7–3.3) | 4 |
| 2,000 | cached-writes | 1,980 | 1% | 1.7 ms | 2.4 ms | 3.6 ms (3.1–3.9) | 32 |
| 3,000 | uncached | 2,998 | 0% | 2.1 ms | 3.3 ms | 5.2 ms (4.0–11.4) | 145 |
| 3,000 | cached | 2,996 | 34% | 1.6 ms | 3.3 ms | 7.7 ms (3.4–8.5) | 216 |
| 3,000 | cached-at-least | 2,996 | 34% | 1.6 ms | 3.1 ms | 5.7 ms (3.2–5.8) | 234 |
| 3,000 | cached-writes | 2,969 | 1% | 2.3 ms | 3.5 ms | 4.5 ms (4.0–6.4) | 0 |
| 4,000 | uncached | 3,997 | 0% | 2.6 ms | 3.9 ms | 5.4 ms (5.1–12.6) | 209 |
| 4,000 | cached | 3,997 | 37% | 1.6 ms | 3.2 ms | 4.2 ms (4.2–4.5) | 207 |
| 4,000 | cached-at-least | 3,999 | 37% | 1.6 ms | 3.1 ms | 4.0 ms (4.0–4.2) | 48 |
| 4,000 | cached-writes | 3,959 | 1% | 2.8 ms | 4.0 ms | 5.2 ms (5.0–5.9) | 117 |

**Up to 4,000 checks/s the median p99 stays under 8 ms in every mode**, though single runs reach 21 ms, and under 0.1% of requests are dropped (k6 drops a request when it can't start it on time). The cache barely changes latency at these rates: an uncached check is already about 2 ms at the median.

To find the ceiling, one run per mode at 6,000 and 8,000 checks/s:

| target checks/s | mode | achieved checks/s | cache hits | p50 | p95 | p99 | dropped |
|---|---|---|---|---|---|---|---|
| 6,000 | uncached | 5,991 | 0% | 3.0 ms | 5.5 ms | 13.7 ms | 570 |
| 6,000 | cached | 5,997 | 42% | 1.8 ms | 3.9 ms | 5.5 ms | 212 |
| 6,000 | cached-at-least | 5,973 | 42% | 1.9 ms | 5.0 ms | 35.2 ms | 1,596 |
| 6,000 | cached-writes | 5,927 | 1% | 3.2 ms | 5.1 ms | 11.0 ms | 727 |
| 8,000 | uncached | 7,868 | 0% | 4.7 ms | 66.0 ms | 95.3 ms | 7,944 |
| 8,000 | cached | 7,973 | 46% | 1.8 ms | 4.7 ms | 12.6 ms | 1,644 |
| 8,000 | cached-at-least | 7,928 | 46% | 1.9 ms | 6.8 ms | 52.3 ms | 4,306 |
| 8,000 | cached-writes | 7,739 | 1% | 9.5 ms | 80.3 ms | 101.1 ms | 11,124 |

**Uncached, the server saturates between 6,000 and 8,000 checks/s**: at 8,000 its p99 jumps to 95 ms and it drops 1.7% of requests. With the cache it holds a 12.6 ms p99 at 8,000, since 46% of checks never reach Postgres. These are single runs, so the `cached-at-least` p99s, worse than `cached` despite the same hit rate, are more likely noise than a real difference.

What the numbers say about the design:

- **The hit rate is the whole story for the cache.** It rises with load (17% at 500 checks/s, 46% at 8,000) because more repeats land within an entry's lifetime, and it only pays off once Postgres is the bottleneck.
- **1% writes take the hit rate to 1%.** Every write bumps the one global revision, so a default check almost never finds a fresh-enough entry. `at_least` avoids it, but only for callers that can accept older answers. The snapshot setting below was added to address this.
- **A 10-minute stretch of the first sweep collapsed** (p99s of 1 to 7 seconds at 2,000 checks/s, then fine again at 3,000), most likely from other activity on the laptop. Those runs were discarded and the 2,000 and 4,000 rows rerun. On a shared laptop, treat these numbers as a rough guide, not a spec.

### Snapshots under writes

The `cached-writes` mode again at 4,000 checks/s, for four values of `MAX_STALENESS`, 3 runs each with the values interleaved:

```
for i in 1 2 3; do for s in 0s 100ms 1s 5s; do
  LABEL=m4pro-staleness MAX_STALENESS=$s MODES=cached-writes RATES=4000 RUNS=1 loadtest/run-all.sh
done; done
```

| max staleness | cache hits | p50 | p95 | p99 (range) | dropped |
|---|---|---|---|---|---|
| 0s | 1% | 3.0 ms | 4.9 ms | 13.1 ms (5.3–44.4) | 453 |
| 100ms | 1% | 2.6 ms | 4.3 ms | 9.3 ms (5.0–19.0) | 707 |
| 1s | 6% | 2.6 ms | 4.4 ms | 13.8 ms (5.2–60.1) | 569 |
| 5s | 14% | 2.4 ms | 3.8 ms | 5.4 ms (5.2–75.5) | 362 |

- **The snapshot works, but the hit rate stays low.** With writes, the hit rate goes from 1% to 6% at 1 second and 14% at 5 seconds. Without writes the same traffic hit 37%, because entries lived for the whole run. What limits the cache now is how often the exact same check repeats within the window: there are 2 million possible document checks and 4,000 a second. Caching shared subproblems, such as one folder's viewers, would help far more, since hundreds of document checks go through the same folder.
- **The median gets a little faster** (3.0 to 2.4 ms) from hits and from skipping the revision query. The p99s are within the noise: the third round had a bad stretch on the laptop, which is where the 60 and 75 ms runs and most of the drops came from.
