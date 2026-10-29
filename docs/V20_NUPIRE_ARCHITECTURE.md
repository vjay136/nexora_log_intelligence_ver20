# NLI Ver20 NUPIRE architecture

Ver20 extends the released Ver18 runtime. It does not replace the agent reader,
deterministic parser packs, output routers, quarantine store, signed rollout,
NURSF synchronization, authentication, TLS, enrollment, RPMs, or systemd
services.

## Runtime boundary

The ordinary client hot path remains:

`source workers -> multiline framing -> parser registry/engine -> schema and
assurance -> normalized output or exact-raw quarantine -> bounded telemetry`.

NUPIRE candidate work runs on the controller/Assurance path. A candidate never
changes production output merely because it was generated or evaluated.

## Evidence-gated evolution

`internal/nupire` provides:

- Evidence-backed structure hypotheses for JSON, key-value, delimited,
  syslog-like, access-log-like, timestamped, and unknown records.
- Quote- and escape-aware tokenization with bounded delimiter inference.
- A declarative parser specification compiler. It validates schema and static
  complexity limits, uses Go's bounded RE2-family regular-expression engine,
  binds the canonical specification to a SHA-256 digest, and returns explicit
  parsed, partial, or abstain outcomes.
- Separate evaluation dimensions for exact matches, precision, recall,
  ambiguity, errors, and sample counts. These measurements are not presented
  as calibrated probabilities.
- A persistent, concurrency-safe lifecycle store with atomic writes and an
  immutable artifact/provenance binding.

The lifecycle is:

`discovery -> candidate -> evaluated -> rejected | shadow -> approval ->
canary -> promoted -> drifted | rolled_back`.

Every transition records actor, time, prior state, new state, and reason.
Training and held-out evidence identifiers must be unique and disjoint. Failed
policy checks produce a rejected candidate; ambiguous evidence cannot enter
shadow. Promotion requires the exact evaluated digest and a recorded approval.

## Existing subsystem integration

The existing bounded NAPI synthesis endpoint generates independent declarative
candidates and tests discovery and held-out inputs. Ver20 binds each selected
NAPI artifact to a NUPIRE candidate identified by parser/version/artifact hash.
Only evidence hashes and the declarative artifact enter NUPIRE; raw Assurance
records are not copied into lifecycle telemetry.

Controller endpoints under `/api/v1/nupire` expose the persistent lifecycle:

- `GET /snapshot`
- `GET /candidates`
- `GET /candidates/{id}`
- `POST /discoveries`
- `POST /candidates/{id}/artifact`
- `POST /candidates/{id}/evaluation`
- `POST /candidates/{id}/actions/{shadow|approve|canary|promote|drift|rollback}`

All writes use existing controller authentication, CSRF handling, RBAC, and
audit logging. The NUPIRE workspace reads these real records; an empty registry
is shown as empty and missing evidence is shown as unavailable rather than zero.

## Safety boundaries

- Parser artifacts are declarative JSON. Shell commands and arbitrary host code
  are rejected.
- Pattern, capture, token, field, nesting, and artifact sizes are bounded.
- Candidate artifacts do not run in the production output path.
- Customer raw records are not published in lifecycle telemetry.
- Existing quarantine and replay remain the recovery source of truth.
- Web disconnection does not affect parsing.
- Production rollout continues through the existing signed parser artifact and
  approval mechanisms; a NUPIRE promoted state does not bypass that rollout.

