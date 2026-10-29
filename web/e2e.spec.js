import { test, expect } from '@playwright/test';
import { readFile } from 'node:fs/promises';

const tabs = [
  'operations','monitoring','streams','sync-fabric','nhle-monitor','napi-monitor','nal-monitor','assurance','api-monitor','field-integrity','fleet','parser-sync','clients','controller','parsers','migration','input-paths','kv','static',
  'projection','studio','regex','knownkeys','semantic','engine-network','engine-url','engine-email','engine-subject','outputs','metadata','unknowns','drift','performance','rollouts','artifacts','security','system','ai',
];
const liveWorkspaceContracts = {
  operations: { paths: ['/api/v1/sync/snapshot'], text: 'Command Center' },
  monitoring: { paths: [], text: 'NLI Monitoring' },
  assurance: { paths: ['/api/v1/assurance/cases','/api/v1/assurance/runs'], text: 'Production parser assurance' },
  streams: { paths: [], text: 'Streams' },
  'sync-fabric': { paths: [], text: 'Unified Real-Time Synchronization Fabric' },
  'nhle-monitor': { paths: [], text: 'Nexora Hypersonic Log Engine' },
  'napi-monitor': { paths: [], text: 'Nexora Adaptive Parser Intelligence' },
  'nal-monitor': { paths: [], text: 'Nexora Assurance Lab' },
  'field-integrity': { paths: ['/api/v1/verification/sample-corpus'], text: 'Family field-accounting proof' },
  fleet: { paths: ['/api/v1/fleet/sync'], text: 'Fleet desired vs active' },
  'parser-sync': { paths: ['/api/v1/parser-sync'], text: 'Approved desired revision / hash' },
  clients: { paths: ['/api/v1/fleet/sync'], text: 'Client agent inventory' },
  controller: { paths: ['/api/v1/system','/api/v1/overview','/api/v1/telemetry/stats'], text: 'Controller agent status' },
  parsers: { paths: ['/api/v1/parser-packs', '/api/v1/parsers'], text: 'Built-in runtime parsers' },
  migration: { paths: ['/api/v1/migration/reports'], text: 'Safe parser-source import' },
  'input-paths': { paths: ['/api/v1/config/input-paths'], text: 'Input paths and family profiles' },
  kv: { paths: ['/api/v1/config/kv'], text: 'KV delimiter policy' },
  static: { paths: ['/api/v1/config/static-patterns'], text: 'Static positional pattern' },
  projection: { paths: ['/api/v1/config/projections'], text: 'Field capture and projection' },
  studio: { paths: ['/api/v1/config/templates'], text: 'Regex and template definition' },
  regex: { paths: ['/api/v1/config/regex'], text: 'Named regular expression pattern' },
  knownkeys: { paths: ['/api/v1/config/known-keys'], text: 'Known key catalog' },
  semantic: { paths: ['/api/v1/config/semantic-extraction'], text: 'Semantic extraction policy' },
  'engine-network': { paths: ['/api/v1/config/semantic-extraction','/api/v1/parsers'], text: 'Network Identity Engine' },
  'engine-url': { paths: ['/api/v1/config/semantic-extraction','/api/v1/parsers'], text: 'URL Engine' },
  'engine-email': { paths: ['/api/v1/config/semantic-extraction','/api/v1/parsers'], text: 'Email Address Engine' },
  'engine-subject': { paths: ['/api/v1/config/semantic-extraction','/api/v1/parsers'], text: 'Encoded Subject Engine' },
  outputs: { paths: ['/api/v1/config/output-destinations'], text: 'Endpoint Configuration' },
  metadata: { paths: ['/api/v1/metadata'], text: 'Live metadata stream' },
  unknowns: { paths: ['/api/v1/quarantine/summary','/api/v1/quarantine/records','/api/v1/quarantine/history'], text: 'Quarantine and reprocessing' },
  'api-monitor': { paths: ['/api/v1/health','/api/v1/telemetry/stats','/api/v1/parser-sync'], text: 'API monitor' },
  drift: { paths: ['/api/v1/parser-sync','/api/v1/assurance/history'], text: 'Template and parser drift' },
  performance: { paths: [], text: 'Canonical throughput and latency' },
  rollouts: { paths: ['/api/v1/rollouts'], text: 'Live controller rollouts' },
  artifacts: { paths: ['/api/v1/artifacts'], text: 'Signed immutable artifacts' },
  security: { paths: ['/api/v1/rbac/roles', '/api/v1/users', '/api/v1/audit'], text: 'Immutable audit chain' },
  system: { paths: ['/api/v1/system'], text: 'Storage adapter' },
  ai: { paths: ['/api/v1/admin/ai-provider'], text: 'AI API key integration' },
};
const adminUser = process.env.NLI_ADMIN_USER || 'admin';
const adminPassword = process.env.NLI_ADMIN_PASSWORD;

