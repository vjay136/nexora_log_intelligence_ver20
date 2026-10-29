# Ver20 subsystem names and execution order

This reference separates the client data path from controller-side improvement
workflows. A live transport badge means that current controller metadata is
arriving. It does not mean that every subsystem runs for every record.

## Names shown in the WebApp

| Short name | Full name | Responsibility |
|---|---|---|
| **NHLE** | **Nexora Hypersonic Log Engine** | Client-side source reading, framing, parsing, normalization, bounded queues, quarantine routing, and destination delivery. This is the ordinary per-record hot path. |
| **NURSF** | **Nexora Unified Real-Time Synchronization Fabric** | Authenticated canonical telemetry collection, monotonic revisioning, aggregation, incremental distribution, freshness measurement, and browser resynchronization. It transports metadata and counts; it does not reparse customer records. |
| **NAPI** | **Nexora Adaptive Parser Intelligence** | Bounded synthesis of declarative parser candidates from explicit discovery, held-out, and negative evidence. It is operator/evidence triggered and never changes a live parser directly. |
| **NAL** | **Nexora Assurance Lab** | Deterministic sample/corpus validation, retained test fixtures, test plans, run evidence, and parser review preparation. It is outside the normal ingest hot path. |
| **NUPIRE** | **Nexora Universal Parsing Intelligence & Recovery Engine** | Persists evidence-linked parser discoveries and candidate lifecycle state for recovery work. Candidate state is not production activation. |
| **EGPE** | **Evidence-Gated Parser Evolution** | Applies held-out evidence, independent review, promotion, drift, and rollback gates to parser evolution. It cannot bypass signed rollout approval or client acknowledgement. |
| **UPES** | **Unstoppable Parsing Engine Support** | Bounded client-side structural failure-signature aggregation and counters, surfaced in the UPES Parser Recovery workspace through canonical sync metadata. It observes/groups failures; it does not create candidates or promote parsers. |
| **GPRS** | **Guided Parser Repair Studio** | Assurance UI for retained quarantine evidence, live previews, and checksum-bound parser candidate lifecycle actions. Review, publish, signed bundle rollout, and client acknowledgement remain separate gates. |

## Ordinary successful record

1. A configured source watcher detects appended bytes and restores its durable
   checkpoint after restart.
2. NHLE frames one single-line or multiline record and places it in a bounded
   per-source processing path.
3. The approved ParserPack registry selects and executes the eligible parser,
   validates the field contract, and normalizes the record.
4. NHLE durably writes or delivers the normalized event to configured routes.
   Acknowledgements and retries remain destination-specific.
5. The client advances the input checkpoint only after the accounted customer
   destinations are durable. Optional telemetry or optional destinations do
   not replay an already durable local output.
6. The client publishes bounded counters, rates, queue state, parser revision,
   destination acknowledgement metadata, and resource measurements. Raw
   customer log bodies are excluded from canonical telemetry.
7. NURSF authenticates the update, deduplicates and orders it, updates one
   canonical revision, and broadcasts an incremental change to WebApp
   subscribers. Command Center and subsystem pages consume this same revision.

NAPI, NAL, NUPIRE, EGPE, UPES, and GPRS do not execute during this successful-record path. UPES observes eligible failure events on the failure path only; GPRS is operator-facing controller UI.

## Unsupported or quarantined record recovery

1. NHLE writes an unmatched or invalid record to the local exact-raw quarantine
   queue and sends bounded metadata to the controller. On eligible failures,
   UPES groups normalized structural signatures in bounded process-local state
   and reports counters; the canonical snapshot exposes aggregate/per-client
   counters, not raw records or individual signature details. Exact source
   evidence is available centrally only when encrypted raw retention was
   explicitly enabled.
2. An authorized operator opens **Unknown & Quarantine** or **UPES · Parser
   Recovery**, selects retained evidence, then opens **Guided Parser Repair
   Studio (GPRS)** / Assurance **Recovery Workspace**. View, audited
   download, exclusion/restoration, and retained-evidence deletion remain
   separate permission-checked actions.
3. NAL executes the current production parser against the selected evidence.
   The operator or NAPI can prepare a bounded declarative candidate. NAPI runs
   only when requested and retains hashes/evidence rather than silently changing
   runtime behavior.
4. NAL validates positive, held-out, negative, multiline, and malformed cases.
   A parser with failing evidence remains a draft.
5. GPRS provides failure inbox, preview, and candidate lifecycle controls over
   existing quarantine and Assurance APIs. It does not directly turn UPES
   signatures into candidates. NUPIRE/EGPE may record discovery and evaluation evidence. These lifecycle
   states support review; they do not install a parser.
6. The author submits the validated draft. A different operator reviews and
   approves or rejects it with an audit record. Only an approved candidate can
   be published.
7. Publish converts the supported declarative candidate into a strict sealed
   ParserPack, adds its immutable revision/hash to the signed fleet bundle, and
   creates the existing approval-gated rollout. Unsupported declarative forms
   fail closed instead of being interpreted differently in production.
8. After rollout approval, each selected client verifies the bundle and pack,
   applies it atomically, restarts the agent, and reports the applied bundle
   hash. Failed activation restores the prior assets and parser directory.
9. Targeted replay remains blocked until the originating client acknowledges
   the exact current bundle hash. The operator then replays selected record IDs;
   outcomes update quarantine history and canonical telemetry.
10. A successfully replayed record follows the ordinary NHLE destination path.
    Excluded or permanently purged evidence is never silently reconstructed.

## Operational ordering rule

The production dependency order is:

`source -> NHLE -> customer destination/checkpoint -> NURSF telemetry -> WebApp`

The controlled recovery loop branches only after quarantine:

`quarantine -> UPES bounded observation + operator GPRS/NAL diagnosis -> optional NAPI/NUPIRE/EGPE evidence -> checksum-bound candidate review -> independent approval -> signed fleet rollout -> client hash acknowledgement -> targeted replay -> NHLE`

## AI as the final recovery option

The client can optionally send bounded unknown-cluster samples to a configured
AI provider only after deterministic parsing has failed and UPES has observed
the failure. Successful records never enter the AI queue. A response must pass
the local declarative-schema and regression checks, then is written to the
candidate staging directory; it is not added to the active client registry or
assigned to a Drain cluster.

Controller-side AI remediation follows the same rule: it is offered only after
a failed deterministic Assurance run. Its proposal requires a second reviewer,
validation against the matching evidence, ordinary parser submission,
independent parser approval, publication, signed rollout, and exact applied-hash
acknowledgement. No dashboard or operator should infer activation from the
existence of an AI candidate.

The WebApp displays the authoritative state of each step. It must not infer a
completed approval, rollout, acknowledgement, or replay from elapsed time or
from the existence of a candidate.
