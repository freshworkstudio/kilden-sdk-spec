# Contributing

This repo is the **authority** for Kilden's server SDKs. The order of
operations for any behavior change is fixed:

1. Change `SPEC.md` (and the vectors, when observable output changes).
2. Then change the SDKs, each with a green vector runner.

A pull request against an SDK that changes behavior without a matching spec
PR is rejected — that is the whole point of this repo. Bug reports about
divergence between an SDK and the spec are the most valuable kind: file them
here, not on the SDK.

## Regenerating vectors

`vectors/identity.json` and `vectors/flag-hashing.json` are generated from
the platform's Go code (`kilden-core`, `scripts/specvectors`) — never edit
them by hand. `vectors/payload.json` is authored here; every `expect_event`
must stay servable by the mock (`go test ./mockserver` checks it).

## Mock server

Zero dependencies, stdlib only, one mutex. Keep it boring: fidelity to
production behavior and easy inspection beat cleverness. New failure modes
need a matching row in `SPEC.md` §10.

## Questions

Use [Discussions](https://github.com/kildenhq/kilden-sdk-spec/discussions)
— answers there stay searchable, which is why we prefer them over chat.