function requireCredentials() {
  if (!adminPassword) throw new Error('NLI_ADMIN_PASSWORD is required; browser tests never use or ship a fallback password');
}

async function openLogin(page) {
  const css = page.waitForResponse(response => new URL(response.url()).pathname.endsWith('/styles.css'));
  const script = page.waitForResponse(response => new URL(response.url()).pathname.endsWith('/app.js'));
  const documentResponse = await page.goto('/');
  const [cssResponse, scriptResponse] = await Promise.all([css, script]);
  expect(documentResponse?.status()).toBe(200);
  expect(cssResponse.status()).toBe(200);
  expect(cssResponse.headers()['content-type']).toContain('text/css');
  expect(scriptResponse.status()).toBe(200);
  expect(scriptResponse.headers()['content-type']).toMatch(/javascript|ecmascript/);
  await expect(page.locator('.auth-shell')).toBeVisible();
  const presentation = await page.locator('.auth-shell').evaluate(node => ({
    radius: getComputedStyle(node).borderRadius,
    sheets: document.styleSheets.length,
    background: getComputedStyle(document.body).backgroundColor,
  }));
  expect(presentation.radius).not.toBe('0px');
  expect(presentation.sheets).toBeGreaterThan(0);
  expect(presentation.background).not.toBe('rgba(0, 0, 0, 0)');
}

async function login(page) {
  requireCredentials();
  await openLogin(page);
  await expect(page.getByRole('button', { name: 'Sign in securely' })).toBeEnabled({ timeout: 15_000 });
  await page.getByLabel('Username').fill(adminUser);
  await page.getByLabel('Password').fill(adminPassword);
  const authenticated = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/auth/me' && response.status() === 200);
  await page.getByRole('button', { name: 'Sign in securely' }).click();
  await authenticated;
  await expect(page.getByTestId('nav-operations')).toBeVisible();
  await expect(page.locator('#identity-role')).toHaveText('admin');
}

test.beforeAll(requireCredentials);

test('premium login loads runtime assets and rejects invalid credentials', async ({ page }) => {
  let releaseAnonymousSession;
  const anonymousSessionGate = new Promise(resolve => { releaseAnonymousSession = resolve; });
  let heldAnonymousSession = false;
  await page.route('**/api/v1/auth/me', async route => {
    if (!heldAnonymousSession && route.request().method() === 'GET') {
      heldAnonymousSession = true;
      await anonymousSessionGate;
    }
    await route.continue();
  });
  await openLogin(page);
  await expect(page.getByRole('button', { name: 'Sign in securely' })).toBeEnabled();
  await page.getByLabel('Username').fill(adminUser);
  await page.getByLabel('Password').fill(`${adminPassword}-invalid`);
  const anonymousResponse = page.waitForResponse(response =>
    new URL(response.url()).pathname === '/api/v1/auth/me' && response.status() === 401
  );
  releaseAnonymousSession();
  await anonymousResponse;
  await expect(page.getByLabel('Password')).toHaveValue(`${adminPassword}-invalid`);
  await page.getByRole('button', { name: 'Sign in securely' }).click();
  await expect(page.locator('#login-error')).toHaveText('invalid credentials');
  await page.getByLabel('Password').fill(adminPassword);
  await page.getByRole('button', { name: 'Sign in securely' }).click();
  await expect(page.getByTestId('nav-operations')).toBeVisible();
});

