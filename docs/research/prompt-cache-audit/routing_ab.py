#!/usr/bin/env python3
"""A/B: does x-opencode-session affect prefix-cache continuity on the OpenCode Go gateway?

Each conversation: unique ~6k-token system prompt (nonce so no cross-run sharing),
then N turns each appending ~1.5k tokens of synthetic tool output + a fixed
assistant reply. Per turn we record prompt P, cached R and lost = min(Pprev,P) - R.
"""
import json, os, random, string, sys, time, uuid, threading, urllib.request

BASE = os.environ.get("BASE", "https://opencode.ai/zen/go/v1")
KEY = os.environ["OPENCODE_API_KEY"]
MODEL = os.environ.get("MODEL", "deepseek-v4.1-flash")
TURNS = int(os.environ.get("TURNS", "16"))
PER_ARM = int(os.environ.get("PER_ARM", "3"))
ARMS = os.environ.get("ARMS", "shared,unique").split(",")
OUT = sys.argv[1]
SHARED = "ses_" + uuid.uuid4().hex[:24]
WPT = int(os.environ.get("WPT", "1100"))
lock = threading.Lock()

WORDS = ["alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet",
         "kilo", "lima", "mike", "november", "oscar", "papa", "quebec", "romeo", "sierra", "tango"]

def blob(rng, n_words):
    return " ".join(rng.choice(WORDS) + str(rng.randint(0, 999)) for _ in range(n_words))

def call(messages, headers):
    body = {"model": MODEL, "messages": messages, "max_tokens": 64, "stream": False}
    extra = json.loads(os.environ.get("EXTRA", "{}"))
    body.update(extra)
    req = urllib.request.Request(BASE + "/chat/completions", data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + KEY, "User-Agent": "steiner-cache-audit/0.1", **headers})
    for attempt in range(4):
        try:
            with urllib.request.urlopen(req, timeout=180) as r:
                return json.loads(r.read()), dict(r.headers)
        except Exception as e:  # retry transient failures
            err = e
            time.sleep(3 * (attempt + 1))
    raise err

def conv(arm, idx):
    rng = random.Random(f"{arm}-{idx}-{time.time()}")
    nonce = uuid.uuid4().hex
    sess = "ses_" + uuid.uuid4().hex[:24]
    if arm == "shared":
        sess = SHARED
    headers = {"x-opencode-session": sess}
    msgs = [{"role": "system", "content": f"run {nonce}. You are a test harness; reply with the single word ok.\n" + blob(rng, 4000)},
            {"role": "user", "content": "Start. " + blob(rng, 200)}]
    prevP = None
    for t in range(1, TURNS + 1):
        resp, rh = call(msgs, headers)
        u = resp.get("usage", {})
        P = u.get("prompt_tokens", 0)
        R = (u.get("prompt_tokens_details") or {}).get("cached_tokens")
        R2 = u.get("prompt_cache_hit_tokens")
        Rv = R if R is not None else (R2 or 0)
        lost = max(0, min(prevP, P) - Rv) if prevP else None
        rec = {"arm": arm, "conv": idx, "turn": t, "P": P, "R": R, "hit_tokens": R2, "miss_tokens": u.get("prompt_cache_miss_tokens"),
               "lost": lost, "ts": time.time(), "ep": rh.get("x-opencode-endpoint-id") or rh.get("X-Opencode-Endpoint-Id"), "usage": u if t <= 2 else None,
               "hdr": {k: v for k, v in rh.items() if k.lower().startswith(("x-", "cf-ray"))} if t <= 2 else None}
        with lock:
            with open(OUT, "a") as f:
                f.write(json.dumps(rec) + "\n")
        prevP = P
        msgs.append({"role": "assistant", "content": "ok"})
        msgs.append({"role": "user", "content": "[tool_result]\n" + blob(rng, WPT)})
        time.sleep(rng.uniform(1, 4))

threads = [threading.Thread(target=conv, args=(arm, i)) for i in range(PER_ARM) for arm in ARMS]
for th in threads:
    th.start(); time.sleep(0.5)
for th in threads:
    th.join()
print("done")
