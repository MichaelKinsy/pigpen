---
title: Changelog
icon: lucide/history
---

# Changelog

## v0.2.1 (2026-09-22)

### Bug fixes

- reject incomplete chat completions
- allow OpenAI responses without token usage
- stop retrying provider refusals as malformed output
- support both `httpx` and `httpx2` provider SDKs

### Features

- support Gemini provider

## v0.2.0 (2026-09-18)

### Breaking Changes

- ser/de library has been changed from `msgspec` to `pydantic` as `typesafe-sdk` did in `v0.7.0`

### Features

- support `typesafe-sdk>=0.7.0`

## v0.1.5 (2026-09-18)

### Bug fixes

- constrained `typesafe-sdk` version to `>=0.6.0,<0.7.0`

## v0.1.4 (2026-09-16)

### Bug fixes

- reuse providers and close owned SDK clients. Thanks [@AbdelStark](https://github.com/AbdelStark)!

## v0.1.3 (2026-09-15)

Initial release.
