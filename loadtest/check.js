// Load test for GET /v1/check. Seeds a fixed dataset, then sends checks at a constant rate.
//
//   docker compose --profile loadtest run --rm k6 run -e RATE=500 /scripts/check.js
//
// Options (-e NAME=value):
//   RATE         checks per second (default 200)
//   DURATION     how long to send them (default 60s)
//   WRITE_RATIO  fraction of requests that write or delete a tuple instead (default 0)
//   AT_LEAST     1 to pass the seed revision as at_least, accepting older cached results (default 0)
import http from "k6/http";
import { check } from "k6";
import { Rate } from "k6/metrics";

const BASE = __ENV.BASE_URL || "http://localhost:8080";
const RATE = Number(__ENV.RATE || 200);
const WRITE_RATIO = Number(__ENV.WRITE_RATIO || 0);
const AT_LEAST = __ENV.AT_LEAST === "1";

const USERS = 500;
const GROUPS = 50;
const DOCS = 2000;

const cacheHits = new Rate("cache_hits");

export const options = {
  setupTimeout: "10m",
  scenarios: {
    checks: {
      executor: "constant-arrival-rate",
      rate: RATE,
      timeUnit: "1s",
      duration: __ENV.DURATION || "60s",
      preAllocatedVUs: 50,
      maxVUs: 500,
    },
  },
  summaryTrendStats: ["avg", "p(50)", "p(95)", "p(99)", "max"],
  // Thresholds that always pass, so the summary lists these sub-metrics on their own.
  thresholds: {
    "http_req_duration{name:check}": ["p(99)>=0"],
    "http_req_duration{name:write}": ["p(99)>=0"],
  },
};

// mulberry32, so every run seeds the same dataset.
function rng(seed) {
  return () => {
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// Folders form a tree of 5 roots, 25 children and 125 leaves, and every document sits in a leaf.
// Root viewers and child editors are groups, so most allowed checks walk
// document → leaf → child → root → group → nested group → user.
function dataset() {
  const rand = rng(1);
  const t = [];
  for (let u = 0; u < USERS; u++) t.push(`group:g${u % GROUPS}#member@user:u${u}`);
  for (let g = 0; g < GROUPS / 2; g++) t.push(`group:g${g}#member@group:g${g + GROUPS / 2}#member`);
  for (let r = 0; r < 5; r++) t.push(`folder:r${r}#viewer@group:g${r}#member`);
  for (let c = 0; c < 25; c++) {
    t.push(`folder:c${c}#parent@folder:r${c % 5}`);
    t.push(`folder:c${c}#editor@group:g${5 + c}#member`);
  }
  for (let l = 0; l < 125; l++) t.push(`folder:l${l}#parent@folder:c${l % 25}`);
  for (let d = 0; d < DOCS; d++) {
    t.push(`document:d${d}#parent@folder:l${d % 125}`);
    t.push(`document:d${d}#owner@user:u${Math.floor(rand() * USERS)}`);
  }
  return t;
}

function tupleRequest(method, tuple, name = "write") {
  return {
    method,
    url: `${BASE}/v1/tuples`,
    body: JSON.stringify({ tuple }),
    params: { headers: { "Content-Type": "application/json" }, tags: { name } },
  };
}

export function setup() {
  const tuples = dataset();
  let revision = 0;
  for (let i = 0; i < tuples.length; i += 50) {
    for (const res of http.batch(tuples.slice(i, i + 50).map((t) => tupleRequest("POST", t, "seed")))) {
      if (res.status !== 200) throw new Error(`seeding failed: ${res.status} ${res.body}`);
      revision = Math.max(revision, res.json("revision"));
    }
  }
  console.log(`seeded ${tuples.length} tuples, revision ${revision}`);
  return { revision };
}

// Skewed toward low indexes, so a few documents and users get most of the traffic,
// as popular objects do in real systems. With power 3, the top 1% get about 22% of picks.
function popular(n) {
  return Math.floor(n * Math.random() ** 3);
}

export default function (data) {
  const doc = `document:d${popular(DOCS)}`;
  const user = `user:u${popular(USERS)}`;

  if (Math.random() < WRITE_RATIO) {
    const req = tupleRequest(Math.random() < 0.5 ? "POST" : "DELETE", `${doc}#viewer@${user}`);
    const res = http.request(req.method, req.url, req.body, req.params);
    check(res, { "write 200": (r) => r.status === 200 });
    return;
  }

  const relation = Math.random() < 0.8 ? "viewer" : "editor";
  let url = `${BASE}/v1/check?object=${doc}&relation=${relation}&user=${user}`;
  if (AT_LEAST) url += `&at_least=${data.revision}`;
  const res = http.get(url, { tags: { name: "check" } });
  check(res, { "check 200": (r) => r.status === 200 });
  cacheHits.add(res.headers["X-Cache"] === "hit");
}

// One JSON line per run, so run-all.sh can collect results. Latencies are in ms.
export function handleSummary(data) {
  const m = data.metrics;
  const d = m["http_req_duration{name:check}"].values;
  const hits = m.cache_hits.values;
  const round = (x) => Math.round(x * 100) / 100;
  const line = {
    rate: RATE,
    write_ratio: WRITE_RATIO,
    at_least: AT_LEAST,
    checks: hits.passes + hits.fails,
    hit_rate: round(hits.rate),
    p50: round(d["p(50)"]),
    p95: round(d["p(95)"]),
    p99: round(d["p(99)"]),
    dropped: m.dropped_iterations ? m.dropped_iterations.values.count : 0,
    failed: m.http_req_failed.values.passes,
  };
  return { stdout: JSON.stringify(line) + "\n" };
}