test('V20 blank-panel regressions: every workspace renders authenticated controller responses', async ({ page }) => {
  // This single regression walks every authenticated workspace and validates
  // each API contract.  Give the packaged two-node gate enough time on a
  // cold, resource-constrained CI/Docker host without weakening per-action
  // assertions.
  test.setTimeout(90_000);
  await login(page);
  const failedAPIs = [];
  const successfulAPIs = new Set();
  page.on('response', response => {
    const path = new URL(response.url()).pathname;
    if (path.startsWith('/api/v1/') && response.status() >= 400) failedAPIs.push(`${response.status()} ${path}`);
    if (path.startsWith('/api/v1/') && response.ok()) successfulAPIs.add(path);
  });
  // Authentication bootstraps canonical sync before the response listener is
  // registered. Fetch it once here so this test explicitly proves the V20
  // source-of-truth contract instead of requiring the legacy dashboard poll.
  const syncStatus = await page.evaluate(async () => {
    const response = await fetch('/api/v1/sync/snapshot', { credentials: 'same-origin' });
    await response.json();
    return response.status;
  });
  expect(syncStatus).toBe(200);
  for (const tab of tabs) {
    const contract = liveWorkspaceContracts[tab];
    await page.getByTestId(`nav-${tab}`).click();
    await expect(page.locator('#page-title')).not.toHaveText('');
    await expect(page.locator('.loading-state')).toHaveCount(0);
    await expect(page.locator('.error-state')).toHaveCount(0);
    // Workspaces may render a panel/grid/split root or a composed dashboard
    // made from several direct children. The regression contract is that the
    // authenticated workspace renders visible content, independent of layout.
    await expect(page.locator('#page > *').first()).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('#page')).toContainText(contract.text);
    await expect(page.locator('#page')).not.toContainText('undefined');
    if(tab==='assurance') await expect(page.getByRole('tab',{name:'Runs',exact:true})).toBeVisible();
    await expect.poll(() => contract.paths.every(path => successfulAPIs.has(path)), { message: `${tab} did not complete ${contract.paths.filter(path => !successfulAPIs.has(path)).join(', ')}` }).toBe(true);
    expect((await page.locator('#page').innerText()).trim().length).toBeGreaterThan(100);
  }
  await expect(page.getByTestId('nav-parsers')).toBeVisible();
  await page.getByTestId('nav-parsers').click();
  await expect(page.getByText('smtp.postfix', { exact: true }).first()).toBeVisible();
  await page.getByTestId('nav-migration').click();
  await expect(page.locator('#migration-form')).toBeVisible();
  await page.getByTestId('nav-fleet').click();
  await expect(page.getByText('Fleet desired vs active')).toBeVisible();
  expect(failedAPIs).toEqual([]);
});

test('legacy Assurance regressions: complete valid JSON, traversal, manual retention, exact log download and purge', async ({ page }) => {
  test.setTimeout(90_000);
  await login(page);
  await page.getByTestId('nav-assurance').click();
  const raw = '20260822 050048557+0200 severrgo001 smtp 230675 0 140347436861184 Note;SieveRuleMsg(2005/1) id=6A291BF5048F23DE:policy=outbound:checkpoint=AUTH:rule=INOUT_action:msg=PolicyMsgInfo:eventType=PolicyMsgInfo:action=disconnect:reason=badpassword:fromhost=11.22.33.44:geo=IL:helo=Site6:auth_ok=false:auth_user=managers999@012.meg.in:auth_fail=6:auth_tries=7:score=0';
  await page.getByTestId('assurance-raw').fill(raw);
  await page.locator('[name=desired]').fill('{}');
  const runResponsePromise = page.waitForResponse(response =>
    new URL(response.url()).pathname.endsWith('/api/v1/assurance/run') &&
    response.request().method() === 'POST'
  );
  await page.getByRole('button', { name: 'Run assurance' }).click();
  const runResponse = await runResponsePromise;
  expect(runResponse.status()).toBe(200);
  const runResult = await runResponse.json();
  expect(JSON.parse(JSON.stringify(runResult.actual))).toMatchObject({
    date: '20260822',
    time: '050048557+0200',
    hostname: 'severrgo001',
    service: 'smtp',
    major_pid: '230675',
    pid_status: 0,
    minor_pid: '140347436861184',
    event_type: 'Note',
    event: 'SieveRuleMsg',
    code: '2005/1',
    action: 'disconnect',
    auth_fail: 6,
    auth_tries: 7,
    score: 0,
  });
  expect(Object.keys(runResult.actual)).toHaveLength(26);
  expect(runResult.trace.some(step => step.stage_type === 'static_layout' && step.outcome === 'success')).toBe(true);
  expect(runResult.trace.some(step => step.stage === 'parser_attempt')).toBe(true);
  expect(runResult.evidence_spans.some(span => span.field === 'date' && span.source_backed)).toBe(true);
  await expect(page.getByText('Assurance verdict')).toBeVisible();
  await expect(page.getByText('Complete actual output')).toBeVisible();
  await expect(page.locator('#assurance-result')).toContainText('20260822');
  await expect(page.locator('#assurance-result')).toContainText('severrgo001');
  await expect(page.locator('#assurance-result')).toContainText('SieveRuleMsg');
  await expect(page.getByText('Parser traversal dashboard')).toBeVisible();
  await expect(page.getByText('Field mapping and source evidence')).toBeVisible();
  const fixtureName = `smtp-browser-fixture-${Date.now()}`;
  await page.locator('#assurance-save-form [name=name]').fill(fixtureName);
  const saveResponsePromise = page.waitForResponse(response =>
    new URL(response.url()).pathname.endsWith('/api/v1/assurance/cases') &&
    response.request().method() === 'POST'
  );
  await page.getByRole('button', { name: 'Encrypt and retain in Supabase' }).click();
  const saveResponse = await saveResponsePromise;
  expect(saveResponse.status()).toBe(201);
  await expect(page.getByText(fixtureName, { exact: true })).toBeVisible({ timeout: 15_000 });
  const downloadPromise = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Download .log' }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toMatch(/\.log$/);
  expect(await readFile(await download.path(), 'utf8')).toBe(`${raw}\n`);
  await page.getByRole('tab', { name: 'Analyse', exact: true }).click();
  await page.getByRole('button', { name: 'Clear manually' }).click();
  await expect(page.getByText('Run a case to inspect complete output')).toBeVisible();
  await page.getByRole('tab', { name: 'Fixtures' }).click();
  await expect(page.getByText(fixtureName, { exact: true })).toBeVisible();
  page.once('dialog', dialog => dialog.accept());
  const purgeResponsePromise = page.waitForResponse(response =>
    new URL(response.url()).pathname.includes('/api/v1/assurance/cases/') &&
    response.request().method() === 'DELETE'
  );
  await page.getByRole('button', { name: 'Purge' }).click();
  expect((await purgeResponsePromise).status()).toBe(200);
  await expect(page.getByText(fixtureName, { exact: true })).toHaveCount(0);
});

