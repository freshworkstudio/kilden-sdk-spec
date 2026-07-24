# Kilden Mobile Client SDK Specification

Status: **draft** · Spec version: **0.2** (versioned together with
[SPEC.md](SPEC.md), see its §11)

This document is the authority for the **mobile-session surface** of Kilden's
mobile client SDK (`@kilden-io/expo`, Expo / React Native). It is a companion
to [SPEC.md](SPEC.md), not a replacement: the wire protocol (§4), batching
(§5), identity signing (§6), trust model (§7) and flag semantics (§8) of the
server spec govern the mobile SDK unchanged. What this document adds is the
part the server spec deliberately excludes — sessions, screens, app
lifecycle, exception capture and visual replay — which exists only on
stateful clients.

Sections 1–4 travel **inside `properties`** (opaque to capture, per SPEC.md
§4.5): no new envelope fields, no mock-server changes. Section 6 (visual
replay) is the one surface with its own wire path — the replay chunk
endpoint — specified there.

## 1. Sessions — `$session_id`

Every event the mobile SDK emits carries `properties.$session_id`:

- A **UUID v7**, generated client-side.
- **Rotation**: a new id is generated when more than **30 minutes** have
  passed since the last activity. Activity is *any emitted event* — there is
  no other clock. Rotation happens lazily, at the next emit.
- **Persistence**: the pair `{id, last}` (`last` = epoch ms of last
  activity) is stored under the key `kilden_session` in the SDK's key-value
  storage, so an app restart within the window continues the same session.
  Without working storage the SDK degrades to an in-memory session — events
  are never dropped or left unstamped because storage failed.
- An explicit `$session_id` passed by the caller in event properties wins
  over the stamp (consistent with the context-merge rule: explicit
  properties always win). The override is accepted **verbatim and
  unvalidated** — properties are opaque end to end (SPEC.md §4.5) and the
  SDK never rejects or rewrites them. A non-UUID override simply forms its
  own session key downstream; keeping overrides well-formed is the
  caller's responsibility.

These are the same semantics as the web SDK (`kilden-sdk-js/src/session.ts`)
so a "session" means one thing across the product: the `web_sessions`
aggregation and the panel's session join (`$session_id` extraction) consume
web and mobile events identically.

## 2. Screens — `$screen`

`screen(name, properties?)` emits a `$screen` event with
`properties.$screen_name = name`.

The one non-negotiable rule: **`$screen_name` is the static route name,
never the resolved URL or params** — `user/[id]`, not `user/ana@mail.com`.
Route params routinely contain PII; the static name never does. Navigation
integrations must source the name from the navigation library's route *name*
(react-navigation `route.name`, which expo-router derives from the file
path) and must never interpolate params into it.

A navigation integration collapses consecutive duplicates (re-renders of the
same route emit nothing) and reports the current route on attach.

## 3. App lifecycle — `$app_opened` / `$app_backgrounded`

Driven by the platform's app-state source (React Native `AppState`). Default
**on**; disabled with `trackAppLifecycle: false`. Without a lifecycle source
(plain Node, tests, missing adapter) no lifecycle event is ever emitted.

- `$app_opened` with `properties.$from_background`:
  - `false` — cold start (client construction with a lifecycle source).
  - `true` — return to `active` from a non-active state.
- `$app_backgrounded` — transition from `active` to `background` or
  `inactive`. The iOS `inactive → background` hop emits **once**, not twice.

Both count as session activity, so returning to the foreground after more
than 30 minutes starts a new session (rotation on the `$app_opened` emit).

## 4. Exceptions — `$exception` (opt-in)

Off by default; enabled with `captureExceptions: true`. The SDK wraps the
runtime's global error handler (`ErrorUtils` on React Native) and emits, per
uncaught error:

| Property | Content |
|---|---|
| `$exception_type` | `error.name`, or `"Error"` for non-Error throwables |
| `$exception_message` | the message, **scrubbed** (below) |
| `$exception_fatal` | the runtime's `isFatal` flag as a boolean |
| `$exception_stack` | scrubbed stack, capped at 4000 chars (omitted when absent) |

Ordering per uncaught error: the SDK **first** enqueues the `$exception`
event and, when fatal, initiates a flush; **then** it delegates to the
previously installed handler (dev red screen, crash reporters). Delegation
always happens, even when reporting itself fails. Delivery of a fatal
exception is best-effort: the flush is initiated before delegation, but a
previous handler that terminates the process can still preempt the network
round trip — `persistQueue: true` closes that gap (the event is on disk and
delivers on the next launch).

### Scrubbing

Exception messages routinely drag PII along (emails in auth errors, ids and
card numbers interpolated into strings). Before entering the queue, message
and stack are transformed, **in this order**:

1. Email addresses → `[email]`.
2. Contiguous digit runs of 6 or more → `[digits]` (card numbers, numeric
   ids, unformatted phone numbers; short numbers like line/row references
   survive).
3. Message capped at 1000 characters (applied last, after redaction).

