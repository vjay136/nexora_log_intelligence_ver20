import { test, expect } from '@playwright/test';
import { readFile } from 'node:fs/promises';

// These browser regressions verify frontend behavior independently of network
// and database availability. e2e.spec.js separately exercises a real controller.
const origin = 'http://127.0.0.1:4176';
const tabs = ['operations', 'monitoring', 'streams', 'sync-fabric', 'nhle-monitor', 'napi-monitor', 'nal-monitor', 'assurance', 'quarantine-wizard', 'upes', 'parser-workspace', 'nupire', 'api-monitor', 'field-integrity', 'fleet', 'parser-sync', 'clients', 'controller', 'parsers', 'migration', 'input-paths', 'kv', 'static', 'projection', 'studio', 'regex', 'knownkeys', 'semantic', 'engine-network', 'engine-url', 'engine-email', 'engine-subject', 'outputs', 'metadata', 'unknowns', 'drift', 'performance', 'rollouts', 'artifacts', 'security', 'system', 'communications', 'notifications', 'ai'];
const assets = Object.fromEntries(await Promise.all(['index.html', 'app.js', 'styles.css', 'nli-mark.svg', 'nexora-logo-v20.png', 'login-banner-v20.png', 'nexora-poster-v20.png'].map(async name => [name, await readFile(new URL(name, import.meta.url))])));

