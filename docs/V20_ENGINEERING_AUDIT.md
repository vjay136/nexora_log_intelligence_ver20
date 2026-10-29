# NLI Ver20 engineering audit

Updated: 2026-10-23

## Baseline and scope

Ver20 is derived from released Ver18 commit `8a084b8`. It preserves the client
reader, parser registry, outputs, quarantine, controller authentication,
NURSF telemetry, fleet rollout, RPM identities, systemd units, configuration,
and state paths. Ver20 adds NUPIRE/EGPE as a controller-side, evidence-gated
evolution plane.

## Implemented evidence

- Bounded structure discovery and quote/escape-aware tokenization.
- Declarative-only parser compiler with schema, size, pattern, capture, and
  nesting limits; canonical SHA-256 artifact binding.
- Explicit parsed, partial, and abstain results.
- Disjoint training and held-out evidence enforcement with measurable exact,
  precision, recall, ambiguity, error, and sample dimensions.
- Persistent atomic lifecycle with audited shadow, approval, canary,
  promotion, drift, and rollback transitions.
- NAPI candidate binding to NUPIRE using evidence hashes and declarative
  artifacts without copying retained raw samples.
- Authenticated controller API and a controller-backed WebView workspace.
- Independent approval and the existing signed rollout remain the only route
  into production.
- A consolidated, deduplicated ledger records 100 distinct source-level
  upstream reviews with implementation, test/fixture, activity, and license
  evidence plus explicit reuse caveats.

## Verification summary

The complete Go suite, focused NUPIRE/controller tests, 42 Playwright browser
tests, 20 Vercel/Cloudflare/Vite contracts, Vite production build, shell syntax,
installer contracts, parser provenance, controller-edge checks, and SELinux
rollback fixture pass locally. The tokenizer fuzz target completed 110,438
executions in three seconds without a failure.

## Limits and blockers

- Windows portable Go cannot execute the race detector with CGO disabled.
- Docker is installed but its daemon is unavailable, so deterministic RPM,
  systemd/container, Rocky lifecycle, two-node TLS, and reproduction gates have
  not run on this host.
- No universal accuracy or hardware-independent throughput claim is made.

The current decision is source-publication eligible and **blocked from release
tagging** until every mandatory gate passes.