Known limitation, accepted for v1: digits split by separators are **not**
normalized before matching — a formatted phone like `555-123-4567` has no
6-digit contiguous run and survives scrubbing. Implementations must not
silently "improve" on this individually; widening the contract is a spec
change so every SDK keeps the identical privacy guarantee.

This is a **deliberate, documented exception** to SPEC.md's contract 3
("never mutate customer data"): like timestamp formatting, it is an
obligation of the surface, not a mutation of the customer's data — the
customer never chose to send an uncaught error's payload, and unscrubbed
messages would turn a crash into a data leak.

## 5. Session recording config

The `/decide` response's `sessionRecording` block (`{enabled, sampleRate}`,
already served — see SPEC.md §8.4) is parsed and exposed by the mobile SDK:

- An absent or **structurally invalid** block (missing, `null`, a
  non-object, an array) leaves the previous value (initially `null`)
  untouched — backward compatibility is unconditional.
- A structurally valid block is always adopted, field by field: `enabled`
  is `true` only for a literal `true`; a `sampleRate` outside `[0, 1]` or
  non-numeric falls back to `1` (the block is still adopted — only the
  field falls back).
- The config is project-level: it survives `identify()`/`reset()`.

### 5.1 The `mobile` sub-block

Servers that support mobile visual replay (§6) nest the mobile gate inside
`sessionRecording`:

```json
"sessionRecording": { "enabled": true, "sampleRate": 1,
                      "mobile": { "enabled": true, "sampleRate": 0.5 } }
```

Parsing rules mirror the parent block exactly, one level down:

- `mobile` absent or structurally invalid (non-object, `null`, array) →
  parsed as **absent** (`null`), which the recorder treats as *off*. Old
  servers never send it; old SDKs ignore it — compatibility both ways is
  unconditional.
- A structurally valid `mobile` is adopted field by field with the same
  leniency: `enabled` only for literal `true`; bad `sampleRate` → `1`.
- The top-level `enabled`/`sampleRate` govern **web** recording only; the
  mobile recorder reads exclusively `mobile.*`. The server may force the
  whole block off (quota): a poll that stops carrying `mobile` stops the
  recorder at the next poll.

The §5 wiring note is superseded: with §6, the parsed config is no longer
inert.

## 6. Visual replay — screenshot slideshow

Mobile replay captures **periodic screenshots** and synthesizes a **valid
rrweb event stream client-side**, so the entire existing pipeline (chunk
ingest, claim-check storage, session index, web player) consumes it
unchanged. `format_version: 1`.

### 6.1 Gates

Recording happens only when **all** hold, evaluated in this order:

1. **Developer opt-in**: `sessionReplay: true` in `init()` (default off).
2. **Capture capability**: the screenshot adapter (react-native-view-shot's
   `captureScreen`, an optional peer) loaded. Absent → recorder off with a
   one-time debug warning; never a throw.
3. **Server gate**: latest parsed `sessionRecording.mobile.enabled === true`
   (§5.1). Loss of the gate at any later poll stops the recording and
   flushes what was captured.
4. **Sampling** (§6.2) said yes for the current session.
5. Not `optOut()`, not `pauseSessionRecording()`, current screen not in
   `replayDenylist` (§6.6).

### 6.2 Sampling

Web semantics, verbatim (`kilden-sdk-js/src/replay/sampling.ts`): the
decision is rolled **once per session** against `mobile.sampleRate`,
persisted as `{sid, sampled}` under the storage key `kilden_replay_sample`,
sticky for the session's whole life (a stored decision for the current
session id is never re-rolled) and naturally re-rolled when the session
rotates. Storage failure degrades to an in-memory decision.

### 6.3 Recording lifecycle

- One **recording** = one recorder start: `recording_id` (UUID v7) fresh per
  start, `chunk_index` restarting at 0 — exactly the web contract.
- App backgrounded → flush the tail and **stop** the recording. Foregrounded
  again → a **new recording** of the (possibly same) session.
- Session rotation (§1) while recording → stop, then a new recording under
  the new session id. A chunk never crosses sessions.
- `optOut()` → discard **without transmitting** and stop: the in-memory
  buffer is dropped, chunks queued for retry are abandoned, and an upload
  mid-retry is never retried again. After opt-out, no retained replay data
  may leave the device.

### 6.4 Capture

- Source: `captureScreen` (whole native window — includes RN `Modal`s),
  JPEG, `quality: 0.35`, output scaled to the window's **dp size** (device
  px ÷ pixelRatio). Everything downstream — mask rects, touch coordinates,
  rrweb viewport — shares that dp space, so no further transforms exist.
- Triggers: screen change (§2 integration), touch end (§6.5, debounced),
  return to foreground, and a **heartbeat ≥ 10 s** (a floor, never a
  target: an idle screen produces at most one frame per heartbeat).