async function mockController(page, { role = 'admin', healthIdentity = 'v20', upesSnapshot = null, assurancePassed = true, nupireCandidates = [], quarantineRaw = 'exact raw source line', qrwParsers = [], qrwRecords = null, qrwSyncAssets = null, navigationCounts = null, managedParsers = [] } = {}) {
  const observed = [];
  const cases = [];
  const uploads = [];
  const assuranceRequests = [];
  const assuranceCorpora = [];
  const assurancePlans = [];
  const assuranceRuns = [];
  const outputSaves = [];
  const inputSaves = [];
  const outputProfiles = [];
  const inputProfiles = [];
  const inputRolloutSaves = [];
  const rolloutSaves = [];
  const rolloutProposals = [];
  const historyQueries = [];
  const historyCounts = {};
  const users = [];
  const auditEvents = [];
  const notificationPolicySaves = [];
  const qrwParserActions = [];
  const qrwReplays = [];
  const qrwSyncVerifications = [];
  const qrwRolloutRequests = [];
  let currentBundleHash = qrwSyncAssets?.[0]?.desired?.sha256 || '';
  let currentQRWParsers = qrwParsers.map(item => structuredClone(item));
  const currentManagedParsers = managedParsers.map(item => structuredClone(item));
  let currentQRWRecords = qrwRecords ? structuredClone(qrwRecords) : null;
  let currentQRWSyncAssets = qrwSyncAssets ? structuredClone(qrwSyncAssets) : null;
  const currentQRWHistory = [];
  let authenticated = false;
  await page.route(`${origin}/**`, async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (!path.startsWith('/api/v1/')) {
      const name = path === '/' ? 'index.html' : path.slice(1);
      return route.fulfill({ status: assets[name] ? 200 : 404, contentType: name.endsWith('.js') ? 'application/javascript' : name.endsWith('.css') ? 'text/css' : name.endsWith('.svg') ? 'image/svg+xml' : name.endsWith('.png') ? 'image/png' : 'text/html', body: assets[name] || 'Not found' });
    }
    observed.push(`${request.method()} ${path}`);
    const headers = { 'content-type': 'application/json', 'x-nli-controller': healthIdentity, 'x-nli-upstream': 'controller' };
    const fulfill = (value, status = 200) => {
      if (path === '/api/v1/sync/snapshot' && upesSnapshot) value = { ...value, subsystems: { ...(value.subsystems || {}), upes: upesSnapshot } };
      return route.fulfill({ status, headers, body: JSON.stringify(value) });
    };
    if (path === '/api/v1/health') return fulfill({ status: 'ok', service: 'nli-controller-agent', api_version: 'v1' });
    if (path === '/api/v1/navigation/counts') {
      const lifecycle = items => items.filter(item => ['draft','rejected','in_review','approved'].includes(item.state)).length;
      const waiting = (currentQRWRecords || [{ status: 'waiting' }]).filter(item => ['waiting','replay_failed'].includes(item.status)).length;
      const parserActions = lifecycle(currentQRWParsers);
      const counts = {
        assurance: { total: assuranceRuns.filter(item => ['queued','running'].includes(item.state || item.payload?.state)).length },
        'quarantine-wizard': { total: waiting }, unknowns: { total: waiting }, upes: { total: waiting },
        rollouts: { total: rolloutProposals.filter(item => item.state === 'pending_approval').length },
        parsers: { total: parserActions }, 'parser-workspace': { total: parserActions },
        'input-paths': { total: lifecycle(inputProfiles) }, outputs: { total: lifecycle(outputProfiles) },
        kv: { total: 0 }, static: { total: 0 }, projection: { total: 0 }, studio: { total: 0 }, regex: { total: 0 }, knownkeys: { total: 0 }, semantic: { total: 0 },
        ...(navigationCounts || {}),
      };
      return fulfill({ generated_at: new Date().toISOString(), counts, source: 'controller lifecycle stores' });
    }
    if (path === '/api/v1/auth/login' && request.method() === 'OPTIONS') return route.fulfill({ status: 204, headers: { ...headers, allow: 'POST, OPTIONS' } });
    if (path === '/api/v1/auth/login') { authenticated = true; return fulfill({ status: 'ok' }); }
    if (path === '/api/v1/auth/me') return authenticated ? fulfill({ username: 'test-operator', role, permissions: ['assurance:run'] }) : fulfill({ error: 'unauthorized' }, 401);
    if (path === '/api/v1/auth/csrf') return fulfill({ csrf_token: 'browser-test-csrf' });
    if (path === '/api/v1/auth/logout') { authenticated = false; return fulfill({ status: 'ok' }); }
    if (path === '/api/v1/nupire/snapshot') return fulfill({ candidates: nupireCandidates, count: nupireCandidates.length, evolution_state: 'available' });
    if (path === '/api/v1/assurance/run') {
      const requestBody = request.postDataJSON();
      assuranceRequests.push(requestBody);
      return fulfill({ passed: assurancePassed, route: assurancePassed ? 'normal' : 'quarantine', actual: { message: requestBody.raw, service: requestBody.service }, parser: { name: assurancePassed ? 'test-parser' : 'unknown', version: '7', strategy: 'json' }, counts: { actual: 2, attempts: 1 }, trace: [{ stage: 'parser_attempt', parser: assurancePassed ? 'test-parser' : 'unknown', success: assurancePassed, fields: 2 }], evidence_spans: [{ field: 'message', source_backed: true, start: 0, end: requestBody.raw.length }], parser_creation_helper: { normalized_sample: requestBody.raw.replace(/\r\n/g,'\n').split('\n').map(line=>line.replace(/[\t ]+$/,'')).join('\n'), sample_lines: 2, detected_format: 'key_value', requested_family: requestBody.service, selected_parser: assurancePassed ? 'test-parser' : 'unknown', selected_strategy: 'key_value', match_scope: { kind: 'line', prefix: 'level=' }, suggested_regex: 'level=(?P<level>\\S+) user=(?P<user>\\S+)', kv_delimiters: ['='], fields: [{ name: 'level', source: 'group:level', type: 'string' }, { name: 'user', source: 'group:user', type: 'string' }], projection: ['level','user'], template: { level: '{level}', user: '{user}' }, requires_human_approval: true } });
    }
    if (path === '/api/v1/assurance/corpus' && request.method() === 'POST') { const body=request.postDataJSON();assuranceCorpora.push(body);return fulfill({total:body.cases.length,passed:body.cases.length,failed:0,results:[]}); }
    if (path === '/api/v1/assurance/upload' && request.method() === 'POST') { uploads.push(request.postData() || ''); return fulfill({ retained: true, fixture_id: 'fixture-42', encrypted: true, filename: 'sample.log', content_type: 'text/plain', size_bytes: 28, sha256: 'a'.repeat(64), parser_revision: 'smtp@18.0.0', storage_status: 'encrypted_supabase' }); }
    if (path === '/api/v1/assurance/cases/fixture-42/download') return route.fulfill({ status: 200, headers: { ...headers, 'content-type': 'application/octet-stream' }, body: 'service=example user=operator' });
    if (path === '/api/v1/assurance/cases' && request.method() === 'POST') { const body = request.postDataJSON(); cases.push({ id: 'case-1', name: body.name, revision: 1, manual_retention: true, parser_id: 'test-parser', passed: true }); return fulfill(cases.at(-1)); }
    if (path === '/api/v1/assurance/cases') return fulfill({ cases: [{ id: 'fixture-42', name: 'Retained fixture', payload: { name: 'Retained fixture' } }, ...cases] });
    if (path === '/api/v1/assurance/parsers' && request.method() === 'POST') { const body=request.postDataJSON(),doc={id:'draft-1',revision:1,state:'draft',payload:body};currentQRWParsers.push(doc);return fulfill({id:doc.id,state:doc.state,revision:doc.revision},201); }
    if (path.match(/^\/api\/v1\/assurance\/parsers\/[^/]+\/(validate|submit|approve|reject|cancel|publish|rollback|offboard)$/) && request.method() === 'POST') { const [,id,action]=path.match(/^\/api\/v1\/assurance\/parsers\/([^/]+)\/(validate|submit|approve|reject|cancel|publish|rollback|offboard)$/),doc=currentQRWParsers.find(item=>item.id===id);qrwParserActions.push({id,action,body:request.postDataJSON()});if(doc){doc.state=({validate:doc.state,submit:'in_review',approve:'approved',reject:'rejected',cancel:'draft',publish:'published',rollback:'approved',offboard:'approved'})[action];doc.payload={...doc.payload,...(action==='publish'?{activated:true}:action==='rollback'?{activated:false,rolled_back_at:'2026-10-28T00:00:00Z'}:action==='offboard'?{activated:false,offboarded_at:'2026-10-28T00:00:00Z'}:{})};if(action==='publish')currentBundleHash=doc.payload.artifact_checksum||currentBundleHash;}return fulfill({id,state:doc?.state||action,validation:{score:100},bundle_hash:currentBundleHash,rollout_proposal:['publish','rollback','offboard'].includes(action)?{id:`proposal-${action}-${id}`,state:'pending_approval'}:undefined}); }
    if (path === '/api/v1/assurance/parsers') return fulfill({ parsers: currentQRWParsers });
    if (path === '/api/v1/assurance/napi/synthesize' && request.method() === 'POST') { const body=request.postDataJSON();return fulfill({id:body.id,revision:1,state:'draft',draft_created:true,selected_candidate:'anchored-regex',strategy:'regex',validation:{score:100,held_out_passed:body.held_out.length},regex_safety:{safe:true,engine:'go-re2'},provenance:{discovery_count:body.discovery.length,held_out_count:body.held_out.length,raw_samples_persisted:false},requires_human_approval:true,published:false},201); }
    if (path === '/api/v1/assurance/ai-proposals') return fulfill({ proposals: [], raw_samples_retained: false });
    if (path === '/api/v1/assurance/history') return fulfill({ events: [] });
    if (path === '/api/v1/assurance/plans' && request.method() === 'POST') { const body=request.postDataJSON();const doc={id:body.id,revision:1,state:'active',sha256:'a'.repeat(64),payload:body};assurancePlans.push(doc);return fulfill({id:body.id,revision:1,state:'active'},201); }
    if (path === '/api/v1/assurance/runs' && request.method() === 'POST') { const body=request.postDataJSON(),queued={id:'run-1',plan_id:body.plan_id,plan_revision:body.plan_revision,plan_sha256:'a'.repeat(64),case_id:body.case_id,scenario:body.scenario,state:'queued',queued_at:'2026-10-19T00:00:00Z'},run={...queued,state:'passed',started_at:'2026-10-19T00:00:01Z',finished_at:'2026-10-19T00:00:02Z',evidence:{raw_sha256:'b'.repeat(64),passed:true,score:100}};assuranceRuns.push({id:run.id,state:run.state,payload:run});return fulfill(queued,202); }
    if (path === '/api/v1/assurance/runs') return fulfill({runs:assuranceRuns,count:assuranceRuns.length});
    if (path === '/api/v1/assurance/runs/run-1') return assuranceRuns.length?fulfill(assuranceRuns[0]):fulfill({error:'not found'},404);
    if (path === '/api/v1/assurance/plans') return fulfill({ plans: assurancePlans, count: assurancePlans.length });
    if (path.startsWith('/api/v1/assurance/plans/')) { const id=decodeURIComponent(path.split('/').at(-1)),index=assurancePlans.findIndex(item=>item.id===id);if(request.method()==='PUT'){const body=request.postDataJSON(),previous=assurancePlans[index],doc={id,revision:(previous?.revision||0)+1,state:'active',payload:body};if(index>=0)assurancePlans[index]=doc;else assurancePlans.push(doc);return fulfill({id,revision:doc.revision,state:'active'},201);}if(request.method()==='DELETE'){if(index>=0)assurancePlans.splice(index,1);return fulfill({status:'deleted'});}return index>=0?fulfill(assurancePlans[index]):fulfill({error:'not found'},404); }
    if (path === '/api/v1/quarantine/records/q-1/raw') return route.fulfill({ status: 200, headers: { ...headers, 'content-type': 'text/plain' }, body: quarantineRaw });
    if (path === '/api/v1/quarantine/records/q-1/analyze' && request.method() === 'POST') return fulfill({ raw_sha256: 'c'.repeat(64), record: { id: 'q-1', node_id: 'node-1', raw_available: true }, result: { raw: quarantineRaw, passed: false, parser: { name: 'no-match' } }, parser_changed: false, review_state: 'analysis_only' });
    if (path === '/api/v1/admin/ai-provider') return fulfill({ provider: { provider: 'openai', model: 'test-model', configured: false, enabled: false } });
    if (path === '/api/v1/sync/snapshot') return fulfill({ schema_version: 'nli.sync.v1', revision: 7, generated_at: new Date().toISOString(), target_interval_ms: 1000, aggregate: { nli_source_records_read_total: 120, nli_source_bytes_read_total: 4096, nli_parser_records_succeeded_total: 116, nli_parser_records_failed_total: 4, nli_normalized_records_total: 116, nli_quarantined_records_total: 4, nli_validation_attempts_total: 120, nli_validation_passed_total: 116, nli_validation_failed_total: 4, nli_queue_depth: 4, read_lines_per_second: 90, normalized_per_second: 87, quarantined_per_second: 3, validation_attempts_per_second: 90, validation_passed_per_second: 87, validation_failed_per_second: 3, bytes_per_second: 4096, backpressure_source_count: 0, nli_telemetry_propagation_delay_seconds_p95: 0.08 }, sources: [{ node_id: 'node-1', source: '/var/log/mail.log', process_generation: 'gen-1', sequence: 7, phase: 'tailing', counters: { nli_source_records_read_total: 120, nli_parser_records_succeeded_total: 116, nli_parser_records_failed_total: 4, nli_normalized_records_total: 116, nli_quarantined_records_total: 4, nli_validation_attempts_total: 120, nli_validation_passed_total: 116, nli_validation_failed_total: 4 }, rates: { read_lines_per_second: 90, normalized_per_second: 87, quarantined_per_second: 3, validation_attempts_per_second: 90, validation_passed_per_second: 87, validation_failed_per_second: 3, bytes_per_second: 4096 }, queue: { nli_queue_depth: 4, queue_capacity: 128, backpressure: false }, controller_received_at: new Date().toISOString(), controller_processing_ms: 2, observed_propagation_delay_ms: 80, freshness: 'live', freshness_age_ms: 120, freshness_reason: 'within target', critical: false, diagnostic_codes: [] }], diagnostics: { subscribers: 1, tracked_sources: 1, live: 1, delayed: 0, stale: 0, disconnected: 0, accepted: 7, duplicate: 0, out_of_order: 0, sequence_gaps: 0, reconnects: 0, coalesced: 0, rejected: 0, missed_deadlines: 0, clock_skew: 0, propagation_delay_ms_p50: 35, propagation_delay_ms_p95: 80, propagation_delay_ms_p99: 95, issues: [{ code: 'parser_latency', stage: 'parser', severity: 'warning', component: 'NHLE', node_id: 'node-1', source: '/var/log/mail.log', reason: 'parser latency exceeded warning threshold', recommended_action: 'Inspect parser trace and queue saturation.', last_seen: new Date().toISOString(), occurrences: 1 }] }, controller: { health: 'ok', version: '20.0.0', uptime_seconds: 600, process_memory_alloc_bytes: 33554432, process_memory_sys_bytes: 67108864, cpu_count: 4, goroutines: 12, open_file_descriptors: 21 }, infrastructure: { clients: [{ node_id: 'node-1', hostname: 'mail-node-1', cpu_percent: 18, memory_percent: 31, memory_bytes: 1073741824, load_1m: .4, load_5m: .3, load_15m: .2, updated_at: new Date().toISOString() }] }, subsystems: { nhle: { status: 'live', updated_at: new Date().toISOString(), source_count: 1, active_nodes: 1, records_read_total: 120, normalized_total: 116, quarantined_total: 4, queue_depth: 4, backpressure_sources: 0, read_lines_per_second: 90, normalized_per_second: 87 }, napi: { status: 'live', updated_at: new Date().toISOString(), requests_total: 3, failures_total: 0, pending_drafts: 1, latest_proposal_at: new Date().toISOString() }, nal: { status: 'live', updated_at: new Date().toISOString(), total: 5, queued: 1, running: 1, passed: 3, failed: 0, cancelled: 0, latest_run_at: new Date().toISOString() }, delivery: { status: 'ready', updated_at: new Date().toISOString(), acknowledgements_by_destination: { Loki: { delivered: 116, failed: 1 } }, retry_backlog_latency_status: 'not_instrumented' }, replay: { status: 'ready', state: 'queued', updated_at: new Date().toISOString(), queued: 1, reprocessing: 1, resolved: 2, oldest_queued_age_seconds: 45, attempts_total: 3 }, release_readiness: { status: 'ready', state: 'ready', updated_at: new Date().toISOString(), managed_nodes: 1, release_drifted_nodes: 0, evidence: 'persisted desired and reported active release hashes' } } });
    if (path === '/api/v1/sync/stream') return route.fulfill({ status: 200, headers: { ...headers, 'content-type': 'text/event-stream', 'cache-control': 'no-cache' }, body: '' });
    if (path === '/api/v1/dashboard/snapshot') return fulfill({ overview: { fleet: { total: 1, online: 1 }, metadata: {}, storage: { mode: 'supabase', status: 'connected' } }, metadata: { records: [{ record: { node_id: 'node-1', service: 'smtp-in', parser_id: 'smtp-in/reviewed', route: 'normal', result: 'success', latency_ms: 2, timestamp: '2026-10-17T05:00:00Z' } }], routes: { normal: 1 } }, system: { health: 'ok', version: '20.0.0', storage: { mode: 'supabase', status: 'connected' } }, ai_usage: { requests: 0 }, fleet_sync: { nodes: [{ node: { id: 'node-1', hostname: 'mail-node-1', status: 'online', last_seen: '2026-10-17T05:00:00Z', issues: [], metrics: { logs_received: 120, logs_parsed: 116, logs_quarantined: 4, parse_rate_pct: 96.67, parse_latency_ms: 2.4, cpu_percent: 18, memory_percent: 31 } }, sync: { drifted: false } }] }, timeseries: { from: '2026-10-17T04:58:00Z', to: '2026-10-17T05:00:00Z', points: [{ timestamp: '2026-10-17T04:59:00Z', logs_received: 60, logs_parsed: 58, logs_quarantined: 2, errors: 1, parse_latency_ms_avg: 2.2 }, { timestamp: '2026-10-17T05:00:00Z', logs_received: 60, logs_parsed: 58, logs_quarantined: 2, errors: 0, parse_latency_ms_avg: 2.6 }], system: { controller_cpu_count: 4, controller_goroutines: 12, controller_memory_alloc_bytes: 33554432 } }, stream_files: [{ node_id: 'node-1', source: '/var/log/nli/input/smtp-in.log', phase: 'tailing', read_lines_per_second: 90, normalized_per_second: 87, quarantined_per_second: 3, queue_depth: 4, queue_capacity: 128, backpressure: false, read_duration_ns: 1000000, parse_duration_ns: 2000000, output_duration_ns: 3000000, updated_at: '2026-10-17T05:00:00Z' }] });
    if (path === '/api/v1/overview') return fulfill({ fleet: {}, metadata: {}, storage: { mode: 'supabase', status: 'connected' } });
    if (path === '/api/v1/telemetry/stats') return fulfill({ received: 120, parsed: 116, quarantined: 4 });
    if (path === '/api/v1/telemetry/stream/snapshot') return fulfill({ bounded: true, files: [{ node_id: 'node-1', source: '/var/log/mail.log', phase: 'tailing', read_lines_per_second: 90, normalized_per_second: 80, quarantined_per_second: 5, bytes_per_second: 4096, queue_depth: 4, queue_capacity: 128, backpressure: false, read_duration_ns: 1000000, parse_duration_ns: 2000000, output_duration_ns: 3000000, total_duration_ns: 6000000, read_lines: 900, updated_at: '2026-10-14T00:00:00Z', thresholds: { read_lines_per_second: 80, bytes_per_second: 5000, quarantined_per_second: 10, queue_depth: 100 } }] });
    if (path === '/api/v1/telemetry/query') return fulfill({ telemetry: [] });
    if (path === '/api/v1/quarantine/summary') return fulfill({ waiting: 1, reprocessing: 0, resolved: 2, raw_available: 1 });
    if (path === '/api/v1/quarantine/records') return fulfill({ records: currentQRWRecords || [{ id: 'q-1', node_id: 'node-1', service: 'smtp', first_seen: '2026-10-10T00:00:00Z', attempt_count: 2, status: 'waiting', error_codes: ['no_match'], raw_available: true }] });
    if (path === '/api/v1/quarantine/history') return fulfill({ events: currentQRWHistory });
    if (path === '/api/v1/quarantine/reprocess' && request.method() === 'POST') { const body=request.postDataJSON();qrwReplays.push(body);for(const record of currentQRWRecords||[])if(body.record_ids.includes(record.id))record.status='resolved';currentQRWHistory.push({record_id:'q-1',status:'resolved',outcome:'success'});return fulfill({ rollout_id: 'replay-1', status: 'pending' }, 202); }
    if (path.startsWith('/api/v1/metrics/timeseries')) { const params=new URL(request.url()).searchParams;historyQueries.push(params);const key=params.toString(),call=historyCounts[key]=(historyCounts[key]||0)+1,read=key.includes('range=24h')&&call>1?'42':'9007199254740993',parsed=read==='42'?'40':'9007199254740990'; return fulfill({ from: '2026-10-25T00:00:00Z', to: '2026-10-25T01:00:00Z', canonical_history: { bucket_width_seconds: 60, points: [{timestamp:'2026-10-25T00:59:00Z',read_delta:read,parsed_delta:parsed,quarantine_delta:'2',errors:1}] }, points: [{ timestamp: '2026-10-25T00:59:00Z', received: read, parsed, quarantine: 2, errors: 1, logs_received: read, logs_parsed: parsed, logs_quarantined: 2 }] }); }
    if (path === '/api/v1/verification/sample-corpus') return fulfill({ verdict: 'pass', known_key_inventory: { declared_family_keys: 278, covered_family_keys: 278, unmapped_family_keys: 0 }, totals: { records: 728, accounted_records: 728, parsed_records: 723, exact_parity_records: 723, failing_records: 0, expected_quarantined_records: 5, unexpected_quarantined_records: 0, parsed_comparable_raw_fields: 13101, parsed_generated_json_fields: 13101 }, families: [{ family: 'IMAP', records: 52, passing_records: 52, comparable_raw_fields: 1154, generated_json_fields: 1154, duplicate_occurrences: 0 }], records: [{ corpus: 'samples.log', source_line: 50, family: 'SMTP-OUT', expected_disposition: 'quarantined', disposition_reason: 'truncated record', record_sha256: 'a'.repeat(64) }] });
    if (path === '/api/v1/admin/ai-usage') return fulfill({ requests: 0, tokens: 0, pending_drafts: 0 });
    if (path === '/api/v1/fleet') return fulfill({ nodes: [{ id: 'node-1', hostname: 'mail-node-1', status: 'online', last_seen: '2026-10-17T05:00:00Z' }] });
    if (path === '/api/v1/fleet/sync') return fulfill({ nodes: currentQRWSyncAssets ? currentQRWSyncAssets.flatMap(asset=>asset.clients||[]).map(client=>({node:{id:client.node_id,hostname:client.node_id,status:client.status||'online',last_seen:'2026-10-17T05:00:00Z'},sync:{drifted:!!client.drifted}})) : [{ node: { id: 'node-1', hostname: 'mail-node-1', status: 'online', last_seen: '2026-10-17T05:00:00Z' }, sync: { drifted: false } }] });
    if (path === '/api/v1/parser-sync' && request.method() === 'GET') return fulfill({ bundle: qrwSyncAssets ? { desired_hash: currentBundleHash, parser_ids: currentQRWParsers.filter(item=>item.state==='published').map(item=>item.id), revision: 2 } : undefined, count: currentQRWSyncAssets?.length || 1, assets: currentQRWSyncAssets || [{ asset_class: 'regex', asset_id: 'nginx-access', name: 'Nginx access pattern', desired: { revision: 3, sha256: 'a'.repeat(64), state: 'published' }, controller: { revision: 3, sha256: 'a'.repeat(64), status: 'applied' }, auto_rollout: { enabled: true, status: 'enabled' }, rollout: { id: 'rollout-1', status: 'complete', total_nodes: 1, done_nodes: 1, failed_nodes: 0, errors: [] }, clients: [{ node_id: 'node-1', desired_revision: 3, desired_hash: 'a'.repeat(64), applied_revision: 3, applied_hash: 'a'.repeat(64), last_seen: '2026-10-09T10:00:00Z', status: 'online', drifted: false }] }] });
    if (path === '/api/v1/parser-sync/rollout' && request.method() === 'POST') { const body=request.postDataJSON(),proposal={id:`qrw-rollout-${qrwRolloutRequests.length+1}`,created_by:'test-operator',state:'pending_approval',payload:{type:'parser_sync',name:'Parser bundle rollout',target_mode:body.target_mode,target_nodes:body.target_mode==='all_eligible'?currentQRWSyncAssets?.[0]?.clients?.map(item=>item.node_id)||[]:[body.origin_node_id],bundle_hash:body.bundle_hash}};qrwRolloutRequests.push(body);rolloutProposals.push(proposal);return fulfill({proposal},201); }
    if (path === '/api/v1/parser-sync/verify' && request.method() === 'POST') { const body=request.postDataJSON();qrwSyncVerifications.push(body);const asset=currentQRWSyncAssets?.find(item=>item.asset_id===body.asset_id);const origin=asset?.clients?.find(item=>item.node_id==='node-1');if(origin){origin.applied_hash=origin.desired_hash;origin.drifted=false;}return fulfill({ verified: true, message: 'Reported state refreshed' }); }
    if (path === '/api/v1/parser-sync/retry' && request.method() === 'POST') return fulfill({ proposal_id: 'proposal-1', state: 'pending_approval', message: 'Rollout proposal created; independent approval is required.' });
    if (path.match(/^\/api\/v1\/parser-packs\/[^/]+\/(submit|approve|reject|activate|repair)$/) && request.method() === 'POST') { const [,id,action]=path.match(/^\/api\/v1\/parser-packs\/([^/]+)\/(submit|approve|reject|activate|repair)$/),doc=currentManagedParsers.find(item=>item.id===id);if(!doc)return fulfill({error:'not found'},404);doc.state=({submit:'in_review',approve:'approved',reject:'rejected',activate:'published',repair:'rejected'})[action];doc.runtime_sync=action==='repair'?{status:'not_published'}:doc.runtime_sync;return fulfill(doc); }
    if (path === '/api/v1/parser-packs') return fulfill({ parser_packs: currentManagedParsers });
    if (path === '/api/v1/parsers') return fulfill([{ id: 'smtp', version: 7, format: 'syslog', description: 'SMTP parser', fields: ['message'] }]);
    if (path === '/api/v1/migration/reports') return fulfill({ reports: [] });
    if (path === '/api/v1/config/output-destinations' && request.method() === 'POST') { outputSaves.push(request.postDataJSON()); return fulfill({ id: 'customer-output', state: 'draft', revision: 1 }); }
    if (path === '/api/v1/config/output-destinations') return fulfill({ items: outputProfiles, count: outputProfiles.length });
    if (path.startsWith('/api/v1/config/output-destinations/')) { const item=outputProfiles.find(profile=>path.endsWith(`/${profile.id}`));return item?fulfill(item):fulfill({error:'not found'},404); }
    if (path.match(/^\/api\/v1\/config\/input-paths\/[^/]+\/(submit|approve|publish)$/) && request.method() === 'POST') { const [,id,action]=path.match(/^\/api\/v1\/config\/input-paths\/([^/]+)\/(submit|approve|publish)$/),doc=inputProfiles.find(item=>item.id===id);if(!doc)return fulfill({error:'not found'},404);doc.state=({submit:'in_review',approve:'approved',publish:'active'})[action];return fulfill({id,state:doc.state,revision:doc.revision}); }
    if (path === '/api/v1/config/input-paths' && request.method() === 'POST') { inputSaves.push(request.postDataJSON()); return fulfill({ id: 'mail-paths', state: 'draft', revision: 1 }); }
    if (path === '/api/v1/config/input-paths') return fulfill({ items: inputProfiles, count: inputProfiles.length });
    if (path.match(/^\/api\/v1\/config\/input-paths\/[^/]+\/rollout$/) && request.method() === 'POST') { inputRolloutSaves.push({ path, body: request.postDataJSON() }); return fulfill({ id: `input-proposal-${inputRolloutSaves.length}`, state: 'pending_approval' }, 201); }
    if (path.match(/^\/api\/v1\/config\/input-paths\/[^/]+\/rollout$/)) { const id=decodeURIComponent(path.split('/').at(-2));return fulfill({ profile_id:id, profile_state:'active', history:[], clients:[{node_id:'node-1',hostname:'mail-node-1',status:'online',input_path_sync:{status:'not_targeted'}}] }); }
    if (path.startsWith('/api/v1/config/input-paths/')) { const item=inputProfiles.find(profile=>path.endsWith(`/${profile.id}`));return item?fulfill(item):fulfill({error:'not found'},404); }
    if (path.startsWith('/api/v1/config/')) return fulfill({ items: [] });
    if (path === '/api/v1/metadata') return fulfill({ records: [], stats: {}, payload_retained: false });
    if (path === '/api/v1/rollouts' && request.method() === 'POST') { rolloutSaves.push(request.postDataJSON()); return fulfill({ id: 'proposal-test', state: 'pending_approval' }, 201); }
    if (path.match(/^\/api\/v1\/rollouts\/[^/]+\/(approve|cancel)$/) && request.method() === 'POST') { const [,id,action]=path.match(/^\/api\/v1\/rollouts\/([^/]+)\/(approve|cancel)$/),item=rolloutProposals.find(doc=>doc.id===id);if(item)item.state=action==='approve'?'approved':'cancelled';return fulfill({status:item?.state||action}); }
    if (path === '/api/v1/rollouts') return fulfill({ proposals: rolloutProposals, live: [] });
    if (path === '/api/v1/artifacts') return fulfill({ artifacts: [], signing_keys_configured: 0 });
    if (path === '/api/v1/rbac/roles') return fulfill({ roles: { viewer: ['assurance:run'], admin: ['*'] } });
    if (path === '/api/v1/users') return fulfill({ users });
    if (path === '/api/v1/audit') return fulfill({ verification: 'ok', events: auditEvents });
    if (path === '/api/v1/communications/browser-heartbeat' && request.method() === 'POST') return fulfill({ status: 'recorded', observed_at: new Date().toISOString() });
    if (path === '/api/v1/communications/status') return fulfill({ generated_at: new Date().toISOString(), path: [{ hop: 'client_to_controller', state: 'live', evidence: 'client heartbeat', clients: [{ node_id: 'node-1', hostname: 'mail-node-1', state: 'live', last_seen: new Date().toISOString(), age_seconds: 0, heartbeat_status: 'live' }], client_count: 1 }, { hop: 'controller_internal', state: 'live', evidence: 'controller health', observed_at: new Date().toISOString() }, { hop: 'controller_to_webapp', state: 'live', evidence: 'browser heartbeat', browser: { state: 'live', status: 'recorded', observed_at: new Date().toISOString(), age_seconds: 0, username: 'test-operator', evidence: 'heartbeat accepted' } }], canonical: { revision: 7, generated_at: new Date().toISOString(), schema_version: 'nli.sync.v1', target_interval_ms: 1000 } });
    if (path === '/api/v1/notifications') return fulfill({ notifications: [], count: 0, delivery_history: [], policy: {} });
    if (path === '/api/v1/notifications/config' && request.method() === 'PUT') { const body=request.postDataJSON();notificationPolicySaves.push(body);return fulfill({policy:body,channels:{in_app:{enabled:true,state:'available',status:'available'},email:{enabled:body.channels.email.enabled,state:body.channels.email.enabled?'ready':'not_configured',status:body.channels.email.enabled?'ready':'not_configured'},pagerduty:{enabled:body.channels.pagerduty.enabled,state:body.channels.pagerduty.enabled?'ready':'not_configured',status:body.channels.pagerduty.enabled?'ready':'not_configured'}}}); }
    if (path === '/api/v1/notifications/config') return fulfill({ policy: { channels: { in_app: { enabled: true }, email: { enabled: false, server_env: 'NLI_SMTP_SERVER', sender_env: 'NLI_NOTIFICATION_EMAIL_FROM', recipient_env: 'NLI_NOTIFICATION_EMAIL_RECIPIENTS', username_env:'NLI_SMTP_USERNAME', password_env:'NLI_SMTP_PASSWORD' }, pagerduty: { enabled: false, secret_env: 'NLI_PAGERDUTY_ROUTING_KEY' } }, admin_fallback: true, recipients: ['admin'], events: ['approval','quarantine','sync','system','delivery','security'] }, channels: { in_app: { state: 'available', status: 'available' }, email: { state: 'not_configured', status: 'not_configured' }, pagerduty: { state: 'not_configured', status: 'not_configured' } } });
    if (path.match(/^\/api\/v1\/notifications\/audit-[^/]+$/) && request.method() === 'POST') return fulfill({ status: 'acknowledged' });
    if (path === '/api/v1/system') return fulfill({ version: '20.0.0', health: 'ok', storage: { mode: 'supabase', status: 'connected' }, security: { audit_chain: 'ok' } });
    return fulfill({ error: `Unmocked API: ${path}` }, 500);
  });
  return { observed, uploads, assuranceRequests, assuranceCorpora, assurancePlans, assuranceRuns, outputSaves, inputSaves, outputProfiles, inputProfiles, inputRolloutSaves, rolloutSaves, rolloutProposals, historyQueries, users, auditEvents, notificationPolicySaves, qrwParserActions, qrwReplays, qrwSyncVerifications, qrwRolloutRequests };
}

