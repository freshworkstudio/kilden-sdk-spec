# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