- Throttle: **≥ 1 s** between frames, always.
- Dedup applies to **same-screen frames only**: a frame whose JPEG data-URI
  is byte-identical to the previous frame's (exact comparison, not a hash —
  a collision must never discard a distinct frame) is dropped before it
  enters the stream. A screen change is NEVER deduplicated: its Meta +
  FullSnapshot (§6.5) are emitted even when the new screen renders
  identical pixels.
- Caps per recording: **300 frames** and **15 minutes** — whichever hits
  first stops the recording (tail flushed).

### 6.5 Synthetic rrweb stream

All numeric constants are rrweb's published enums. Timestamps are real
epoch ms of each capture. Node ids are pinned: document 1, `html` 2,
`head` 3, `body` 4, `img` 5.

Recording start (in this order, same timestamp as the first frame):

1. **Meta** — `{type: 4, data: {href: "kilden-app://screen/<name>",
   width, height}}` with the window dp size; `<name>` is the current
   `$screen_name` (§2) or `unknown` before any.
2. **Custom** — `{type: 5, data: {tag: "kilden_mobile_meta", payload:
   {format_version: 1, platform: "ios"|"android", sdk_version}}}`.
3. **FullSnapshot** — `{type: 2, data: {node, initialOffset: {left: 0,
   top: 0}}}` where `node` is the minimal document: `html > head (empty) +
   body > img#5` with attributes `src` = the frame's data-URI and `style` =
   `width:100vw;height:100vh;object-fit:contain;background:#000`.

Then, per event:

- **Same-screen frame** — `{type: 3, data: {source: 0, texts: [],
  attributes: [{id: 5, attributes: {src: <data-URI>}}], removes: [],
  adds: []}}` (an attribute mutation on the img).
- **Screen change** — a fresh **Meta** (new `href`, current dp size)
  followed by a **FullSnapshot** with the new frame. This is what gives the
  player its navigation markers for free.
- **Rotation / resize** — `{type: 3, data: {source: 4, width, height}}`
  (ViewportResize, new dp size). The img's `100vw/100vh` styling absorbs
  the change; the next frame repaints.
- **Touch** — `{type: 3, data: {source: 2, type: 2, id: 5, x, y}}`
  (MouseInteraction Click on the img, dp coordinates rounded to whole
  numbers) — the player draws these as click markers.

### 6.6 Privacy controls

- **`<Kilden.Mask>`** (component): registered subtrees are measured
  (`measureInWindow`, dp) **at every capture** and their rects blacked out
  on the frame **before it enters the stream** — masked pixels never leave
  the device, in transit or at rest. Rects round **outward** to whole dp.
  **Fail-closed**: a registered mask that cannot be measured at capture
  time drops the frame entirely.
- **`replayDenylist: string[]`** (init option): while the current screen
  name is in the list, no frames are captured at all (the recording keeps
  running; time simply passes between frames).
- **`pauseSessionRecording()` / `resumeSessionRecording()`**: manual gate
  with the same no-frames semantics as the denylist.
- Masking beyond these controls (automatic input masking) is a committed
  roadmap item, not part of `format_version: 1`.

### 6.7 Chunk transport

The web replay transport contract, plus one header:

- `POST` to the replay ingest endpoint; body = the JSON array of rrweb
  events, **uncompressed** (no `Content-Encoding` — React Native has no
  native gzip; the server stores the body verbatim either way).
- Headers: `X-Kilden-Write-Key`, `X-Kilden-Session-Id`,
  `X-Kilden-Recording-Id`, `X-Kilden-Distinct-Id` (URI-encoded),
  `X-Kilden-Chunk-Index`, `X-Kilden-Page-Url` (URI-encoded
  `kilden-app://screen/<name>` of the chunk's first event),
  `X-Kilden-Has-Error` (`1` when a §4 exception fired during the chunk),
  `X-Kilden-First-Event-At` / `X-Kilden-Last-Event-At` (epoch ms), and
  **`X-Kilden-Platform`: `ios` | `android`** (never any other value; web
  SDKs omit it).
- Flush: **512 KB** of serialized events or **30 s**, whichever first.
  Size accounting is the serialized JSON's length; the threshold is checked
  after each append, so a single event that alone exceeds it (a large
  FullSnapshot) ships as its own immediately-flushed chunk — the hard bound
  is the server's 4 MiB chunk cap. Retry with exponential backoff (the
  server is idempotent per `(session, recording, chunk_index)`).
- Any 2xx is success; the response body is never parsed (SPEC.md posture).

### 6.8 Test vectors

`vectors/mobile-replay.json` pins the synthesis byte-exactly: each case
feeds a capture timeline (dims, frames, screen changes, touches, resizes)
and lists the exact rrweb JSON the SDK must produce. Implementations MUST
reproduce the vectors' output with deep equality.

Vector inputs are **synthesizer-level events, after capture policy**: the
recorder's triggers, throttle, dedup and caps (§6.4) run upstream and are
NOT exercised by the vectors. In particular a `touch` input is only the
click event itself — the capture a real touch may additionally trigger
would arrive as its own later `frame` input once the throttle allows it.