test('administrator can create a least-privilege user', async ({ page }) => {
  await login(page);
  await page.getByTestId('nav-security').click();
  const username = `demo-viewer-${Date.now()}`;
  const form = page.locator('#user-form');
  await expect(form).toBeVisible();
  await form.locator('[name=username]').fill(username);
  await form.locator('[name=email]').fill(`${username}@example.test`);
  await form.locator('[name=role]').selectOption('viewer');
  await form.locator('[name=password]').fill('Demo-viewer-password-2026!');
  await form.getByRole('button', { name: 'Create user' }).click();
  await expect(page.getByText(username, { exact: true })).toBeVisible();
});

test('narrow viewport keeps every tab reachable', async ({ page }) => {
  test.setTimeout(60_000);
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  for (const tab of tabs) {
    await page.getByLabel('Toggle navigation').click();
    await expect(page.getByTestId(`nav-${tab}`)).toBeVisible();
    await page.getByTestId(`nav-${tab}`).click();
    await expect(page.locator('.error-state')).toHaveCount(0);
  }
});


test('credentials never POST while controller verification is pending or false', async ({ page }) => {
  let loginPosts = 0;
  page.on('request', request => {
    if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/v1/auth/login') loginPosts += 1;
  });
  await page.route('**/api/v1/health', async route => {
    await new Promise(resolve => setTimeout(resolve, 1200));
    await route.continue();
  });
  await page.goto('/');
  await expect(page.getByRole('button', { name: 'Checking controller…' })).toBeDisabled();
  await expect(page.getByLabel('Username')).toBeDisabled();
  await page.locator('#login-form').evaluate(form => form.requestSubmit());
  await page.waitForTimeout(150);
  expect(loginPosts).toBe(0);
  await expect(page.getByRole('button', { name: 'Sign in securely' })).toBeEnabled({ timeout: 5000 });

  await page.unroute('**/api/v1/health');
  await page.route('**/api/v1/health', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ status: 'ok', service: 'not-nli', api_version: 'v1' }),
  }));
  await page.reload();
  await expect(page.getByText('Controller connection required')).toBeVisible();
  await page.locator('#login-form').evaluate(form => form.requestSubmit());
  await page.waitForTimeout(100);
  expect(loginPosts).toBe(0);
});
