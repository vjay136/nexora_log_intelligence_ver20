# NLI Ver20 progress and acceptance state

Updated: 2026-10-29

## Release 20.0.0-1 candidate

Ver20 is derived from the latest Ver19 Release 8 source. It adds the complete
guided quarantine lifecycle, exact parser-bundle approval, fleet-safe live
synchronization, TLS hardening, and per-source processing progress while
preserving the ordinary record hot path.

## Verified in the integrated source tree

- Full Go suite: PASS.
- Go vet: PASS.
- Browser UI suite: PASS, 62/62 Chromium scenarios.
- Hosted WebApp contracts: PASS, 24/24 Node scenarios.
- Release static/source checks: PASS.
- Parser approval preserves and validates the exact stored bundle bytes and
  checksum used by rollout and client acknowledgement.
- QRW/GPRS exposes one ordered nine-stage workflow from retained evidence to
  independent approval, scoped rollout, exact client acknowledgement, replay,
  verification, rollback, and offboarding.
- Records are grouped only by a verified failure signature; otherwise they
  remain individual records.
- Client journals and live views expose source, phase, records read, processed,
  parsed, quarantined, errors, and bytes without placing NAPI, NAL, NUPIRE, or
  EGPE on every successful record.
- Controller event streams use rolling deadlines, bounded reconnect backoff,
  pooled TLS transports, and transactional CA replacement suitable for large
  enrolled fleets.

Intentional `[no test files]` output for executable entrypoint packages means
those packages contain no direct unit-test files. Their behavior is covered by
integration, browser, packaging, and lifecycle gates.

## Publication gates

The frozen commit and tag workflow must still produce reproducible RPMs and
evidence, verify checksums, and pass the Linux race, Rocky 9/10.2 lifecycle,
upgrade/rollback, and multi-node TLS gates. Deployment-specific throughput and
destination acceptance remain operator evidence and are not inferred from UI
or unit tests.

Status: **source candidate verified; publication evidence pending tag CI**.