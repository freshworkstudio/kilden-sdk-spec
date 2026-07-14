# Kilden Server SDK Specification

Status: **draft** · Spec version: **0.1**

This document is the single authority for the behavior of Kilden's five
server-side SDKs — PHP, Node (TypeScript), Python, Ruby and Go. The goal is
not "having SDKs": it is that all five behave **identically** given the same
input, and keep doing so two years from now. A pull request against any SDK
that changes behavior without changing this spec (and its test vectors) is
rejected by policy.

The companion artifacts in this repo are part of the spec, not extras:

- [`vectors/`](vectors/) — frozen test vectors every SDK must pass in CI.
- [`mockserver/`](mockserver/) — the mock capture server every SDK's
  integration tests run against, replacing per-language handwritten mocks
  (which is exactly where divergence hides).

## 1. Scope

These SDKs run in trusted backend code, hold a **secret** write key, and have
no notion of a browser session. They are not ports of the web SDK
(`kilden-sdk-js`), whose surface exists because of the browser: persisted
anonymous identity, autocapture, session replay.

The following do **not** exist server-side, deliberately. Do not port them
"for completeness":

- autocapture, session replay
- persisted super properties
- persisted `anonymous_id` / identity state of any kind
- `reset()`, `optOut()`
- `group()` — reserved, consistent with the web SDK: it must not exist yet

Each SDK implements this surface idiomatically — `snake_case` in Ruby and
Python, `camelCase` in PHP and TypeScript, functional `ClientOption`s in Go —
but the **semantics** are this document's and may not vary.

## 2. Public surface

Signatures below are pseudo-PHP; §12 maps them to each language's idiom.

### 2.1 Constructor

```php
$kilden = new Kilden\Client(string $secretWriteKey, array $options = []);
```

| Option           | Default                      | Meaning |
|------------------|------------------------------|---------|
| `host`           | `https://ingest.kilden.io`   | Base URL; `POST {host}/capture` and `POST {host}/decide` |
| `flush_at`       | `20`                         | Queue length that triggers a flush |
| `flush_interval` | `10`                         | Seconds between periodic flushes |
| `max_queue_size` | `10000`                      | Hard cap on queued events (contract 7) |
| `timeout`        | `3`                          | Seconds per HTTP request |
| `transport`      | `null`                       | Transport instance; `null` = autodetect |
| `debug`          | `false`                      | Verbose logging, `$`-prefix warnings |
| `enabled`        | `true`                       | `false` = full no-op (tests, CI, local dev) |

No other options exist. Future extension happens by adding keys here — never
by changing method signatures.

The constructor **may throw** (contract 2), and must when:

- the write key is missing or empty;
- the write key is a **public** key — any key with the `wk_` prefix. Public
  keys degrade server events to `source=client` and break the trust model
  (see §7). The error message must say to use the secret key and to keep it
  out of browsers;
- no transport is available (PHP: neither curl nor stream wrappers) and
  `enabled` is not `false`.

Anything after construction never throws (contract 1).

### 2.2 Methods

```php
$kilden->track(string $distinctId, string $event, array $properties = [], array $opts = []): void;
$kilden->identify(string $distinctId, array $traits = [], array $opts = []): void;
$kilden->alias(string $previousId, string $distinctId): void;
$kilden->isEnabled(string $flagKey, string $distinctId, array $opts = []): bool;
$kilden->getFeatureFlag(string $flagKey, string $distinctId, array $opts = []); // false | true | string
$kilden->flush(): void;   // blocking: drain the queue now
$kilden->close(): void;   // flush + stop the worker; idempotent
```

`$opts` on `track`/`identify` accepts exactly two keys:

- `timestamp` — event time as ISO 8601 UTC (§4.4). Default: now.
- `uuid` — event UUID for retry idempotency. Default: a fresh UUID v7.

`$opts` on `isEnabled`/`getFeatureFlag` accepts exactly two keys (§8):

- `person_properties` — map sent to `/decide`; overrides stored person traits
  for this evaluation only. Reserved for local evaluation later — the
  signature is frozen today so local eval arrives without an API change.
