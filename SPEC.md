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