async function login(page) {
  await page.goto(origin);
  await expect(page.getByRole('button', { name: 'Sign in securely' })).toBeEnabled();
  await page.getByLabel('Username').fill('test-operator');
  await page.getByLabel('Password').fill('test-only-password');
  await page.getByRole('button', { name: 'Sign in securely' }).click();
  await expect(page.getByTestId('nav-operations')).toBeVisible();
  await expect(page.locator('.loading-state')).toHaveCount(0);
}

test('all workspaces render and audit is visible', async ({ page }) => {
  const { observed } = await mockController(page);
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await login(page);
  await expect(page.locator('.topbar-brand')).toContainText('Nexora Log Intelligence');
  await expect(page.locator('.topbar-brand')).toContainText('Infinite Logs. One Engine. Intelligent Parsing.');
  await expect(page.locator('#stream-state')).toHaveText(/Live sync|Reconnecting|Resynchronizing|Streaming unavailable/);
  for (const tab of tabs) {
    await page.getByTestId(`nav-${tab}`).click();
    await expect(page.locator('#page-title')).not.toBeEmpty();
    await expect(page.locator('.loading-state')).toHaveCount(0);
    await expect(page.locator('.error-state')).toHaveCount(0);
    await expect(page.locator('#page')).not.toContainText('undefined');
    if (tab === 'fleet') await expect(page.getByText('mail-node-1')).toBeVisible();
    if (tab === 'operations') await expect(page.locator('#page').getByRole('heading', { name: 'Command Center' })).toBeVisible();
    if (tab === 'api-monitor') await expect(page.getByRole('heading', { name: 'API monitor', exact: true })).toBeVisible();
    if (tab === 'unknowns') await expect(page.getByRole('heading', { name: 'Quarantine and reprocessing' })).toBeVisible();
    if (tab === 'parsers') await expect(page.getByText('SMTP parser', { exact: true }).first()).toBeVisible();
    if (tab === 'security') await expect(page.getByRole('heading', { name: 'Immutable audit chain' })).toBeVisible();
  }
  expect(observed.filter(item => item.endsWith('/api/v1/stream'))).toEqual([]);
  const fleetSyncRequests = observed.filter(item => item === 'GET /api/v1/fleet/sync');
  expect(fleetSyncRequests.length).toBeGreaterThanOrEqual(1);
  expect(fleetSyncRequests.length).toBeLessThanOrEqual(6);
  expect(errors).toEqual([]);
  await page.locator('#notification-bell').click();
  await expect(page.getByText('Notifications', { exact: true })).toBeVisible();
});

test('every visible inner tab activates without browser, request, or render errors', async ({ page }) => {
  test.setTimeout(120_000);
  const { observed } = await mockController(page);
  const errors = [];
  const failedResponses = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('response', response => { if (response.status() >= 500) failedResponses.push(`${response.status()} ${new URL(response.url()).pathname}`); });
  await login(page);
  for (const workspace of tabs) {
    await page.getByTestId(`nav-${workspace}`).click();
    await expect(page.locator('#page')).toHaveAttribute('data-page', workspace);
    await expect(page.locator('.loading-state')).toHaveCount(0);
    await expect(page.locator('.error-state')).toHaveCount(0);
    const innerTabNames = await page.locator('#page [role=tab]:visible').allTextContents();
    for (const rawName of innerTabNames) {
      const name = rawName.trim();
      const tab = page.locator('#page').getByRole('tab', { name, exact: true }).first();
      await expect(tab, `inner tab ${workspace} / ${name} remains mounted`).toBeVisible();
      await tab.click();
      await expect(tab).toHaveAttribute('aria-selected', 'true');
      await expect(page.locator('.error-state')).toHaveCount(0);
      await expect(page.locator('#page')).not.toContainText(/rows\.join is not a function|Request failed|NaN|undefined/);
    }
  }
  expect(errors).toEqual([]);
  expect(failedResponses).toEqual([]);
  expect(observed.length).toBeGreaterThan(40);
});

test('one canonical synchronization snapshot feeds Command Center, NHLE, NAPI and NAL', async ({ page }) => {
  await mockController(page);
  await login(page);
  await expect(page.getByTestId('live-dashboard-kpis')).toContainText('120');
  await expect(page.locator('.chart-legend').first()).toContainText('Input (records / bucket)');
  const firstPoint=page.locator('[data-chart-category="Input"]').first();
  await expect(firstPoint).toBeVisible();
  const readout=await firstPoint.evaluate(point=>{
    point.dispatchEvent(new FocusEvent('focusin',{bubbles:true}));
    return point.closest('.multi-chart')?.querySelector('.chart-hover-readout')?.textContent||'';
  });
  await expect.poll(async()=>readout.match(/Input .* records/)?readout:page.locator('[data-chart-category="Input"]').first().evaluate(point=>{point.dispatchEvent(new FocusEvent('focusin',{bubbles:true}));return point.closest('.multi-chart')?.querySelector('.chart-hover-readout')?.textContent||'';})).toMatch(/Input .* records/);

  await page.getByTestId('nav-sync-fabric').click();
  await expect(page.getByRole('heading',{name:'Unified Real-Time Synchronization Fabric'})).toBeVisible();
  await expect(page.getByText('nli.sync.v1')).toBeVisible();
  await page.getByRole('tab',{name:'Diagnostics'}).click();
  await expect(page.getByText('parser latency exceeded warning threshold')).toBeVisible();
  await expect(page.getByText('Inspect parser trace and queue saturation.')).toBeVisible();

  await page.getByTestId('nav-nhle-monitor').click();
  await expect(page.getByRole('heading',{name:'Nexora Hypersonic Log Engine'})).toBeVisible();
  await expect(page.locator('#page')).toContainText('120');
  await expect(page.locator('#page')).toContainText('90 lines/s');
  await page.getByRole('tab',{name:'Diagnostics'}).click();
  await expect(page.getByText('parser latency exceeded warning threshold')).toBeVisible();

  await page.getByTestId('nav-napi-monitor').click();
  await expect(page.getByRole('heading',{name:'Nexora Adaptive Parser Intelligence'})).toBeVisible();
  await expect(page.locator('#page')).toContainText('Pending drafts');
  await expect(page.locator('#page')).toContainText('3');

  await page.getByTestId('nav-nal-monitor').click();
  await expect(page.getByRole('heading',{name:'Nexora Assurance Lab'})).toBeVisible();
  await expect(page.locator('#page')).toContainText('Total runs');
  await expect(page.locator('#page')).toContainText('5');
  await page.getByTestId('nav-operations').click();
  await expect(page.locator('.throughput-gauges')).toContainText('90.0/s');
});

test('Monitoring exposes eleven canonical dashboards using only the shared recovery path', async ({ page }) => {
  const { observed } = await mockController(page);
  await login(page);
  const before = observed.length;
  await page.getByTestId('nav-monitoring').click();
  await expect(page.getByRole('heading', { name: 'NLI Monitoring' })).toBeVisible();
  await expect(page.locator('.monitoring-tabs [role=tab]')).toHaveCount(11);
  await expect(page.locator('.monitoring-tabs [role=tab][aria-controls]')).toHaveCount(11);
  await expect(page.locator('[data-monitoring-panel=overview]')).toContainText('Ingestion');
  await expect(page.locator('[data-monitoring-panel=overview]')).toContainText('90 rec/s');
  await expect(page.locator('.monitoring-freshness')).toContainText('Revision 7');

  await page.getByRole('tab', { name: 'Pipeline' }).click();
  const validateRow=page.locator('[data-monitoring-panel=pipeline] tr').filter({hasText:'VALIDATE'});
  await expect(validateRow).toContainText('90 rec/s');
  await expect(validateRow).toContainText('87 rec/s');
  await expect(validateRow).toContainText('3 rec/s failed validation');
  await expect(validateRow).not.toContainText('NOT REPORTED');

  await page.getByRole('tab', { name: 'Latency & Synchronization' }).click();
  await expect(page.locator('[data-monitoring-panel=latency]')).toBeVisible();
  await expect(page.getByRole('tab', { name: 'Latency & Synchronization' })).toHaveAttribute('aria-selected','true');
  await expect(page.locator('[data-monitoring-panel=latency]')).toContainText('parser latency exceeded warning threshold');
  await expect(page.locator('[data-monitoring-panel=latency]')).toContainText('Inspect parser trace and queue saturation.');

  await page.getByRole('tab', { name: 'Infrastructure' }).click();
  await expect(page.locator('[data-monitoring-panel=infrastructure]')).toContainText('Client CPU avg18%');
  await expect(page.locator('[data-monitoring-panel=infrastructure]')).toContainText('File descriptors21');
  await expect(page.locator('.filter-contract-note')).toContainText('Component, destination and environment labels are not included');

  await page.getByRole('tab', { name: 'Destinations' }).click();
  await expect(page.locator('[data-monitoring-panel=destinations]')).toContainText('Loki');
  await expect(page.locator('[data-monitoring-panel=destinations]')).toContainText('delivered: 116');
  await page.getByRole('tab', { name: 'Queues / Replay / Quarantine' }).click();
  await expect(page.locator('[data-monitoring-panel=queues]')).toContainText('Oldest pending45 s');
  await page.getByRole('tab', { name: 'Assurance & Release Health' }).click();
  await expect(page.locator('[data-monitoring-panel=assurance]')).toContainText('Release readinessready');
  const newRequests = observed.slice(before).filter(item => !item.endsWith('/api/v1/sync/stream'));
  expect(newRequests.every(item => ['GET /api/v1/sync/snapshot','GET /api/v1/navigation/counts'].includes(item))).toBe(true);
});

test('diagnostics span the dashboard grid and remain usable at narrow widths', async ({ page }) => {
  await mockController(page);
  await login(page);
  const diagnostics=page.locator('.diagnostic-panel').last();
  const desktop=await diagnostics.boundingBox();
  expect(desktop?.width).toBeGreaterThan(700);
  await page.setViewportSize({width:390,height:844});
  const mobile=await diagnostics.boundingBox();
  expect(mobile?.width).toBeGreaterThan(300);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await expect(diagnostics.locator('.table-wrap')).toBeVisible();
});