- `default` — what to return when Kilden cannot answer (timeout, network
  error, non-200, unknown flag). Defaults: `false`.

## 3. Behavior contracts

Numbered and testable. SDK READMEs may summarize them; this list is the law.

1. **The public API never throws in the hot path.** After construction,
   `track()`, `identify()`, `alias()`, `flush()`, `close()` and the flag
   methods never raise, whatever the input: invalid input is dropped and
   logged. Telemetry must never take down a customer request.
2. **The constructor is the one place that fails fast** (§2.1). A
   misconfigured SDK should die at boot, not at 3am in production.
3. **Never mutate customer data.** No trimming, lowercasing, normalizing or
   coercion of event names, distinct_ids, property keys or values. What the
   caller passed is what goes on the wire.
4. **`distinct_id` is required and explicit on every call.** The SDK holds no
   identity state. An empty `distinct_id` (or empty `event`) means the event
   is dropped with a warning. `distinct_id` and `event` must be strings; a
   non-string is dropped with a warning in dynamic languages and a compile
   error in typed ones. Events exceeding the wire limits (§4.2) are dropped
   client-side with a warning — the server would reject the whole batch
   otherwise.
5. **`$`-prefixed event names and property keys belong to Kilden.** Using
   them logs a warning when `debug` is on, and the event is **sent anyway** —
   never break customers.
6. **One UUID v7 per event, generated at call time** (or taken from
   `$opts['uuid']`). This is what makes retries idempotent: ClickHouse
   deduplicates by `uuid`. Generated UUIDs are lowercase canonical form,
   version 7; explicit UUIDs are sent verbatim (any RFC 4122 UUID the caller
   chose).
7. **The in-memory queue is bounded.** At `max_queue_size` the **new** event
   is dropped (never the old ones), a warning is logged, and a dropped-events
   counter the caller can read is incremented. Never block the caller's
   thread, never grow without bound.
8. **Retries: exponential backoff with jitter, honoring `Retry-After`.**
   Retryable: 429, 5xx, timeouts, network errors. Not retryable: any other
   4xx — the batch is dropped and logged (retrying a 401 is spam). Policy in
   §4.3.
9. **Fork safety (Python, Ruby).** Preforking servers (gunicorn, puma,
   unicorn) fork after import: the child inherits the parent's queue and a
   dead worker thread. The SDK must detect a PID change on every enqueue and
   respond by discarding the inherited queue and starting a fresh worker.
   Discarding is correct: the parent still owns those events; sending them
   from the child too would duplicate them.
10. **Shutdown.** `close()` is explicit and idempotent. Each SDK also
    registers a best-effort automatic hook (`register_shutdown_function`,
    `atexit`, `at_exit`, none in Go — Go programs call `Close()`). The final
    flush has a deadline of **10 seconds**; whatever has not drained by then
    is dropped and logged. A process must never hang on telemetry.
11. **Batch payload** (§4.1): `POST {host}/capture` with `write_key`,
    `sent_at` and `batch[]`. `Content-Encoding: gzip` when the language does
    it cheaply (§4.5). No JWT anywhere: server-side, the secret key **is**
    the authentication (`source=server`, `verified=true`).
12. **Secret key only.** The constructor rejects public (`wk_`) keys with a
    clear error (§2.1). Symmetric to the platform: `/decide` rejects secret
    keys server-side with 403.

## 4. Wire protocol

### 4.1 `POST {host}/capture`

Headers:

- `Content-Type: application/json`
- `Content-Encoding: gzip` — optional, §4.5
- `User-Agent: kilden-<lang>/<version>` (e.g. `kilden-php/0.1.0`)

Body — exactly these keys, nothing else:

```json
{
  "write_key": "sk_...",
  "sent_at": "2026-07-14T12:34:56.789Z",
  "batch": [
    {
      "uuid": "0197fa10-7a2b-7c3d-8e4f-5a6b7c8d9e0f",
      "event": "order_completed",
      "distinct_id": "user_42",
      "properties": { "revenue": 99.9, "currency": "CLP" },
      "timestamp": "2026-07-14T12:34:56.702Z"
    }
  ]
}
```

