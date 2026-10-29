# NLI Ver20 progress and acceptance state

Updated: 2026-10-29

## Release 20.0.0-2 candidate

Release 2 reconciles valid parser records created by earlier releases, isolates
malformed records without taking the controller APIs offline, and automatically
restores the previous catalog and materialized bundle if an approval proposal
cannot be persisted. The quarantine workflow now gives one state-derived path
through independent review, exact-bundle rollout, origin acknowledgement,
targeted replay, verification, rollback, and offboarding.

## Verified in the integrated source tree

- Full Go suite: PASS.
- Go vet: PASS.
- Browser UI suite: PASS, 64/64 Chromium scenarios across every primary workspace and visible subtab.
- Hosted WebApp contracts: PASS, 24/24; standalone and Vite production builds pass.
- Linux race detector: PASS across all Go packages.
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

Status: **Release 2 source candidate verified; publication evidence pending tag CI**.