test('empty telemetry uses reported state and never renders NaN', async ({ page }) => {
  await mockController(page);
  await page.route(`${origin}/api/v1/sync/snapshot`, route => route.fulfill({
    status:200,
    headers:{'content-type':'application/json','x-nli-controller':'v20','x-nli-upstream':'controller'},
    body:JSON.stringify({schema_version:'nli.sync.v1',revision:0,generated_at:new Date().toISOString(),target_interval_ms:1000,aggregate:{},sources:[],diagnostics:{issues:[]},subsystems:{}}),
  }));
  await login(page);
  await expect(page.locator('.diagnostic-panel')).toContainText('No active latency or error diagnostics');
  await expect(page.locator('.dashboard-kpi-strip').first()).toContainText('—');
  await expect(page.locator('#page')).not.toContainText('NaN');
  await page.setViewportSize({width:390,height:844});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

test('NUPIRE workspace displays an explicit empty candidate state', async ({ page }) => {
  await mockController(page);
  await login(page);
  await page.getByTestId('nav-nupire').click();
  await expect(page.getByRole('heading', { name: 'NUPIRE / EGPE workspace' })).toBeVisible();
  await expect(page.getByText('No NUPIRE candidates are persisted yet.')).toBeVisible();
  await expect(page.getByText('No candidates')).toHaveCount(5);
});

test('NUPIRE candidate registry renders non-empty controller results', async ({ page }) => {
  await mockController(page, { nupireCandidates: [{ id: 'candidate-structured-mail', state: 'evaluated', digest: 'a'.repeat(64), metrics: { samples: 12, exact_matches: 12 }, updated_at: '2026-10-26T12:00:00Z' }] });
  await login(page);
  await page.getByTestId('nav-nupire').click();
  await expect(page.getByRole('button', { name: 'candidate-structured-mail' })).toBeVisible();
  await expect(page.locator('#page')).toContainText('12 exact / 12 samples');
  await expect(page.locator('.error-state')).toHaveCount(0);
});

test('navigation leads with Command Center, Assurance Lab, approvals and notifications', async ({ page }) => {
  await mockController(page);
  await login(page);
  const ids=await page.locator('.nav-item').evaluateAll(items=>items.map(item=>item.dataset.page));
  expect(ids.slice(0,18)).toEqual(['operations','monitoring','streams','assurance','quarantine-wizard','upes','rollouts','parser-sync','artifacts','parser-workspace','nupire','field-integrity','ai','parsers','studio','regex','knownkeys','drift']);
  expect(ids[18]).toBe('notifications');
  const input=ids.indexOf('input-paths'),output=ids.indexOf('outputs'),parsing=ids.indexOf('kv');
  expect(ids[input+1]).toBe('migration');
  expect(ids[input+2]).toBe('outputs');
  expect(ids[output+1]).toBe('metadata');
  expect(ids[output+2]).toBe('unknowns');
  expect(ids[output+3]).toBe('performance');
  expect(ids[parsing]).toBe('kv');
  expect(ids.indexOf('parsers')).toBeLessThan(parsing);
  expect(ids.indexOf('parsers')).toBe(ids.indexOf('ai')+1);
  expect(ids.indexOf('engine-network')).toBeGreaterThan(ids.indexOf('drift'));
});

test('UPES observatory uses controller failure and candidate data with explicit unavailable views', async ({ page }) => {
  const mock = await mockController(page, { upesSnapshot: { status: 'live', clients_total: 1, clients_reporting: 1, unique_failure_signatures: 2, observed_failures: 7, discovery_queued: 2, discovery_dropped: 0, discovery_pending: 1 } });
  await login(page);
  await page.getByTestId('nav-upes').click();
  await expect(page.getByRole('heading', { name: 'Unstoppable Parsing Engine Support' })).toBeVisible();
  await expect(page.getByText('q-1', { exact: true }).first()).toBeVisible();
  await expect(page.locator('[data-subsystem-panel=failures]')).toContainText('Observed failures7');
  expect(mock.observed).toContain('GET /api/v1/sync/snapshot');
  expect(mock.observed).not.toContain('GET /api/v1/upes/snapshot');
  await expect(page.getByRole('tab', { name: 'Knowledge Base' })).toBeVisible();
  await page.getByRole('tab', { name: 'Discovery Jobs' }).click();
  await expect(page.locator('[data-subsystem-panel=jobs]')).toContainText('"observed_failures": 7');
  await page.getByRole('tab', { name: 'Knowledge Base' }).click();
  await expect(page.getByText('Knowledge base telemetry unavailable.')).toBeVisible();
});

test('Guided Parser Repair Studio inspects retained failures, uses AI only after failure, and opens the candidate editor', async ({ page }) => {
  const mock = await mockController(page, { assurancePassed: false });
  await login(page);
  await page.getByTestId('nav-assurance').click();
  await page.getByRole('tab', { name: 'Guided Parser Repair Studio' }).click();
  await expect(page.getByText('q-1', { exact: true }).first()).toBeVisible();
  await expect(page.locator('.quarantine-wizard [data-wizard-step]')).toHaveCount(9);
  await page.getByRole('button',{name:'Inspect',exact:true}).click();
  await expect(page.locator('#gprs-preview-form [name=manual_field_name]')).toBeVisible();
  await expect(page.locator('#gprs-preview-form [name=manual_field_index]')).toBeVisible();
  await expect(page.locator('#gprs-preview-form [name=manual_delimiter]')).toBeVisible();
  await expect(page.locator('#gprs-preview-form [name=manual_template]')).toBeVisible();
  await expect(page.locator('#gprs-preview-form [name=manual_regex]')).toBeVisible();
  await page.locator('[data-gprs-record="q-1"]').check();
  const desired = page.locator('#gprs-preview-form [name=gprs_desired]');
  await desired.fill('{"user":"alice"}');
  await page.locator('#gprs-preview-form [name=mapping]').fill('{"username":"user"}');
  await page.locator('[data-wizard-step="6"] button').click();
  await page.getByRole('button', { name: /Run deterministic preview/ }).click();
  await expect(page.getByTestId('gprs-preview')).toContainText('records_tested');
  await expect(page.getByRole('button', { name: 'Ask AI to diagnose and propose a fix' })).toBeVisible();
  expect(mock.observed).toContain('POST /api/v1/quarantine/records/q-1/analyze');
  expect(mock.assuranceRequests.at(-1).desired).toEqual({ user: 'alice' });
  await page.getByRole('button', { name: 'Edit generated candidate' }).click();
  await expect(page.getByRole('tab', { name: 'Guided Parser Repair Studio' })).toHaveAttribute('aria-selected', 'true');
  await expect(page.locator('#assurance-panel-gprs #gprs-parser-helper-form')).toBeVisible();
  await expect(page.locator('#gprs-parser-helper-form [name=samples]')).toHaveValue('exact raw source line');
});

test('Quarantine inbox groups only controller-verified signatures and isolates unverified records', async ({ page }) => {
  await mockController(page, { qrwRecords: [
    { id:'q-unverified-1', node_id:'node-1', service:'smtp', first_seen:'2026-10-10T00:00:00Z', attempt_count:1, status:'waiting', error_codes:['no_match'], raw_available:true },
    { id:'q-unverified-2', node_id:'node-1', service:'smtp', first_seen:'2026-10-10T00:01:00Z', attempt_count:1, status:'waiting', error_codes:['no_match'], raw_available:true },
    { id:'q-verified-1', node_id:'node-1', service:'smtp', first_seen:'2026-10-10T00:02:00Z', attempt_count:1, status:'waiting', error_codes:['no_match'], raw_available:true, failure_signature:'smtp-shape-a' },
    { id:'q-verified-2', node_id:'node-2', service:'smtp', first_seen:'2026-10-10T00:03:00Z', attempt_count:1, status:'waiting', error_codes:['no_match'], raw_available:true, failure_signature:'smtp-shape-a' },
  ] });
  await login(page);
  await page.getByTestId('nav-quarantine-wizard').click();
  await expect(page.getByText('Individual record · shared cause unverified')).toHaveCount(2);
  await expect(page.getByText('2 signature-matched records')).toHaveCount(1);
});

test('Quarantine Resolution Wizard maps the required unstructured fixture without fabricating delimiter values and saves a governed candidate', async ({ page }) => {
  const raw='20250812 130035122+0100 aaa-gmailrgo009 mail 3609 0 140293768701696 Note;systemlogs_for_data';
  const mock=await mockController(page,{quarantineRaw:raw,assurancePassed:true});
  await login(page);
  await page.getByTestId('nav-quarantine-wizard').click();
  await expect(page.locator('#page').getByRole('heading',{name:'Quarantine Resolution Wizard',exact:true})).toBeVisible();
  await expect(page.locator('.quarantine-wizard [data-wizard-step]')).toHaveCount(9);
  await page.getByRole('button',{name:'Inspect',exact:true}).click();
  await expect(page.locator('#gprs-preview-form [name=raw_evidence]')).toHaveValue(raw);
  await page.getByRole('button',{name:/Define desired schema/}).click();
  await page.getByRole('button',{name:'Suggest from raw log'}).click();
  await expect(page.locator('[data-qrw-key=name]')).toHaveCount(9);
  await expect(page.locator('[data-qrw-key=name]').nth(0)).toHaveValue('date');
  await expect(page.locator('[data-qrw-key=name]').nth(7)).toHaveValue('event_type');
  await expect(page.getByRole('button',{name:/Back to evidence/})).toBeVisible();
  await expect(page.getByRole('button',{name:/Generate parser candidate/})).toBeVisible();
  await expect(page.locator('[data-qrw-key=timestamp_format]')).toHaveCount(9);
  await expect(page.locator('[data-qrw-key=null_policy]')).toHaveCount(9);
  const editor=page.locator('#gprs-parser-helper-form');
  const initialParser=await editor.locator('[name=parser]').inputValue();
  await page.getByRole('button',{name:'Test all fields'}).click();
  await expect(page.locator('#qrw-field-status')).toContainText('2 field mapping error');
  await page.locator('[data-wizard-step="5"] button').click();
  await expect(page.getByRole('alert').last()).toContainText('Resolve field mapping errors');
  await expect(editor.locator('[name=parser]')).toHaveValue(initialParser);
  const confirmations=page.getByLabel('Confirm delimiter interpretation');
  await expect(confirmations).toHaveCount(2);
  await confirmations.nth(0).check();
  await confirmations.nth(1).check();
  await expect(confirmations.nth(0)).toBeChecked();
  await expect(confirmations.nth(1)).toBeChecked();
  await page.getByRole('button',{name:'Test all fields'}).click();
  await expect(page.locator('#qrw-field-status')).toContainText('9 fields mapped');
  const desired=JSON.parse(await page.locator('#gprs-preview-form [name=gprs_desired]').inputValue());
  expect(desired).toMatchObject({date:'20250812',hostname:'aaa-gmailrgo009',service:'mail',pid:3609,status:0,minor_pid:140293768701696,event_type:'Note',event_name:'systemlogs_for_data'});
  await page.locator('[data-qrw-key=transform]').first().selectOption('lowercase');
  await page.getByRole('button',{name:'Test all fields'}).click();
  await page.locator('[data-wizard-step="5"] button').click();
	await page.getByRole('button',{name:/Prepare candidate from evidence/}).click();
  await expect(page.getByRole('alert').last()).toContainText('preview-only transform');
  await expect(editor.locator('[name=parser]')).toHaveValue(initialParser);
	await page.getByRole('button',{name:/Back to mapping/}).click();
  await page.locator('[data-qrw-key=transform]').first().selectOption('none');
  await page.getByRole('button',{name:'Test all fields'}).click();
  await page.locator('[data-wizard-step="5"] button').click();
  await page.getByRole('button',{name:/Prepare candidate from evidence/}).click();
  await expect(editor).toBeVisible();
  const parser=JSON.parse(await editor.locator('[name=parser]').inputValue());
  expect(parser.pattern).toContain('(?P<event_type>');
  expect(parser.provenance).toMatchObject({kind:'quarantine_resolution_wizard',generated_from_verified_evidence:true});
  await editor.getByRole('button',{name:'Save review-gated draft'}).click();
  await expect.poll(()=>mock.observed.includes('POST /api/v1/assurance/parsers')).toBe(true);
});

test('QRW stages 6–9 give the next action and gate replay on the exact origin bundle before verify and rollback', async ({ page }) => {
  const checksum='d'.repeat(64),wrong='e'.repeat(64);
  const mock=await mockController(page,{quarantineRaw:'alpha beta',
    qrwParsers:[{id:'qr-candidate',revision:1,state:'in_review',payload:{name:'reviewed-smtp',source_quarantine_record_id:'q-1',artifact_checksum:checksum,validation:{score:100}}},{id:'qr-offboard',revision:2,state:'published',payload:{name:'retired-smtp',source_quarantine_record_id:'q-2',artifact_checksum:checksum,activated:true,validation:{score:100}}}],
    qrwRecords:[{id:'q-1',node_id:'node-1',service:'smtp',first_seen:'2026-10-10T00:00:00Z',attempt_count:2,status:'waiting',error_codes:['no_match'],raw_available:true}],
    qrwSyncAssets:[{asset_class:'parser',asset_id:'qr-candidate',name:'reviewed-smtp',desired:{revision:1,sha256:checksum,state:'published'},rollout:{id:'global-qr-rollout',status:'complete',target_mode:'all_eligible',total_nodes:2,done_nodes:2,failed_nodes:0},clients:[{node_id:'node-1',desired_revision:1,desired_hash:checksum,applied_revision:0,applied_hash:wrong,status:'online',drifted:true},{node_id:'node-2',desired_revision:1,desired_hash:checksum,applied_revision:1,applied_hash:checksum,status:'online',drifted:false}]}]
  });
  await login(page);
  await page.getByTestId('nav-quarantine-wizard').click();
  const lifecycle=page.locator('#page tr').filter({hasText:'reviewed-smtp'});
  await expect(page.locator('.qrw-next-action')).toContainText('Select and inspect one retained failure');
  await page.getByRole('button',{name:'Inspect',exact:true}).click();
  await page.getByRole('button',{name:/Define desired schema/}).click();
  await page.getByRole('button',{name:'Suggest from raw log'}).click();
  await page.getByRole('button',{name:'Test all fields'}).click();
  await page.locator('[data-wizard-step="6"] button').click();
  await page.getByRole('button',{name:/Run deterministic preview/}).click();
  for(const step of [6,7,8,9]){
    const button=page.locator(`[data-wizard-step="${step}"] button`);
    await button.click();
    await expect(page.locator(`[data-wizard-step="${step}"]`)).toHaveClass(/active/);
  }
  await page.getByTestId('nav-operations').click();
  await page.getByTestId('nav-quarantine-wizard').click();
  await expect(page.locator('.qrw-next-action')).toContainText('Review the candidate independently');
  await page.locator('[data-wizard-step="7"] button').click();
  await expect(lifecycle).toContainText('1/2 clients acknowledged');
  await lifecycle.getByRole('button',{name:'Approve',exact:true}).click();
  await page.getByTestId('nav-quarantine-wizard').click();
  await expect(page.locator('.qrw-next-action')).toContainText('Publish the approved candidate');
  await lifecycle.getByRole('button',{name:'Publish',exact:true}).click();
  await page.getByTestId('nav-quarantine-wizard').click();
  await expect(page.locator('.qrw-next-action')).toContainText('Create the origin-client rollout proposal');
  await expect(page.getByRole('button',{name:/Request targeted replay/})).toBeDisabled();
  await expect(page.locator('#gprs-replay-form [name=record_ids] option[value="q-1"]')).toHaveAttribute('disabled','');
  await expect(page.getByTestId('qrw-deployment-status')).toContainText('Published bundle available');
  await expect(page.getByTestId('qrw-deployment-status')).toContainText('Origin rollout proposed');
  await page.getByRole('button',{name:'Propose to all eligible clients'}).click();
  await expect.poll(()=>mock.qrwRolloutRequests).toHaveLength(1);
  expect(mock.qrwRolloutRequests[0]).toEqual({target_mode:'all_eligible',origin_node_id:'node-1',node_ids:[],bundle_hash:checksum});
  const rolloutRow=page.locator('#page tr').filter({hasText:'qrw-rollout-1'});
  await expect(rolloutRow).toContainText('all_eligible · 2 client(s)');
  await expect(rolloutRow.getByRole('button',{name:'Approve'})).toBeDisabled();
  await expect(page.locator('.qrw-next-action')).toContainText('Independent rollout approval is required');
  mock.rolloutProposals[0].state='approved';
  await page.getByTestId('nav-operations').click();
  await page.getByTestId('nav-quarantine-wizard').click();
  await expect(page.locator('.qrw-next-action')).toContainText('Verify the origin client acknowledgement');
  await page.locator('.qrw-next-action').getByRole('button',{name:/Verify origin acknowledgement/}).click();
  await expect.poll(()=>mock.qrwSyncVerifications).toHaveLength(1);
  expect(mock.qrwSyncVerifications[0]).toEqual({asset_class:'parser',asset_id:'qr-candidate'});
  await page.getByTestId('nav-quarantine-wizard').click();
  await expect(lifecycle).toContainText('2/2 clients acknowledged');
  await expect(page.locator('#gprs-replay-form [name=record_ids] option[value="q-1"]')).not.toHaveAttribute('disabled');
  const replay=page.locator('#gprs-replay-form');
  await replay.locator('[name=record_ids]').selectOption('q-1');
  await expect(replay.locator('[name=node_id]')).toHaveValue('node-1');
  await replay.getByRole('button',{name:/Request targeted replay/}).click();
  await expect.poll(()=>mock.qrwReplays).toHaveLength(1);
  expect(mock.qrwReplays[0]).toEqual({record_ids:['q-1'],node_id:'node-1'});
  await expect(page.locator('[data-qrw-panel="9"] .qrw-runbook')).toContainText('Origin record resolved');
  await page.getByTestId('nav-quarantine-wizard').click();
  await expect(page.locator('.qrw-next-action')).toContainText('Verify the replay result');
  await page.locator('[data-wizard-step="7"] button').click();
  await lifecycle.getByRole('button',{name:'Rollback',exact:true}).click();
  await expect.poll(()=>mock.qrwParserActions.some(item=>item.id==='qr-candidate'&&item.action==='rollback')).toBe(true);
  await page.getByTestId('nav-quarantine-wizard').click();
  await page.locator('[data-wizard-step="7"] button').click();
  const offboardRow=page.locator('#page tr').filter({hasText:'retired-smtp'});
  await offboardRow.getByRole('button',{name:'Offboard',exact:true}).click();
  await expect.poll(()=>mock.qrwParserActions.some(item=>item.id==='qr-offboard'&&item.action==='offboard')).toBe(true);
  expect(mock.qrwParserActions.map(item=>item.action)).toEqual(['approve','publish','rollback','offboard']);
});

test('premium login keeps the square V20 emblem and banner fully contained on desktop and mobile', async ({ page }) => {
  await mockController(page);
  await page.goto(origin);
  const lockup=page.locator('.showcase-header .brand-emblem');
  const banner=page.locator('.login-banner-image');
  await expect(lockup).toHaveAttribute('src','./nexora-poster-v20.png');
  await expect(banner).toHaveAttribute('src','./login-banner-v20.png');
  await expect(page.locator('.showcase-header .brand-wordmark b')).toHaveText('Nexora Log Intelligence');
  await expect(page.locator('.showcase-header .brand-wordmark strong')).toHaveText('Nexora');
  await expect.poll(()=>banner.evaluate(image=>image.naturalWidth)).toBeGreaterThan(0);
  await expect(banner).toHaveCSS('object-fit','contain');
  const desktop=await banner.evaluate(image=>({width:image.getBoundingClientRect().width,height:image.getBoundingClientRect().height}));
  expect(Math.abs(desktop.width-desktop.height)).toBeLessThanOrEqual(1);
  await page.setViewportSize({width:390,height:844});
  await expect(page.locator('.auth-showcase')).toBeVisible();
  await expect(page.getByRole('button', {name:'Sign in securely'})).toBeVisible();
  const geometry=await banner.evaluate(image=>({width:image.getBoundingClientRect().width,height:image.getBoundingClientRect().height,scrollWidth:document.documentElement.scrollWidth,clientWidth:document.documentElement.clientWidth}));
  expect(geometry.width).toBeGreaterThan(240);
  expect(Math.abs(geometry.width-geometry.height)).toBeLessThanOrEqual(1);
  expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.clientWidth);
});

test('browser brand, Assurance selection, and account/audit timestamps stay explicit', async ({ page }) => {
  const mock = await mockController(page);
  mock.users.push({ username: 'test-operator', email: 'operator@example.test', role: 'admin', active: true, created_at: '2026-10-24T00:34:56Z', last_login: '2026-10-24T01:02:03Z' });
  mock.users.push({ username: 'never-signed-in', email: 'new@example.test', role: 'viewer', active: true, created_at: '0001-01-01T00:00:00Z', last_login: '0001-01-01T00:00:00Z' });
  mock.auditEvents.push({ sequence: 9, timestamp: '2026-10-24T02:03:04Z', actor: 'admin', role: 'admin', action: 'user.approved', resource: 'test-operator', outcome: 'approved' });
  await login(page);
  await expect(page.locator('link[rel="icon"]')).toHaveAttribute('href', './nli-mark.svg');
  await page.getByTestId('nav-assurance').click();
  const corpusTab = page.locator('[data-assurance-tab="corpus"]');
  await corpusTab.click();
  await expect(corpusTab).toHaveClass(/active/);
  await expect(corpusTab).toHaveAttribute('aria-selected', 'true');
  await page.getByTestId('nav-security').click();
  const usersPanel = page.locator('.panel').filter({ has: page.getByRole('heading', { name: 'Users' }) });
  const created = usersPanel.locator('tr', { hasText: 'test-operator' }).locator('td').nth(4);
  const expected = await page.evaluate(() => new Intl.DateTimeFormat(undefined, { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', timeZoneName: 'short' }).format(new Date('2026-10-24T00:34:56Z')));
  await expect(created).toHaveText(expected);
  await expect(usersPanel.locator('tr', { hasText: 'never-signed-in' }).locator('td').nth(5)).toHaveText('—');
  expect(expected).toMatch(/\d{2}:\d{2}:\d{2}/);
  const auditPanel = page.locator('.panel').filter({ has: page.getByRole('heading', { name: 'Immutable audit chain' }) });
  const approvalTime = auditPanel.locator('tr', { hasText: 'user.approved' }).locator('td').nth(1);
  const approvalExpected = await page.evaluate(() => new Intl.DateTimeFormat(undefined, { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', timeZoneName: 'short' }).format(new Date('2026-10-24T02:03:04Z')));
  await expect(approvalTime).toHaveText(approvalExpected);
  await page.getByTestId('nav-communications').click();
  await expect(page.getByText('Client → Controller', { exact: true })).toBeVisible();
  await page.getByTestId('nav-notifications').click();
  await expect(page.getByText('not_configured').first()).toBeVisible();
  await page.getByLabel('Enable SMTP email').check();
  await page.getByLabel('Enable PagerDuty').check();
  await page.getByLabel('Recipient usernames, comma separated').fill('admin, oncall-1');
  await page.getByLabel('SMTP server environment variable').fill('NLI_SMTP_SERVER_ENV');
  await page.getByLabel('Email sender environment variable').fill('NLI_MAIL_FROM_ENV');
  await page.getByLabel('Email recipient-list environment variable').fill('NLI_MAIL_TO_ENV');
  await page.getByLabel('SMTP username environment variable').fill('NLI_SMTP_USER_ENV');
  await page.getByLabel('SMTP password environment variable').fill('NLI_SMTP_PASS_ENV');
  await page.getByLabel('PagerDuty routing-key environment variable').fill('NLI_PD_KEY_ENV');
  await page.getByRole('button', { name: 'Save notification policy' }).click();
  await expect.poll(() => mock.notificationPolicySaves.length).toBe(1);
  expect(mock.notificationPolicySaves[0]).toEqual({ channels: { in_app: { enabled: true }, email: { enabled: true, server_env: 'NLI_SMTP_SERVER_ENV', sender_env: 'NLI_MAIL_FROM_ENV', recipient_env: 'NLI_MAIL_TO_ENV', username_env:'NLI_SMTP_USER_ENV', password_env:'NLI_SMTP_PASS_ENV' }, pagerduty: { enabled: true, secret_env: 'NLI_PD_KEY_ENV' } }, admin_fallback: true, recipients: ['admin','oncall-1'], events: ['approval','quarantine','sync','system','delivery','security'] });
  expect(JSON.stringify(mock.notificationPolicySaves[0])).not.toMatch(/secret-value|routing-key-value/i);
  expect(mock.observed).toContain('POST /api/v1/communications/browser-heartbeat');
});

test('Assurance Lab tabs isolate each workflow and expose complete generated outputs', async ({ page }) => {
  await mockController(page);
  await login(page);
  await page.getByTestId('nav-assurance').click();
  await page.getByRole('tab', {name:'Parser Creation Helper'}).click();
  await expect(page.locator('#assurance-panel-helper')).toBeVisible();
  await expect(page.locator('#assurance-panel-analysis')).toBeHidden();
  const form=page.locator('#parser-helper-form');
  await form.locator('[name=samples]').fill('level=INFO user=alice');
  await page.getByRole('button', {name:'Generate parser draft'}).click();
  await expect(page.locator('.parser-helper-output')).toHaveCount(6);
  for(const label of ['Generated JSON','Regex','Templates','KV rules','Field mappings','Field definitions']) await expect(page.getByRole('heading',{name:label})).toBeVisible();
  await expect(page.getByRole('button',{name:'Copy Regex'})).toBeVisible();
});

test('NAPI Synthesis validates separate datasets and exposes review-gated evidence', async ({ page }) => {
  await mockController(page);
  await login(page);
  await page.getByTestId('nav-assurance').click();
  await page.getByRole('tab',{name:'NAPI Synthesis',exact:true}).click();
  const form=page.getByTestId('napi-synthesis-form');
  await expect(form).toBeVisible();
  await form.getByRole('button',{name:'Synthesize and validate draft'}).click();
  const result=page.getByTestId('napi-synthesis-result');
  await expect(result).toContainText('anchored-regex');
  await expect(result).toContainText('requires_human_approval');
  await expect(result).toContainText('raw_samples_persisted');
  await expect(result).toContainText('false');
});

test('Assurance Test Plans saves versioned declarative plans through the controller API', async ({ page }) => {
  const { assurancePlans } = await mockController(page);
  await login(page);
  await page.getByTestId('nav-assurance').click();
  await page.getByRole('tab', {name:'Test Plans'}).click();
  await expect(page.locator('#assurance-panel-plans')).toBeVisible();
  const form=page.locator('#assurance-plan-form');
  await form.locator('[name=id]').fill('smtp-certification');
  await form.locator('[name=name]').fill('SMTP certification');
  await form.locator('[name=version]').fill('1.0.0');
  await form.getByRole('button',{name:'Save test-plan revision'}).click();
  await expect.poll(()=>assurancePlans.length).toBe(1);
  expect(assurancePlans[0].payload.plan.scenarios).toEqual(['parser-regression','failure-recovery','replay','destination-integration','performance','security']);
  await expect(page.getByText('SMTP certification',{exact:true})).toBeVisible();
});


test('Assurance Runs use an exact plan revision and retained fixture and display persisted evidence', async ({ page }) => {
  const { assuranceRuns } = await mockController(page);
  await login(page);
  await page.getByTestId('nav-assurance').click();
  await page.getByRole('tab', {name:'Test Plans'}).click();
  const planForm=page.locator('#assurance-plan-form');
  await planForm.locator('[name=id]').fill('parser-certification');
  await planForm.locator('[name=plan]').fill(JSON.stringify({objective:'Certify parser',environment:'synthetic-lab',scenarios:['parser-regression'],pass_criteria:['exact expected output']}));
  await planForm.getByRole('button',{name:'Save test-plan revision'}).click();
  await page.getByRole('tab',{name:'Runs',exact:true}).click();
  await expect(page.locator('#assurance-run-form')).toBeVisible();
  await expect(page.getByTestId('assurance-run-scenario').locator('option')).toHaveCount(6);
  await page.getByTestId('assurance-run-start').click();
  await expect.poll(()=>assuranceRuns.length).toBe(1);
  expect(assuranceRuns[0].payload).toMatchObject({plan_id:'parser-certification',plan_revision:1,case_id:'fixture-42',state:'passed'});
  await expect(page.getByTestId('assurance-run-row')).toContainText('parser-certification');
  await expect(page.getByTestId('assurance-run-row')).toContainText('b'.repeat(64));
});

test('AI workspace exposes provider, status, results, parser, regex, template and approval categories', async ({ page }) => {
  await mockController(page);
  await login(page);
  await page.getByTestId('nav-ai').click();
  for(const category of ['Provider','Status','Results','Parser','Regex','Template','Approval']){
    const tab=page.getByRole('tab',{name:category,exact:true});
    await tab.click();
    await expect(tab).toHaveAttribute('aria-selected','true');
    await expect(page.locator(`#ai-panel-${category.toLowerCase()}`)).toBeVisible();
  }
});

test('V20 security identity and task navigation remain accessible', async ({ page }) => {
  await mockController(page);
  await page.goto(origin);
  const officialLogo = page.getByAltText('Nexora Log Intelligence logo').first();
  await expect(officialLogo).toBeVisible();
  await expect(officialLogo).toHaveJSProperty('complete', true);
  expect(await officialLogo.evaluate(image => image.naturalWidth)).toBeGreaterThan(0);
  await login(page);
  const groups = await page.locator('.nav-group').allTextContents();
  for (const group of ['COMMAND CENTER','ASSURANCE & RELEASE','OPERATIONS','REAL-TIME SUBSYSTEMS','INPUT', 'PARSING ENGINE', 'OUTPUT', 'CLIENT MANAGEMENT', 'CONTROLLER MANAGEMENT', 'PARSER MANAGEMENT']) {
    expect(groups).toContain(group);
  }
  expect(groups[groups.indexOf('ASSURANCE & RELEASE') + 1]).toBe('PARSER MANAGEMENT');
  await page.getByTestId('nav-fleet').click();
  await expect(page.getByText('Enrolled clients', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Client monitoring dimensions')).toContainText('Parser activity');
});

test('eagle identity honors reduced-motion preference', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await mockController(page);
  await page.goto(origin);
  const logo = page.locator('.eagle-brand .brand-logo');
  await expect(logo).toBeVisible();
  await expect(logo).toHaveCSS('animation-name', 'none');
  await expect(page.locator('.eagle-orbit')).toHaveCSS('animation-name', 'none');
});

test('streams render live per-node rates and file processing timings', async ({ page }) => {
  await mockController(page);
  await login(page);
  await page.getByTestId('nav-streams').click();
  await expect(page.getByRole('heading', { name: 'Streams', exact: true, level: 1 })).toBeVisible();
  await expect(page.locator('#stream-live-table')).toContainText('/var/log/mail.log');
  await expect(page.locator('#stream-live-table')).toContainText('90.0');
  await expect(page.locator('#stream-live-table')).toContainText('4 / 128');
  await expect(page.locator('#stream-live-table')).toContainText('tailing');
  await page.getByTestId('nav-performance').click();
  await expect(page.getByRole('heading', { name: 'Canonical throughput and latency' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Per-source processing and freshness' })).toBeVisible();
  await expect(page.locator('#page')).toContainText('parser latency exceeded warning threshold');
});

test('operations reuses canonical sync and only fetches one aggregate history series', async ({ page }) => {
  const { observed } = await mockController(page);
  await login(page);
  const start = observed.length;
  await page.getByTestId('nav-operations').click();
  const requests = observed.slice(start);
  expect(requests.filter(item => item.startsWith('GET /api/v1/dashboard/snapshot'))).toHaveLength(0);
  // The shared snapshot recovery loop may race the navigation while the
  // mocked EventSource is opening.  It is the one permitted canonical
  // fallback. The retained timeline is one aggregate controller query; it
  // must not fan out into fleet, metadata, system, or overview polling.
  expect(requests.filter(item => item === 'GET /api/v1/sync/snapshot').length).toBeLessThanOrEqual(1);
  expect(requests.filter(item => item.startsWith('GET /api/v1/metrics/timeseries')).length).toBeLessThanOrEqual(1);
  expect(requests.some(item => /GET \/api\/v1\/(overview|metadata\?|system|fleet\/sync)/.test(item))).toBe(false);
});

test('live dashboard renders controller metrics, client filter, pipeline timing and safe failures', async ({ page }) => {
  const { observed } = await mockController(page);
  await login(page);
  await expect(page.locator('#page').getByRole('heading', { name: 'Command Center' })).toBeVisible();
  await expect(page.getByTestId('live-dashboard-kpis')).toContainText('120');
  await expect(page.getByText('96.67% success')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Pipeline time travel' })).toBeVisible();
  await expect(page.getByText('/var/log/mail.log').first()).toBeVisible();
  await expect(page.locator('#page')).not.toContainText('service=');
  const before=observed.length;
  await page.locator('#dashboard-client').selectOption('node-1');
  expect(observed.slice(before).some(item=>item.startsWith('GET /api/v1/dashboard/snapshot'))).toBe(false);
  await page.getByTestId('nav-monitoring').click();
  await page.getByRole('tab', { name: 'Parser Performance' }).click();
  await expect(page.locator('[data-monitoring-panel=parsers]')).toContainText('Parser successes');
  await expect(page.locator('[data-monitoring-panel=parsers]')).toContainText('Parse p95');
  await expect(page.locator('[data-monitoring-panel=parsers]')).toContainText('Source workers');
});

test('canonical SSE reconnect resumes from the last accepted revision without legacy polling', async ({ page }) => {
  await page.clock.install();
  await mockController(page);
  await page.addInitScript(() => {
    const instances=[];
    class FailingEventSource extends EventTarget {
      constructor(url){super();this.url=url;this.closed=false;instances.push(this);window.__failingSources=instances;setTimeout(()=>this.onerror?.(new Event('error')),0);}
      close(){this.closed=true;}
    }
    window.EventSource=FailingEventSource;
  });
  await login(page);
  await page.getByTestId('nav-streams').click();
  await expect.poll(() => page.evaluate(() => window.__failingSources?.length||0)).toBe(1);
  await page.evaluate(() => window.__failingSources[0].onerror?.(new Event('error')));
  await page.clock.fastForward(5_100);
  await expect.poll(() => page.evaluate(() => window.__failingSources.length)).toBeGreaterThan(1);
  expect(await page.evaluate(() => window.__failingSources.at(-1).url)).toContain('last_revision=7');
  await expect(page.locator('#stream-live-table')).toContainText('/var/log/mail.log');
});

test('canonical SSE deltas update stream rows and reconnect from the accepted revision', async ({ page }) => {
  await mockController(page);
  await page.addInitScript(() => {
    const instances=[];
    class FakeEventSource extends EventTarget {
      constructor(url){super();this.url=url;this.closed=false;instances.push(this);window.__streamSources=instances;setTimeout(()=>this.onopen?.(),0);}
      close(){this.closed=true;}
      emit(type,data,id=''){const event=new MessageEvent(type,{data:JSON.stringify(data)});Object.defineProperty(event,'lastEventId',{value:id});this.dispatchEvent(event);}
    }
    window.EventSource=FakeEventSource;
  });
  await login(page);
  await page.getByTestId('nav-streams').click();
  await expect(page.locator('#stream-live-table')).toContainText('node-1');
  await expect.poll(() => page.evaluate(() => window.__streamSources?.length||0)).toBeGreaterThan(0);
  await page.evaluate(() => window.__streamSources.at(-1).emit('sync_delta',{schema_version:'nli.sync.v1',revision:8,generated_at:new Date().toISOString(),target_interval_ms:1000,aggregate:{nli_source_records_read_total:132,nli_parser_records_succeeded_total:126,nli_parser_records_failed_total:1,nli_normalized_records_total:126,nli_quarantined_records_total:6,nli_queue_depth:5,read_lines_per_second:102,normalized_per_second:97,quarantined_per_second:5,bytes_per_second:4608},source:{node_id:'node-live',source:'/var/log/live.log',process_generation:'gen-live',sequence:1,phase:'tailing',counters:{nli_source_records_read_total:12,nli_parser_records_succeeded_total:10,nli_parser_records_failed_total:0,nli_normalized_records_total:10,nli_quarantined_records_total:2},rates:{read_lines_per_second:12,normalized_per_second:10,quarantined_per_second:2,bytes_per_second:512},queue:{nli_queue_depth:1,queue_capacity:20,backpressure:false},controller_received_at:new Date().toISOString(),controller_processing_ms:1,observed_propagation_delay_ms:5,freshness:'live',freshness_age_ms:5,freshness_reason:'within target',critical:false,diagnostic_codes:[]},diagnostics:{subscribers:1,tracked_sources:2,live:2,delayed:0,stale:0,disconnected:0,issues:[]},subsystems:{nhle:{status:'live',source_count:2,active_nodes:2,records_read_total:132,normalized_total:126,quarantined_total:6,queue_depth:5,read_lines_per_second:102,normalized_per_second:97},napi:{status:'live'},nal:{status:'live'}}},'8'));
  await expect(page.locator('#stream-live-table')).toContainText('node-live');
  await expect(page.locator('#stream-live-table')).toContainText('12.0');
  await page.evaluate(() => window.__streamSources.at(-1).emit('sync_delta',{schema_version:'nli.sync.v1',revision:9,generated_at:new Date().toISOString(),target_interval_ms:1000,aggregate:{nli_source_records_read_total:140,nli_parser_records_succeeded_total:134,nli_parser_records_failed_total:1,nli_normalized_records_total:134,nli_quarantined_records_total:5,nli_queue_depth:4,read_lines_per_second:110,normalized_per_second:105,quarantined_per_second:4,bytes_per_second:4796},source:{node_id:'node-live',source:'/var/log/live.log',process_generation:'gen-live',sequence:2,phase:'eof',counters:{nli_source_records_read_total:20,nli_parser_records_succeeded_total:18,nli_parser_records_failed_total:0,nli_normalized_records_total:18,nli_quarantined_records_total:1},rates:{read_lines_per_second:20,normalized_per_second:18,quarantined_per_second:1,bytes_per_second:700},queue:{nli_queue_depth:0,queue_capacity:20,backpressure:false},controller_received_at:new Date().toISOString(),controller_processing_ms:1,observed_propagation_delay_ms:5,freshness:'live',freshness_age_ms:5,freshness_reason:'within target',critical:false,diagnostic_codes:[]},diagnostics:{subscribers:1,tracked_sources:2,live:2,delayed:0,stale:0,disconnected:0,issues:[]},subsystems:{nhle:{status:'live',source_count:2,active_nodes:2,records_read_total:140,normalized_total:134,quarantined_total:5,queue_depth:4,read_lines_per_second:110,normalized_per_second:105},napi:{status:'live'},nal:{status:'live'}}},'9'));
  await expect(page.locator('#stream-live-table')).toContainText('20.0');
	await page.getByTestId('nav-operations').click();
	await expect(page.locator('.throughput-gauges')).toContainText('110.0/s');
	await page.evaluate(() => window.__streamSources.at(-1).onerror?.(new Event('error')));
	await new Promise(resolve => setTimeout(resolve, 1_100));
  expect(await page.evaluate(() => window.__streamSources.at(-1).url)).toContain('last_revision=9');
});

test('equal-revision canonical heartbeat refreshes freshness without replacing the focused workspace', async ({ page }) => {
  await mockController(page);
  await page.addInitScript(() => {
    const instances=[];
    class FakeEventSource extends EventTarget {
      constructor(url){super();this.url=url;instances.push(this);window.__streamSources=instances;setTimeout(()=>this.onopen?.(),0);}
      close(){this.closed=true;}
      emit(type,data){this.dispatchEvent(new MessageEvent(type,{data:JSON.stringify(data)}));}
    }
    window.EventSource=FakeEventSource;
  });
  const snapshotResponse=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/v1/sync/snapshot');
  await login(page);
  const snapshot=await (await snapshotResponse).json();
  await page.getByTestId('nav-operations').click();
  const range=page.locator('#dashboard-range');
  await range.focus();
  await range.evaluate(node=>{window.__heartbeatInput=node;window.__heartbeatRoot=document.querySelector('#page').firstElementChild;});
  await page.evaluate(value=>window.__streamSources.at(-1).emit('sync_heartbeat',{schema_version:value.schema_version,revision:value.revision,generated_at:new Date().toISOString(),target_interval_ms:value.target_interval_ms,aggregate:value.aggregate,sources:value.sources,diagnostics:value.diagnostics}),snapshot);
  await expect.poll(()=>page.evaluate(()=>({focused:document.activeElement===window.__heartbeatInput,connected:window.__heartbeatInput?.isConnected,root:document.querySelector('#page').firstElementChild===window.__heartbeatRoot}))).toEqual({focused:true,connected:true,root:true});
  await expect(page.locator('#freshness')).toContainText('live');
  await page.getByTestId('nav-napi-monitor').click();
  await expect(page.locator('#page')).toContainText('Pending drafts');
  await expect(page.locator('#page')).toContainText('3');
});

test('canonical updates defer a global dashboard rerender while a select is focused', async ({ page }) => {
  await mockController(page);
  await page.addInitScript(() => {
    const instances=[];
    class FakeEventSource extends EventTarget {
      constructor(url){super();this.url=url;instances.push(this);window.__streamSources=instances;setTimeout(()=>this.onopen?.(),0);}
      close(){}
      emit(type,data){this.dispatchEvent(new MessageEvent(type,{data:JSON.stringify(data)}));}
    }
    window.EventSource=FakeEventSource;
  });
  const response=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/sync/snapshot');
  await login(page);
  const snapshot=await (await response).json();
  await page.getByTestId('nav-operations').click();
  const range=page.locator('#dashboard-range');
  await range.focus();
  await range.evaluate(node=>{window.__focusedSelect=node;});
  await page.evaluate(value=>window.__streamSources.at(-1).emit('sync_delta',{schema_version:value.schema_version,revision:Number(value.revision)+1,generated_at:new Date().toISOString(),target_interval_ms:value.target_interval_ms,aggregate:{...value.aggregate,nli_queue_depth:17},source:{node_id:'node-1',source:'/var/log/mail.log',sequence:1,phase:'tailing',counters:{},rates:{},queue:{nli_queue_depth:17,queue_capacity:128},freshness:'live',freshness_age_ms:0},diagnostics:value.diagnostics,subsystems:value.subsystems}),snapshot);
  await expect.poll(()=>page.evaluate(()=>({connected:window.__focusedSelect?.isConnected,focused:document.activeElement===window.__focusedSelect}))).toEqual({connected:true,focused:true});
  await range.evaluate(node=>node.blur());
  await expect(page.locator('#dashboard-range')).toHaveCount(1);
  await expect(page.locator('.throughput-gauges')).toContainText('17');
});

test('shared snapshot recovery keeps canonical freshness live when SSE heartbeats stop', async ({ page }) => {
  await page.clock.install();
  const { observed } = await mockController(page);
  await page.addInitScript(() => {
    class QuietEventSource extends EventTarget {
      constructor(){super();setTimeout(()=>this.onopen?.(),0);}
      close(){}
    }
    window.EventSource=QuietEventSource;
  });
  await login(page);
  await page.clock.fastForward(6_100);
  await expect(page.locator('#stream-state')).toContainText('Live sync');
  await expect.poll(()=>observed.filter(item=>item==='GET /api/v1/sync/snapshot').length).toBeGreaterThan(1);
  await expect(page.locator('#freshness')).toContainText('live');
  await expect(page.locator('.stale-banner')).toHaveCount(0);
});

test('production Assurance response fills helper using normalized backend proposal and saves a draft', async ({ page }) => {
  const { observed } = await mockController(page);
  await login(page);
  await page.getByTestId('nav-assurance').click();
  await page.getByTestId('assurance-raw').fill('level=INFO user=alice   \r\nlevel=WARN user=bob\t  \r\n');
  await page.getByRole('button', { name: 'Run assurance', exact: true }).click();
  await page.getByRole('tab', { name: 'Parser Creation Helper' }).click();
  const form=page.locator('#parser-helper-form');
  await expect(form.locator('[name=samples]')).toHaveValue('level=INFO user=alice\nlevel=WARN user=bob');
  const parser=JSON.parse(await form.locator('[name=parser]').inputValue());
  expect(parser.known_fields).toEqual(['level', 'user']);
  expect(parser.header_pattern).toMatchObject({ kind: 'line', prefix: 'level=' });
  expect(parser.template).toEqual({ level: '{level}', user: '{user}' });
  await expect(page.locator('#parser-helper-overview')).toContainText('key_value');
  await form.getByRole('button', { name: 'Save review-gated draft' }).click();
  await expect.poll(()=>observed.includes('POST /api/v1/assurance/parsers')).toBe(true);
  expect(observed.some(path=>/assurance\/parsers\/.*\/(approve|publish)/.test(path))).toBe(false);
});

test('Assurance helper trims trailing whitespace, generates editable parser, validates and saves only a draft', async ({ page }) => {
  const { observed } = await mockController(page);
  await login(page);
  await page.getByTestId('nav-assurance').click();
  await expect(page.getByRole('tablist', { name: 'Assurance Lab tools' })).toBeVisible();
  const labels = await page.getByRole('tablist', { name: 'Assurance Lab tools' }).getByRole('tab').allTextContents();
  expect(labels).toEqual(['Analyse', 'Corpus reports', 'Parser drafts', 'Review Queue', 'History', 'Fixtures', 'Guided Parser Repair Studio', 'Parser Creation Helper', 'NAPI Synthesis', 'Test Plans', 'Runs']);
  await page.getByRole('tab', { name: 'Guided Parser Repair Studio' }).click();
  await expect(page.getByText(/active parser is unchanged until the existing approval and deployment flow completes/i)).toBeVisible();
  await page.getByRole('tab', { name: 'Parser Creation Helper' }).click();
  const form = page.locator('#parser-helper-form');
  await form.locator('[name=samples]').fill('level=INFO user=alice   \r\nlevel=WARN user=bob\t  \r\n');
  await page.getByRole('button', { name: 'Generate parser draft' }).click();
  await expect(form.locator('[name=samples]')).toHaveValue('level=INFO user=alice\nlevel=WARN user=bob');
  const parser = JSON.parse(await form.locator('[name=parser]').inputValue());
  expect(parser.pattern_type).toBe('regex');
  expect(parser.known_fields).toEqual(['level', 'user']);
  await expect(page.locator('#parser-helper-overview')).toContainText('key_value_regex');
  await page.getByRole('button', { name: 'Validate draft', exact: true }).click();
  await expect.poll(() => observed.includes('POST /api/v1/assurance/corpus')).toBe(true);
  await form.getByRole('button', { name: 'Save review-gated draft' }).click();
  await expect.poll(() => observed.includes('POST /api/v1/assurance/parsers')).toBe(true);
  expect(observed.some(path => /assurance\/parsers\/.*\/(approve|publish)/.test(path))).toBe(false);
});

test('quarantine diagnosis opens a truthful guided parser recovery workspace', async ({ page }) => {
  const { observed } = await mockController(page);
  await login(page);
  await page.getByTestId('nav-unknowns').click();
  await page.getByRole('button', { name: 'View evidence & diagnose' }).click();
  await expect.poll(() => observed.includes('POST /api/v1/quarantine/records/q-1/analyze')).toBe(true);
  await expect(page.getByRole('heading', { name: 'Recovery Workspace' })).toBeVisible();
  await expect(page.getByText(/Replay stays blocked until node-1 acknowledges the published bundle/)).toBeVisible();
  await expect(page.getByRole('button', { name: 'Prepare replay' })).toHaveCount(0);
  await page.getByRole('button', { name: 'Edit or test parser' }).click();
  await expect(page.locator('#gprs-parser-helper-form')).toBeVisible();
  await expect(page.locator('#gprs-parser-helper-form [name=samples]')).toHaveValue('exact raw source line');
});

test('Parser Creation Workspace builds a complete editable draft without publishing', async ({ page }) => {
  const { observed } = await mockController(page);
  await login(page);
  await page.getByTestId('nav-parser-workspace').click();
  const form = page.locator('#parser-workspace-form');
  await form.locator('[name=samples]').fill('level=INFO user=alice   \r\nlevel=WARN user=bob\t  \r\n');
  await page.getByRole('button', { name: 'Auto-populate complete parser' }).click();
  await expect(form.locator('[name=samples]')).toHaveValue('level=INFO user=alice\nlevel=WARN user=bob');
  const parser = JSON.parse(await form.locator('[name=parser]').inputValue());
  expect(parser).toMatchObject({ pattern_type: 'regex', known_fields: ['level', 'user'], projection: ['level', 'user'] });
  await page.getByRole('button', { name: 'Validate locally' }).click();
  await expect(form.locator('[data-parser-status]')).toContainText('Local structure valid');
  await page.getByRole('button', { name: 'Save approval-gated draft' }).click();
  await expect.poll(() => observed.includes('POST /api/v1/assurance/parsers')).toBe(true);
  expect(observed.some(path => /assurance\/parsers\/.*\/(approve|publish)/.test(path))).toBe(false);
});

test('operations does not fan out beyond shared canonical recovery', async ({ page }) => {
  const { observed } = await mockController(page);
  await login(page);
  const before = observed.length;
  await page.getByTestId('nav-operations').click();
  const requests = observed.slice(before);
  expect(requests.filter(item => item.startsWith('GET /api/v1/dashboard/snapshot'))).toHaveLength(0);
  expect(requests.filter(item => item === 'GET /api/v1/sync/snapshot').length).toBeLessThanOrEqual(1);
  expect(requests.filter(item => item.startsWith('GET /api/v1/metrics/timeseries')).length).toBeLessThanOrEqual(1);
  expect(requests.some(item => /\/api\/v1\/(overview|metadata|system|fleet\/sync)/.test(item))).toBe(false);
});

test('missing EventSource uses shared recovery only and signout aborts it cleanly', async ({ page }) => {
  await page.clock.install();
  const { observed } = await mockController(page);
  await page.addInitScript(() => { window.EventSource = undefined; });
  await login(page);
  await expect(page.getByTestId('nav-streams')).toBeVisible();
  await page.clock.fastForward(5_100);
  expect(observed.filter(item => item === 'GET /api/v1/telemetry/stream/snapshot')).toHaveLength(0);
  await page.getByRole('button', { name: 'Sign out' }).click();
  const afterSignout = observed.filter(item => item === 'GET /api/v1/sync/snapshot').length;
  await page.clock.fastForward(15_000);
  expect(observed.filter(item => item === 'GET /api/v1/sync/snapshot')).toHaveLength(afterSignout);
});

test('API monitor starts its independent probes concurrently and displays latency percentiles', async ({ page }) => {
  const starts = [];
  await mockController(page);
  await page.route(`${origin}/api/v1/**`, async route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/v1/auth/login' || path === '/api/v1/auth/csrf' || path === '/api/v1/auth/me' || path === '/api/v1/health' || path === '/api/v1/sync/snapshot' || path === '/api/v1/sync/stream') return route.fallback();
    starts.push(Date.now());
    await new Promise(resolve => setTimeout(resolve, 80));
    await route.fallback();
  });
  await login(page);
  starts.length = 0;
  await page.getByTestId('nav-api-monitor').click();
  await expect(page.getByRole('heading', { name: 'API monitor', exact: true })).toBeVisible();
  await expect(page.getByText('Latency p50')).toBeVisible();
  await expect(page.getByText('Latency p95')).toBeVisible();
  expect(starts.length).toBeGreaterThan(10);
  // Chromium limits parallel connections per origin. The first transport wave
  // proves that independent probes are dispatched concurrently.
  expect(Math.max(...starts.slice(0, 6)) - Math.min(...starts.slice(0, 6))).toBeLessThan(70);
});

test('Endpoint Configuration tests and saves a safe ClickHouse field-mapped profile', async ({ page }) => {
  const mock = await mockController(page);
  await login(page);
  await page.getByTestId('nav-outputs').click();
  await expect(page.getByRole('heading', { name: 'Endpoint Configuration', level: 1 })).toBeVisible();
  await expect(page.getByRole('tablist', { name: 'Endpoint configuration sections' }).getByRole('tab')).toHaveText(['Destinations', 'Security & credentials', 'Validation', 'Saved profiles', 'Live & rollback']);
  const form = page.locator('#output-profile-form');
  await expect(form.locator('[name=normalised_enabled]')).toBeVisible();
  await page.getByRole('tab', { name: 'Security & credentials' }).click();
  await expect(form.locator('[name=clickhouse_password_env]')).toBeVisible();
  await page.getByRole('tab', { name: 'Validation' }).click();
  await expect(form.locator('[name=field_columns]')).toBeVisible();
  await expect(form.getByRole('button', { name: 'Validate configuration' })).toBeVisible();
  await page.getByRole('tab', { name: 'Saved profiles' }).click();
  await expect(page.getByRole('heading', { name: 'Saved output profiles' })).toBeVisible();
  await page.getByRole('tab', { name: 'Live & rollback' }).click();
  await expect(page.getByRole('heading', { name: 'Roll out endpoint profile' })).toBeVisible();
  await expect(page.locator('#output-rollout-form')).toBeVisible();
  await page.getByRole('tab', { name: 'Destinations' }).click();
  await form.locator('[name=normalised_enabled]').uncheck();
  await form.locator('[name=destinations][value=clickhouse]').check();
  await form.locator('[name=clickhouse_url]').fill('https://clickhouse.example.com:8443');
  await page.getByRole('tab', { name: 'Validation' }).click();
  await form.locator('[name=field_columns]').fill('{"fromhost":"client_ip"}');
  await form.getByRole('button', { name: 'Validate configuration' }).click();
  await expect(page.locator('#output-config-test-result')).toContainText('Local profile validation passed');
  await expect(page.locator('#output-config-test-result')).toContainText('Network connectivity was not tested');
  await expect(page.locator('#output-profile-preview')).toContainText('normal');
  await form.getByRole('button', { name: 'Validate and save draft' }).click();
  await expect(page.locator('#output-profile-preview')).toContainText('client_ip');
  expect(mock.outputSaves).toHaveLength(1);
  const outputs = mock.outputSaves[0].config.outputs;
  expect(outputs.find(item => item.type === 'clickhouse')).toMatchObject({ url: 'https://clickhouse.example.com:8443/', field_columns: { fromhost: 'client_ip' }, fallback_path: '/var/log/nli/output/delivery-fallback.jsonl', routes: ['normal', 'warning'] });
  expect(outputs.some(item => item.type === 'file' && item.routes.includes('quarantine'))).toBe(true);
  expect(outputs.some(item => item.type === 'file' && item.routes.includes('normal'))).toBe(false);
});

test('published endpoint profile creates an outputs-only approval rollout', async ({ page }) => {
  const mock = await mockController(page);
  const profile = { id: 'remote-delivery', revision: 4, state: 'active', updated_at: '2026-10-14T00:00:00Z', payload: { name: 'Remote delivery', config: { outputs: [
    { type: 'loki', url: 'https://loki.example/loki/api/v1/push', routes: ['normal', 'warning'], fallback_path: '/var/lib/nli/spool/loki.jsonl' },
    { type: 'file', path_template: '/var/log/nli/output/auto_quarantine_{hour_utc}.jsonl', routes: ['quarantine', 'unknown'], durable_raw: true },
  ] } } };
  mock.outputProfiles.push(profile);
  await login(page);
  await page.getByTestId('nav-outputs').click();
  await page.getByRole('tab', { name: 'Live & rollback' }).click();
  const form = page.locator('#output-rollout-form');
  await form.locator('[name=profile_id]').selectOption('remote-delivery');
  await form.locator('[name=targets]').fill('node-a, node-b');
  await form.getByRole('button', { name: 'Request independent approval' }).click();
  await expect.poll(() => mock.rolloutSaves.length).toBe(1);
  expect(mock.rolloutSaves[0]).toMatchObject({ type: 'config_push', target_nodes: ['node-a', 'node-b'], payload: { config_patch: { outputs: profile.payload.config.outputs } } });
});

test('Parser Fleet Sync shows live revision, drift, rollout and approval-gated verify/retry actions', async ({ page }) => {
  const { observed } = await mockController(page);
  await login(page);
  await page.getByTestId('nav-parser-sync').click();
  await expect(page.locator('#page')).toContainText('Nginx access pattern');
  await expect(page.locator('#page')).toContainText('published');
  await expect(page.locator('#page')).toContainText('node-1');
  await expect(page.locator('#page')).toContainText('1 / 1 complete');
  await page.getByRole('button', { name: 'Verify' }).first().click();
  await expect.poll(() => observed.includes('POST /api/v1/parser-sync/verify')).toBe(true);
  await page.getByRole('button', { name: 'Propose retry' }).last().click();
  await expect.poll(() => observed.includes('POST /api/v1/parser-sync/retry')).toBe(true);
  await expect(page.locator('#toast-region')).toContainText('independent approval is required');
});

test('Quarantine queue exposes exact raw access, exports and auditable replay controls', async ({ page }) => {
  const { observed } = await mockController(page);
  await login(page);
  await page.getByTestId('nav-unknowns').click();
  await expect(page.getByRole('heading', { name: 'Quarantine and reprocessing' })).toBeVisible();
  await expect(page.getByText('exact raw source line')).toHaveCount(0);
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Download exact raw' }).click();
  expect((await download).suggestedFilename()).toBe('nli-quarantine-q-1.log');
  await page.getByRole('button', { name: 'View evidence & diagnose' }).click();
  await expect(page.getByRole('heading', { name: 'Assurance Lab' })).toBeVisible();
  await expect(page.getByTestId('assurance-raw')).toHaveValue('exact raw source line');
  await page.getByTestId('nav-unknowns').click();
  await page.getByLabel('Record IDs').fill('q-1');
  await page.getByLabel('Target client ID').fill('node-1');
  await page.getByRole('button', { name: 'Request replay' }).click();
  await expect.poll(() => observed.includes('POST /api/v1/quarantine/reprocess')).toBe(true);
  await page.getByTestId('nav-api-monitor').click();
  await expect(page.getByRole('heading', { name: 'API monitor', exact: true })).toBeVisible();
  await expect(page.getByText('/system', { exact: true })).toBeVisible();
});

test('Assurance enables clear and download immediately and preserves results across navigation', async ({ page }) => {
  await mockController(page);
  await login(page);
  await page.getByTestId('nav-assurance').click();
  const raw = '<img src=x onerror="window.injected=true"> service=test user=operator';
  await page.getByTestId('assurance-raw').fill(raw);
  await page.getByRole('button', { name: 'Run assurance', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Clear manually' })).toBeEnabled();
  await expect(page.getByRole('button', { name: 'Download result JSON' })).toBeEnabled();
  await expect(page.locator('#assurance-result')).toContainText('service=test user=operator');
  expect(await page.evaluate(() => window.injected)).toBeUndefined();
  await page.locator('#assurance-save-form [name=name]').fill('browser-retained-case');
  await page.getByRole('button', { name: 'Encrypt and retain in Supabase' }).click();
  await expect(page.getByText('browser-retained-case', { exact: true })).toBeVisible();
  await page.getByTestId('nav-fleet').click();
  await page.getByTestId('nav-assurance').click();
  await expect(page.getByTestId('assurance-raw')).toHaveValue(raw);
  await expect(page.locator('#assurance-result')).toContainText('service=test user=operator');
  const downloadEvent = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Download result JSON' }).click();
  expect((await downloadEvent).suggestedFilename()).toBe('nli-v20-assurance-result.json');
  await page.getByRole('button', { name: 'Clear manually' }).click();
  await expect(page.getByText('Run a case to inspect complete output', { exact: false })).toBeVisible();
  await expect(page.getByTestId('assurance-raw')).toHaveValue('');
});

test('Assurance parser and family catalogs drive single and corpus requests', async ({ page }) => {
  const { assuranceRequests, assuranceCorpora } = await mockController(page);
  await login(page);
  await page.getByTestId('nav-assurance').click();
  await expect(page.getByText('Live runtime parsers', { exact: true })).toBeVisible();
  const single = page.locator('#assurance-form');
  await single.locator('[name=service]').selectOption('smtp-in');
  await single.locator('[name=parser_id]').selectOption('smtp');
  await single.locator('[name=raw]').fill('level=INFO user=alice');
  await page.getByRole('button', { name: 'Run assurance', exact: true }).click();
  await expect.poll(() => assuranceRequests.length).toBe(1);
  expect(assuranceRequests[0]).toMatchObject({ service: 'smtp-in', parser_id: 'smtp' });

  await page.getByRole('tab', {name:'Corpus reports'}).click();
  const corpus = page.locator('#assurance-corpus-form');
  await corpus.locator('[name=service]').selectOption('imap');
  await corpus.locator('[name=parser_id]').selectOption('smtp');
  await corpus.locator('[name=cases]').fill('[{"raw":"level=INFO user=alice","desired":{}}]');
  await page.getByRole('button', { name: 'Run corpus', exact: true }).click();
  await expect.poll(() => assuranceCorpora.length).toBe(1);
  expect(assuranceCorpora[0].cases[0]).toMatchObject({ service: 'imap', parser_id: 'smtp' });
  await expect(page.locator('#assurance-corpus-result')).toContainText('Corpus result');
});

test('Assurance upload defaults to transient and shows protected retention metadata/download', async ({ page }) => {
  const { uploads } = await mockController(page);
  await login(page);
  await page.getByTestId('nav-assurance').click();
  const retain = page.locator('#assurance-upload-form [name=retain]');
  await expect(retain).not.toBeChecked();
  await page.locator('#assurance-upload-form [name=service]').selectOption('smtp-in');
  await page.locator('#assurance-upload-form [name=parser_id]').selectOption('smtp');
  await retain.check();
  await page.locator('#assurance-upload-form [name=file]').setInputFiles({ name: 'sample.log', mimeType: 'text/plain', buffer: Buffer.from('service=example user=operator') });
  await page.getByRole('button', { name: 'Analyze uploaded sample' }).click();
  await expect(page.locator('#assurance-upload-status')).toContainText('encrypted and retained');
  await expect(page.locator('#assurance-upload-status')).toContainText('sample.log');
  await expect(page.locator('#assurance-upload-status')).toContainText('smtp@18.0.0');
  await expect(page.locator('#assurance-upload-status')).toContainText('encrypted_supabase');
  await expect(page.locator('#assurance-upload-status')).toContainText('controller encrypted the fixture');
  expect(uploads).toHaveLength(1);
  expect(uploads[0]).toContain('name="retain"\r\n\r\ntrue');
  expect(uploads[0]).toContain('name="parser_id"\r\n\r\nsmtp');
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Download retained sample through controller' }).click();
  expect((await download).suggestedFilename()).toBe('sample.log');
});

test('live recovery avoids dashboard fan-out and leaves Assurance inputs and results intact', async ({ page }) => {
  await page.clock.install();
  const { observed } = await mockController(page);
  await login(page);
  const canonicalSnapshots = observed.filter(item => item === 'GET /api/v1/sync/snapshot').length;
  await page.clock.fastForward(31_000);
  expect(observed.filter(item => item === 'GET /api/v1/dashboard/snapshot')).toHaveLength(0);
  await expect.poll(() => observed.filter(item => item === 'GET /api/v1/sync/snapshot').length).toBeGreaterThan(canonicalSnapshots);
  await page.getByTestId('nav-assurance').click();
  await page.getByTestId('assurance-raw').fill('message=keep-me');
  await page.getByRole('button', { name: 'Run assurance', exact: true }).click();
  await expect(page.locator('#assurance-result')).toContainText('keep-me');
  await page.clock.fastForward(61_000);
  await expect(page.getByTestId('assurance-raw')).toHaveValue('message=keep-me');
  await expect(page.locator('#assurance-result')).toContainText('keep-me');
});

test('viewer permissions apply to newly rendered Assurance actions', async ({ page }) => {
  await mockController(page, { role: 'viewer' });
  await login(page);
  await page.getByTestId('nav-assurance').click();
  await page.getByTestId('assurance-raw').fill('message=viewer-test');
  await page.getByRole('button', { name: 'Run assurance', exact: true }).click();
  await expect(page.getByText('Complete actual output')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Encrypt and retain in Supabase' })).toBeHidden();
});

test('sign-out clears the last operator input and results', async ({ page }) => {
  await mockController(page);
  await login(page);
  await page.getByTestId('nav-assurance').click();
  await page.getByTestId('assurance-raw').fill('user=private-operator');
  await page.getByRole('button', { name: 'Run assurance', exact: true }).click();
  await expect(page.locator('#assurance-result')).toContainText('private-operator');
  await page.getByRole('button', { name: 'Sign out' }).click();
  await expect(page.getByLabel('Password')).toHaveValue('');
  await login(page);
  await page.getByTestId('nav-assurance').click();
  await expect(page.getByTestId('assurance-raw')).toHaveValue('');
});

test('incompatible controller keeps credentials disabled', async ({ page }) => {
  const { observed } = await mockController(page, { healthIdentity: 'v5' });
  await page.goto(origin);
  await expect(page.getByText('Controller connection required')).toBeVisible();
  await expect(page.getByLabel('Password')).toBeDisabled();
  expect(observed.filter(item => item === 'POST /api/v1/auth/login')).toEqual([]);
});

test('input paths workspace saves a revisioned family profile using the dedicated collection', async ({ page }) => {
  const { inputSaves } = await mockController(page);
  await login(page);
  await page.getByTestId('nav-input-paths').click();
  await expect(page.getByRole('heading', { name: 'Input paths and family profiles' })).toBeVisible();
  await page.locator('#input-profile-form [name=quick_path]').fill('/var/log/nli/input/pop-extra.log');
  await page.locator('#input-profile-form [name=quick_family]').selectOption('pop');
  await page.getByRole('button', { name: 'Add path with selected family' }).click();
  await expect(page.locator('#input-profile-form [name=config]')).toHaveValue(/pop-extra\.log/);
  await page.locator('#input-profile-form [name=id]').fill('mail-family-paths');
  await page.locator('#input-profile-form [name=name]').fill('Mail family paths');
  await page.locator('#input-profile-form [name=config]').fill(JSON.stringify({ inputs: [{ path: '/var/log/imap.log', service: 'imap' }, { path: '/var/log/smtp.log', service: 'smtp-in' }] }));
  await page.getByRole('button', { name: 'Save profile revision' }).click();
  await expect.poll(() => inputSaves.length).toBe(1);
  expect(inputSaves[0]).toMatchObject({ id: 'mail-family-paths', config: { inputs: [{ path: '/var/log/imap.log', service: 'imap' }, { path: '/var/log/smtp.log', service: 'smtp-in' }] } });
});

test('input path editor adds, edits, removes and validates without replacing existing entries', async ({ page }) => {
  await mockController(page);
  await login(page);
  await page.getByTestId('nav-input-paths').click();
  const form=page.locator('#input-profile-form');
  await form.locator('[name=quick_path]').fill('/var/log/custom/one.log');
  await form.locator('[name=quick_family]').selectOption('smtp-in');
  await form.getByRole('button',{name:'Add path with selected family'}).click();
  const config=async()=>JSON.parse(await form.locator('[name=config]').inputValue());
  await expect.poll(async()=> (await config()).inputs.length).toBe(11);
  expect((await config()).inputs[0].path).toBe('/var/log/nli/input/auto.log');
  expect((await config()).inputs[0].multiline.rules).not.toContain('generic_continuation');
  expect((await config()).inputs.at(-1)).toMatchObject({path:'/var/log/custom/one.log',service:'smtp-in'});
  expect((await config()).inputs.at(-1).multiline.rules).toEqual(['generic_continuation']);
  await form.locator('[data-input-family="10"]').selectOption('imap');
  expect((await config()).inputs.at(-1).service).toBe('imap');
  await form.locator('[data-input-family="10"]').selectOption('auto');
  expect((await config()).inputs.at(-1).multiline.rules).not.toContain('generic_continuation');
  await form.getByRole('button',{name:'Remove /var/log/custom/one.log'}).click();
  await expect.poll(async()=> (await config()).inputs.length).toBe(10);
  await form.getByRole('button',{name:'Validate paths'}).click();
  await expect(page.locator('#toast-region').getByText('10 input paths valid')).toBeVisible();
});

test('selecting input family leaves the open profile editor mounted for further selections', async ({ page }) => {
  await mockController(page);
  await login(page);
  await page.getByTestId('nav-input-paths').click();
  const family=page.locator('#input-profile-form [name=quick_family]');
  await family.selectOption('smtp-in');
  await expect(family).toHaveValue('smtp-in');
  await expect(page.locator('#input-profile-form')).toBeVisible();
  await family.selectOption('pop');
  await expect(family).toHaveValue('pop');
});

test('performance timeline reads persisted metrics and reports bounded exact-count buckets', async ({ page }) => {
  const mock=await mockController(page);
  await login(page);
  await page.getByTestId('nav-performance').click();
  await expect(page.getByRole('heading',{name:'Overall Timeline History'})).toBeVisible();
  await expect(page.getByText(/persisted measurements/)).toBeVisible();
  expect(mock.observed).toContain('GET /api/v1/metrics/timeseries');
});

test('input path rollout uses the dedicated inputs-only contract and explicit node IDs', async ({ page }) => {
  const mock = await mockController(page);
  const profile = { id: 'mail-inputs', revision: 2, state: 'active', updated_at: '2026-10-14T00:00:00Z', payload: { name: 'Mail inputs', config: { inputs: [{ path: '/var/log/nli/input/imap.log', service: 'imap' }] } } };
  mock.inputProfiles.push(profile);
  await login(page);
  await page.getByTestId('nav-input-paths').click();
  const form = page.locator('#input-rollout-form');
  await form.locator('[name=profile_id]').selectOption('mail-inputs');
  await form.locator('[name=targets]').fill('node-mail-1');
  await form.getByRole('button', { name: 'Request independent approval' }).click();
  await expect.poll(() => mock.inputRolloutSaves.length).toBe(1);
  expect(mock.inputRolloutSaves[0]).toEqual({ path: '/api/v1/config/input-paths/mail-inputs/rollout', body: { node_ids: ['node-mail-1'] } });
});

test('input path rollout supports select-all clients and multiple profile approvals', async ({ page }) => {
  const mock=await mockController(page);
  const base={revision:1,state:'active',payload:{config:{inputs:[{path:'/var/log/one.log',service:'imap'}]}}};
  mock.inputProfiles.push({...base,id:'profile-one',payload:{...base.payload,name:'Profile one'}});
  mock.inputProfiles.push({...base,id:'profile-two',payload:{...base.payload,name:'Profile two'}});
  await login(page);
  await page.getByTestId('nav-input-paths').click();
  await page.getByLabel('Select all 1 clients').check();
  const form=page.locator('#input-rollout-form');
  await expect(form.locator('[name=targets]')).toHaveValue('node-1');
  await form.locator('[name=profile_id]').selectOption(['profile-one','profile-two']);
  await form.getByRole('button',{name:'Request independent approval'}).click();
  await expect.poll(()=>mock.inputRolloutSaves.length).toBe(2);
  expect(mock.inputRolloutSaves.map(item=>item.path)).toEqual(['/api/v1/config/input-paths/profile-one/rollout','/api/v1/config/input-paths/profile-two/rollout']);
  expect(mock.inputRolloutSaves.every(item=>item.body.node_ids[0]==='node-1')).toBe(true);
});

test('input path rollout supports every eligible client without enumerating node IDs', async ({ page }) => {
  const mock=await mockController(page);
  mock.inputProfiles.push({id:'global-inputs',revision:3,state:'active',payload:{name:'Global inputs',config:{inputs:[{path:'/var/log/nli/input/auto.log',service:'auto'}]}}});
  await login(page);
  await page.getByTestId('nav-input-paths').click();
  const form=page.locator('#input-rollout-form');
  await form.locator('[name=profile_id]').selectOption('global-inputs');
  await form.locator('[name=all_eligible]').check();
  await expect(form.locator('[name=targets]')).toBeDisabled();
  await form.getByRole('button',{name:'Request independent approval'}).click();
  await expect.poll(()=>mock.inputRolloutSaves.length).toBe(1);
  expect(mock.inputRolloutSaves[0].body.node_ids).toEqual([]);
});

test('published input profile can be loaded and extended as a new draft revision', async ({ page }) => {
  const mock=await mockController(page);
  mock.inputProfiles.push({id:'published-inputs',revision:4,state:'active',payload:{name:'Published inputs',config:{inputs:[{path:'/var/log/existing.log',service:'auto'}]}}});
  await login(page);
  await page.getByTestId('nav-input-paths').click();
  await page.getByRole('button',{name:'Edit'}).click();
  const form=page.locator('#input-profile-form');
  await expect(form.locator('[name=id]')).toHaveValue('published-inputs');
  await form.locator('[name=quick_path]').fill('/var/log/added.log');
  await form.getByRole('button',{name:'Add path with selected family'}).click();
  expect(JSON.parse(await form.locator('[name=config]').inputValue()).inputs.map(item=>item.path)).toEqual(['/var/log/existing.log','/var/log/added.log']);
});

test('Command Center supports long presets, custom timestamps, exact count hover, JSON view and download', async ({ page }) => {
  const mock = await mockController(page);
  await login(page);
  const range=page.locator('#dashboard-range');
  await expect(range.locator('option')).toContainText(['1 hour','6 hours','12 hours','24 hours','2 days','1 week','2 weeks','4 weeks','30 days','90 days','3 months','6 months','9 months','12 months','1 year','2 years','5 years','10 years','All retained history','Custom dates']);
  await range.selectOption('all');
  await expect.poll(()=>mock.historyQueries.some(query=>query.get('range')==='all'&&query.get('step')==='auto')).toBe(true);
  await range.selectOption('custom');
  await page.locator('#dashboard-from').fill('2026-09-01T08:00');
  await page.locator('#dashboard-to').fill('2026-10-25T08:00');
  await page.getByRole('button',{name:'Apply dates'}).click();
  await expect.poll(()=>mock.historyQueries.some(query=>query.has('from')&&query.has('to')&&query.get('step')==='auto')).toBe(true);
  await expect(page.locator('[data-chart-category="Input"]').first()).toHaveAttribute('data-chart-value','9007199254740993');
  const point=page.locator('[data-chart-category="Input"]').first();
  await point.focus();
  await expect(page.locator('.chart-hover-readout').first()).toContainText('9,007,199,254,740,993');
  await page.getByText('View returned JSON').click();
  await expect(page.locator('.history-json pre')).toContainText('9007199254740993');
  const download=page.waitForEvent('download');
  await page.getByRole('button',{name:'Download JSON'}).click();
  expect((await download).suggestedFilename()).toBe('nli-command-center-custom.json');
});

test('Command Center refreshes retained counts in place while keeping the selected range mounted', async ({ page }) => {
  await page.clock.install();
  const mock=await mockController(page);
  await login(page);
  const range=page.locator('#dashboard-range');
  await expect(page.locator('[data-chart-category="Input"]').first()).toHaveAttribute('data-chart-value','9007199254740993');
  await page.clock.fastForward(5000);
  await expect(page.locator('[data-chart-category="Input"]').first()).toHaveAttribute('data-chart-value','42');
  await expect(range).toHaveValue('24h');
  expect(mock.historyQueries.filter(query=>query.get('range')==='24h')).toHaveLength(2);
});

test('pending approval nav badge follows controller state and clears after an action', async ({ page }) => {
  const mock=await mockController(page);
  mock.rolloutProposals.push({id:'approval-1',state:'pending_approval',created_by:'author',payload:{name:'Input rollout',type:'config_push'}});
  await login(page);
  await expect(page.locator('#approval-badge')).toHaveText('1');
  await page.getByTestId('nav-rollouts').click();
  await page.getByRole('button',{name:'Approve'}).click();
  await expect(page.locator('#approval-badge')).toBeHidden();
  await expect(page.locator('#page').getByText('approved', { exact: false }).first()).toBeVisible();
});

test('canonical navigation counts expose current parser, quarantine and configuration work without notification history', async ({ page }) => {
  await mockController(page, { navigationCounts: { parsers:{total:3,states:{draft:2,in_review:1}},'parser-workspace':{total:3,states:{draft:2,in_review:1}},'input-paths':{total:2,states:{draft:1,approved:1}},notifications:{total:88} } });
  await login(page);
  await expect(page.locator('#nav-count-parsers')).toHaveText('3');
  await expect(page.locator('#nav-count-parser-workspace')).toHaveText('3');
  await expect(page.locator('#nav-count-input-paths')).toHaveText('2');
  await expect(page.locator('#nav-count-quarantine-wizard')).toHaveText('1');
  await expect(page.locator('[data-nav-count="notifications"]')).toHaveCount(0);
  await expect(page.locator('#nav-count-kv')).toBeHidden();
});

test('Parser Catalog isolates a blocked legacy parser and offers an explicit repair draft transition', async ({ page }) => {
  const mock=await mockController(page,{managedParsers:[{id:'legacy-broken',revision:9,state:'published',created_by:'legacy-import',payload:{name:'Legacy broken',version:'0.9.0',priority:10,rules:[]},runtime_sync:{status:'blocked',reason:'legacy parser rules are incompatible with the current runtime'}}]});
  await login(page);
  await page.getByTestId('nav-parsers').click();
  await expect(page.locator('#page')).toContainText('legacy parser rules are incompatible');
  await expect(page.getByRole('button',{name:'Repair as draft'})).toBeVisible();
  await page.getByRole('button',{name:'Repair as draft'}).click();
  await expect.poll(()=>mock.observed).toContain('POST /api/v1/parser-packs/legacy-broken/repair');
  await expect(page.locator('#page')).toContainText('not_published');
});

test('draft input profiles expose lifecycle actions and JSON without a dead rollout button', async ({ page }) => {
  const mock=await mockController(page);
  mock.inputProfiles.push({id:'draft-inputs',revision:1,state:'draft',payload:{name:'Draft inputs',config:{inputs:[{path:'/var/log/draft.log',service:'imap'}]}}});
  await login(page);
  await page.getByTestId('nav-input-paths').click();
  const rollout=page.locator('#input-rollout-form');
  await expect(rollout.getByRole('button',{name:'Request independent approval'})).toBeEnabled();
  await expect(page.getByRole('button',{name:'Submit for review'})).toBeVisible();
  await page.getByRole('button',{name:'View / download JSON'}).click();
  await expect(page.locator('#input-profile-detail')).toContainText('/var/log/draft.log');
  const download=page.waitForEvent('download');
  await page.locator('#input-profile-detail').getByRole('button',{name:'Download JSON',exact:true}).click();
  expect((await download).suggestedFilename()).toBe('draft-inputs-input-profile.json');
  await page.getByRole('button',{name:'Submit for review'}).click();
  await expect(page.getByRole('button',{name:'Approve',exact:true})).toBeVisible();
  await page.getByRole('button',{name:'Approve',exact:true}).click();
  await expect(page.getByRole('button',{name:'Publish',exact:true})).toBeVisible();
  await page.getByRole('button',{name:'Publish',exact:true}).click();
  await expect(rollout.locator('[name=profile_id]')).toContainText('Draft inputs · r1');
});

test('mobile navigation exposes all workspaces without horizontal page overflow', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mockController(page);
  await login(page);
  for (const tab of tabs) {
    await page.getByLabel('Toggle navigation').click();
    await page.getByTestId(`nav-${tab}`).click();
    await expect(page.locator('.loading-state')).toHaveCount(0);
    await expect(page.locator('.error-state')).toHaveCount(0);
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