- `sent_at` is stamped **when the request is built** (not when the event was
  queued); the server uses it for clock-skew correction
  (`t_real = t_event + (server_now - sent_at)`).
- `properties` is always present, `{}` when empty. It is an opaque JSON
  object to the platform — never typed, never validated beyond being JSON.
- `timestamp` is per event: the `$opts['timestamp']` value, or the wall
  clock at call time.

### 4.2 Limits (server-enforced, SDK pre-validated)

| Limit | Value | SDK behavior |
|---|---|---|
| Events per request | 1000 | flush in chunks of ≤1000 |
| `event` length | 200 bytes | drop event + warn |
| `distinct_id` length | 512 bytes | drop event + warn |
| Request body | 5 MiB (after compression) | keep batches well under; oversize response is not retryable |

### 4.3 Response handling

| Response | SDK action |
|---|---|
| `200` (`{"status":"ok"}`) | done. Note: quota-exceeded projects also get `200` and events are dropped server-side — deliberate, so SDKs do not retry |
| `400` | drop batch + log (malformed — retrying cannot fix it) |
| `401` | drop batch + log (unknown write key) |
| `403` | drop batch + log (origin not allowed) |
| `413` | drop batch + log |
| `429` | retry, waiting `Retry-After` seconds when the header is present |
| `5xx` | retry |
| timeout / network error / corrupt response | retry |

Retry policy, frozen: up to **3 retries** per request (4 attempts total).
Backoff before retry *n* (1-based) is `min(0.5 * 2^(n-1), 30)` seconds —
0.5s, 1s, 2s — multiplied by a random jitter factor in `[0.5, 1.5]`. A
`Retry-After: <seconds>` header on 429 replaces the computed backoff for that
retry (no jitter). When retries are exhausted the batch is dropped, logged,
and counted in the dropped-events counter.

Failed batches are **not** re-queued into the main queue (they would shuffle
ordering and could evict fresh events); the retry loop owns the batch until
success or exhaustion.

### 4.4 Timestamp format

Frozen for every SDK: `YYYY-MM-DDTHH:MM:SS.mmmZ` — UTC, exactly three
fractional digits, `Z` suffix, no offset form. Both `sent_at` and event
`timestamp`s. Caller-supplied timestamps are converted to this form (a
formatting obligation on the SDK, not a mutation of data). Caller values that
cannot be interpreted as a time make the event drop with a warning
(contract 1: never throw).

### 4.5 Compression

gzip the body when it is bigger than **1024 bytes** and the language's
standard library makes it cheap (all five qualify). `Content-Encoding: gzip`
exactly — no `x-gzip`, no lists. The 5 MiB body limit applies to the
compressed bytes.

### 4.6 Event kinds

| Call | `event` | `properties` |
|---|---|---|
| `track(id, name, props)` | `name`, verbatim | `props` |
| `identify(id, traits)` | `$identify` | `{"$set": traits}` (empty object allowed) |
| `alias(previousId, distinctId)` | `$alias` | `{"$alias": distinctId}`, and the envelope `distinct_id` is `previousId` |

`alias` attaches `distinctId` as a new identity of the person `previousId`
already resolves to — mirror of the platform's resolver, where
`distinct_id` must be the **existing** identity and `properties.$alias` the
one being added. Both ids empty-checked per contract 4.

## 5. Batching and delivery

The queue is in-memory, per-process, bounded (contract 7). Delivery models:

- **Node, Python, Ruby**: a background worker (thread / async task) drains
  the queue every `flush_interval` seconds or when it reaches `flush_at`
  events, whichever comes first.
- **Go**: a goroutine, same policy.
- **PHP**: no persistent process exists under FPM. The queue lives for the
  request; flush happens on `flush_at`, on explicit `flush()`/`close()`, and
  in a `register_shutdown_function` hook that calls
  `fastcgi_finish_request()` first when available, so the customer's response
  is not delayed by telemetry. Long-running PHP (Octane, CLI workers) behaves
  like the worker model via the same code path.

