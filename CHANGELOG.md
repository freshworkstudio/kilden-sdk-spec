# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `SPEC-mobile.md` §5.1 + §6: **mobile visual replay** (expo-2) — the
  `sessionRecording.mobile` sub-block parsing rules, and the screenshot
  slideshow contract (`format_version: 1`): gates (dev opt-in + server gate
  + sticky sampling), recording lifecycle, capture rules (dp space, JPEG
  q0.35, heartbeat ≥10 s, hash dedup, 300 frames / 15 min caps), the
  synthetic rrweb stream (pinned node ids, Meta + `kilden_mobile_meta` +
  FullSnapshot, mutation frames, screen changes as Meta+FullSnapshot,
  touches as Click, ViewportResize), privacy controls (`<Kilden.Mask>`
  with pixels-blacked-before-upload and fail-closed measurement, denylist,
  pause/resume) and the chunk transport (+`X-Kilden-Platform`).
- `vectors/mobile-replay.json`: byte-exact synthesis vectors for §6.5.

- `SPEC-mobile.md`: mobile client SDK spec (Expo / React Native) — the
  mobile-session surface: `$session_id` (UUID v7, 30-minute inactivity
  rotation), `$screen`/`$screen_name` (static route names, never resolved
  params), `$app_opened`/`$app_backgrounded`, opt-in `$exception` with a
  normative scrubbing contract, and `sessionRecording` parsing rules.
  Everything travels inside `properties`; wire protocol, batching and the
  mock server are unchanged.

## [0.1.0-alpha.2] - 2026-07-14

### Changed

- Repository moved to the `kildenhq` org; mock server image now publishes
  to `ghcr.io/kildenhq/kilden-mockserver`.

## [0.1.0-alpha.1] - 2026-07-14

### Added

- `SPEC.md`: public surface, 12 behavior contracts, wire protocol, canonical
  JWT form, frozen flag hashing, mock server contract.
- `vectors/payload.json`: 18 call → wire vectors.
- `vectors/identity.json`: 12 byte-exact JWT signing vectors, generated from
  kilden-core.
- `vectors/flag-hashing.json`: 200 rollout + 18 variant vectors, generated
  from kilden-core.
- `mockserver/`: zero-dependency Go mock of `/capture` and `/decide` with
  failure simulation, Docker image.

[Unreleased]: https://github.com/kildenhq/kilden-sdk-spec/compare/v0.1.0-alpha.2...HEAD
[0.1.0-alpha.2]: https://github.com/kildenhq/kilden-sdk-spec/compare/v0.1.0-alpha.1...v0.1.0-alpha.2
[0.1.0-alpha.1]: https://github.com/kildenhq/kilden-sdk-spec/releases/tag/v0.1.0-alpha.1