`flush()` drains **everything** queued at the moment of the call and blocks
until delivery finishes (including retries) or fails terminally. `close()`
is `flush()` with the 10-second deadline (contract 10) plus worker shutdown;
calling it twice, or using the client after `close()`, is defined: subsequent
events are dropped with a warning.

Fork safety (contract 9) is a **check on every enqueue**: if `getpid()`
differs from the PID captured at construction (or at the last reset), the
inherited queue is discarded, the worker is restarted in the child, and the
stored PID is updated. It must cost an integer comparison, not a syscall per
event, where the platform allows caching (`os.getpid()` is cheap in both
languages; caching the value is still required in Ruby where `Process.pid`
is a syscall).

## 6. Identity signing

Identity verification (Kilden's trust model for browser events) needs the
customer's backend to sign a short-lived JWT. Making that signature a
three-line affair is a large part of why these SDKs exist. It is a separate
class — a page-rendering controller wants a token, not an event queue:

```php
$signer = new Kilden\IdentitySigner(string $identitySecret, ['kid' => 'k1']);

$token = $signer->sign(string $sub, [
    'ttl'    => 3600,                       // seconds; default 3600, max 604800
    'traits' => ['plan' => 'pro'],          // signed traits; optional
]);
```

- `kid` is **required** — the platform looks the secret up by `kid` among the
  project's active identity secrets; a token without a known `kid` fails
  verification silently (`verified=false`).
- `sub` is the `distinct_id` the token vouches for. The platform compares it
  byte-for-byte with the event's `distinct_id`.
- `iat` = now, `exp` = `iat + ttl`. `ttl` outside `(0, 604800]` → the signer
  throws (it is construction-adjacent configuration, not hot path).
- `traits` become **signed traits**: they override unsigned traits of the
  same event during enrichment.

### 6.1 Canonical JWT form (byte-frozen)

A wrong signature fails **silently** (`verified=false`), so the vectors in
[`vectors/identity.json`](vectors/identity.json) require byte-identical
output across the five languages. That forces things JWT libraries usually
leave open — which is why SDKs implement HS256 by hand (~25 lines) instead
of depending on one:

- Header: `{"alg":"HS256","kid":"<kid>","typ":"JWT"}` — exactly these three
  fields, keys in that (lexicographic) order.
- Payload: claim keys sorted lexicographically at **every** nesting level:
  `exp`, `iat`, `sub`, then `traits` when present. Empty or absent traits →
  the `traits` key is omitted entirely.
- JSON: compact separators (no whitespace); UTF-8 preserved (no `\uXXXX`
  escaping of non-ASCII); the three HTML-unsafe ASCII characters `&`, `<`,
  `>` escaped as `\u0026`, `\u003c`, `\u003e` (this matches Go's
  `encoding/json`, which the platform's reference generator uses); integers
  without decimal point or exponent.
- base64url (RFC 4648 §5) **without padding** for all three segments.
- Signature: `HMAC-SHA256(header_b64 + "." + payload_b64, utf8(secret))`.

### 6.2 What the signer must refuse

- Never expose the identity secret in any output, log or error message.
- Documentation must carry the counterexample in bold: signing
  `request.input('user_id')` lets anyone impersonate anyone — **only sign a
  `sub` the backend authenticated**.
- No infinite TTLs (hence the 7-day cap).

### 6.3 The real deliverable: the endpoint

The web SDK refreshes its token 60s before expiry and on 401, against an
endpoint the customer's backend exposes. The framework packages ship it
ready-made:

- Laravel (`kilden/laravel`): publishable route `POST /kilden/identity`
  behind the auth middleware, signing for `auth()->user()`.
- WordPress: `GET /wp-json/kilden/v1/identity` (REST, cache-safe) returning
  `{ distinct_id, token, traits }`.
- Plain Express / FastAPI / Rails / net-http: documented examples, not
  middleware.

## 7. Trust model notes

- Events sent with a secret key are `source=server` and `verified=true` on
  the platform: **facts**. That is the point of server-side revenue events.
- The SDK never sends a JWT on `/capture` (contract 11) — the secret key
  outranks it.
- `/decide` is called with the same secret key the client was constructed
  with. The platform accepts secret keys on `/decide` only for requests
  without a browser `Origin` header; a secret key arriving **with** an
  `Origin` still gets the teaching 403 ("secret keys must never leave your
  backend"). Server SDKs never set `Origin`, so this is invisible to them —
  it exists so a leaked secret key in a browser stays a loud error.

## 8. Feature flags

v1 is **remote evaluation with a short cache**; the signature is already
shaped for local evaluation later (§2.2).

### 8.1 `POST {host}/decide`

```json
{ "write_key": "...", "distinct_id": "user_42",
  "person_properties": { "plan": "pro" } }
```

`person_properties` key omitted when the caller passed none. Response:

```json
{ "flags": { "new_checkout": true, "exp_button": "variant_b", "off_flag": false },
  "sessionRecording": { "enabled": false, "sampleRate": 0 } }
```

Server SDKs ignore `sessionRecording`. Every flag of the project is present
in `flags`; a key **absent** from the map means the flag does not exist →
return `default`.

### 8.2 Client behavior

- `getFeatureFlag` returns the raw value: `false`, `true`, or the variant
  key (string). `isEnabled` returns `true` iff the value is `true` or a
  string.
- Cache: in-memory, keyed by `distinct_id`, holding the whole `flags` map of
  the last `/decide` response for that id, TTL **30 seconds**, LRU-bounded at
  **1000** distinct_ids. Calls with `person_properties` **bypass** the cache
  entirely (no read, no write): overridden evaluations are not reusable.
- Timeout: the client `timeout` option. On timeout, network error, non-200,
  or malformed body: return `default`, log once per failure, never throw
  (contract 1), and do not cache the failure.
- Flag lookups never touch the event queue and are **not** retried — a flag
  answer that arrives after the retry budget is useless to the caller.
  One attempt, then `default`.
- Server SDKs do **not** emit `$feature_flag_called` exposure events in v1
  (deliberate: uncontrolled server-side volume; the browser SDK owns
  exposure tracking today). When this changes it changes here first.

### 8.3 Rollout hashing (frozen now, used later)

Local evaluation will need every SDK to bucket users identically to the
platform's `decide` service. The algorithm is frozen **today** in
[`vectors/flag-hashing.json`](vectors/flag-hashing.json) even though v1
never runs it locally:

```
bucket(flag_key, distinct_id) = u64_be(sha256(flag_key + ":" + distinct_id)[0..8)) / 2^64 * 100
```

- The hash input is the UTF-8 bytes of `flag_key`, one `:` (0x3A), and
  `distinct_id`.
- First 8 bytes of the SHA-256 digest as a **big-endian unsigned 64-bit**
  integer, divided by 2^64 in IEEE 754 double precision, times 100 →
  `[0, 100)`.
- In rollout ⇔ `bucket < rollout_percentage` (strict).
- Variants: an independent point from the same scheme with `":variant"`
  appended to the input, walked over cumulative weights, first
  `point < cumulative` wins.

## 9. Test vectors

Three files under [`vectors/`](vectors/). Every SDK ships a **runner** that
consumes them verbatim in CI — the runner is part of the SDK's test suite,
not an optional extra.

- `payload.json` — SDK call → expected wire event, executed against the mock
  server: build a client pointed at the mock, replay `call`, `flush()`, read
  `GET /__mock/captured`, compare. Placeholders in `expect_event`:
  `"<uuid_v7>"` must match `^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
  `"<iso8601_utc_ms>"` must match `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`.
  Vectors with `"expect": "discarded"` assert the mock captured nothing.
  Property comparison is deep structural equality after JSON parsing.
- `identity.json` — `(secret, kid, sub, iat, exp, traits)` → exact JWT
  string. Runners must compare the **whole token string**. Generated from
  the platform's Go implementation; regenerating requires the kilden-core
  generator (`scripts/specvectors`), never a hand edit.
- `flag-hashing.json` — `(flag_key, distinct_id)` → hash uint64 (decimal
  string — it exceeds 2^53, do not parse as a JSON number in JS), bucket
  floor, and variant picks. Same provenance.

## 10. Mock capture server

`mockserver/` is a zero-dependency Go binary + Docker image that stands in
for `ingest.kilden.io` in every SDK's CI. It validates requests with the
same rules the production capture service enforces, records what it
accepted, evaluates flags with the frozen hashing, and simulates every
failure mode of §4.3 on demand.

| Endpoint | Behavior |
|---|---|
| `POST /capture` | full production validation (§4.1–§4.2 limits, gzip, timestamp format, canonical UUIDs); `200 {"status":"ok"}` |
| `POST /decide` | §8.1, evaluated against flags configured via `/__mock/flags`; accepts secret keys without `Origin`, 403s them with one (§7) |
| `GET /healthz` | `200 ok` |
| `GET /__mock/captured` | everything accepted so far: `{"batches":[…],"events":[…]}` |
| `POST /__mock/reset` | wipe captured data, flags, failure queue, keys to defaults |
| `POST /__mock/flags` | `{"flags":[{"key":…,"active":…,"rollout_percentage":…,"variants":[…]}]}` |
| `POST /__mock/fail` | arm failures for the next N matching requests (below) |
| `POST /__mock/keys` | `{"public":["wk_test_public"],"secret":["sk_test_secret"]}` (defaults shown) |
| `POST /__mock/origins` | `{"origins":["https://allowed.example"]}` — empty = allow all |

`/__mock/fail` body: `{"times": 2, "status": 429, "retry_after": 3}` for
status simulation (any of 401/403/413/429/500…; `retry_after` optional), or
`{"times": 1, "mode": "timeout", "delay_ms": 5000}`, `{"times": 1, "mode":
"corrupt"}` (garbage body with 200), `{"times": 1, "mode": "cut"}`
(connection closed mid-response). Failures apply to `/capture` and
`/decide`, FIFO, then behavior returns to normal.

The mock is **stricter** than production where the spec is stricter (exact
timestamp shape, canonical-form UUIDs, no unknown payload keys): an SDK that
passes here passes production, not vice versa.

## 11. Versioning

The spec, the vectors and the mock server version together (SemVer, tags on
this repo). SDKs pin the spec version they implement in their README and CI
checkout. Behavior changes bump minor pre-1.0; vector regeneration without
behavior change is a patch.

## 12. Idiom map

| Concept | PHP | TypeScript | Python | Ruby | Go |
|---|---|---|---|---|---|
| construct | `new Client($key, [...])` | `new Client(key, {...})` | `Client(key, ...)` | `Client.new(key, ...)` | `New(key, opts ...Option)` |
| `track` | `track` | `track` | `track` | `track` | `Track` |
| `identify` | `identify` | `identify` | `identify` | `identify` | `Identify` |
| `alias` | `alias` | `alias` | `alias` | `alias` | `Alias` |
| `isEnabled` | `isEnabled` | `isEnabled` | `is_enabled` | `enabled?` | `IsEnabled` |
| `getFeatureFlag` | `getFeatureFlag` | `getFeatureFlag` | `get_feature_flag` | `feature_flag` | `FeatureFlag` |
| `flush` | `flush` | `flush` | `flush` | `flush` | `Flush` |
| `close` | `close` | `close` | `close` | `close` | `Close` |
| options | assoc array | object | kwargs | kwargs | functional options |
| signer | `IdentitySigner` | `IdentitySigner` | `IdentitySigner` | `IdentitySigner` | `IdentitySigner` |

Dynamic-language option keys are `snake_case` everywhere (`flush_at`,
`person_properties`); TypeScript uses `camelCase` (`flushAt`,
`personProperties`); Go encodes them as `WithFlushAt(20)`-style options.
