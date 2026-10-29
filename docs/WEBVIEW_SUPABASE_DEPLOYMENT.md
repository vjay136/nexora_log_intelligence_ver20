# NLI v20 WebView, Supabase, and fleet setup

## Supported topology

- Controller: Rocky Linux at `<controller-private-ip>`, TCP `12382`, private-CA TLS.
- Clients: separate Rocky Linux nodes using single-use enrollment bundles.
- WebView: served by the controller and connected to its authenticated same-origin API.
- Supabase: revisioned control-plane metadata plus two private, explicit file workflows: encrypted Assurance Lab fixtures and approved Rollout RPM/JSON configuration files. Normal production raw logs, parsed event payloads, metrics, and client spools are never retained there.

## 1. Install and configure the controller

```bash
sudo dnf upgrade ./nli_controller_agent_ver20.rpm
sudo nli-controller-setup
```

Answer the prompts as follows:

1. Deployment mode: `2`
2. Initial admin password: choose at least 12 characters
3. Controller DNS name or private IP: `<controller-private-ip>`
4. Supabase project URL: `https://<project-ref>.supabase.co`
5. Supabase publishable/public key: press Enter to retain the packaged project key, or paste the current key
6. Supabase server-only secret key: paste the project's `sb_secret_...` key for private Assurance and Rollout file storage

The helper generates these locally without printing them:

- private CA and a server certificate containing IP SAN `<controller-private-ip>`
- single-use client enrollment token
- 32-byte Assurance encryption key
- controller-only Supabase RPC secret
- root-owned `SUPABASE_SECRET_KEY` for the two private Storage buckets
- `/root/nli-supabase-bootstrap.sql` containing only the controller secret's SHA-256 digest

### One-time Supabase SQL step

A public anon key cannot create database objects. If setup says the NLI schema is not initialized:

1. Open the project in Supabase Dashboard.
2. Open **SQL Editor** and create a new query.
3. On Rocky, display `/root/nli-supabase-bootstrap.sql` to the authorized operator:

   ```bash
   sudo cat /root/nli-supabase-bootstrap.sql
   ```

4. Paste the complete SQL into the editor and run it once.
5. Return to the waiting Rocky prompt and press Enter.

If the original setup ran without a TTY and exited after generating SQL, apply it and then run:

```bash
sudo nli-supabase-setup
```

Expected result:

```text
PASS: Supabase protected RPC storage is healthy
PASS: controller is active with encrypted manual Assurance retention
```

The controller uses the current `sb_secret_...` server-side key for private
Storage; a legacy `service_role` JWT is not requested. Direct access to
`nli_documents` and `nli_controller_settings` is denied to `anon` and
`authenticated`; only the protected NLI RPCs are executable. Never place the
secret key in Vercel, browser JavaScript, a `NEXT_PUBLIC_` variable, or a client.

## 2. Firewall and controller checks

Allow TCP `12382` only from managed client and administrator networks. Then verify on the controller:

```bash
sudo systemctl --no-pager --full status nli-controller-agent
curl --cacert /var/lib/nli-controller/tls/ca.crt \
  --resolve <controller-private-ip>:12382:127.0.0.1 \
  https://<controller-private-ip>:12382/api/v1/health
```

The JSON must identify `nli-controller-agent` and API `v1`.

## 3. Enroll one client

The controller creates `/root/nli-client-bootstrap.env`, mode `0600`, valid for exactly one successful registration. Copy it securely to one client, then run:

```bash
sudo dnf upgrade ./nli_client_ver20.rpm
sudo nli-client-setup /path/to/nli-client-bootstrap.env
```

The client helper verifies TLS and controller identity, enrolls, protects its node token, provisions the configured local input, starts `nli-agent` and `nli-client-control`, sends a heartbeat, and removes its local bootstrap copy.

For every additional client, first generate a new token on the controller:

```bash
sudo nli-controller-setup --rotate-enrollment
```

Then copy the refreshed bootstrap to only that client.

## 4. Open and test the WebView

Trust `/var/lib/nli-controller/tls/ca.crt` on the administrator workstation and open:

`https://<controller-private-ip>:12382/`

Sign in with username `admin` and the password chosen during controller setup. If it is unknown:

```bash
sudo nli-admin-reset
```

An extracted `webview_ver20.zip` is not an API server. For SSH-tunnel access, extract it and run `Open-NLI-WebView.ps1`; the launcher enables credentials only after the NLI health and login preflights pass.

## 5. What each formerly empty tab requires

- **Fleet & Sync:** displays a node after `nli-client-setup` completes and the first authenticated heartbeat arrives. An empty fleet now shows the exact onboarding command instead of a blank screen.
- **Parser Catalog & Priority:** built-in runtime parsers always render from the controller. Managed Supabase parser revisions render independently; a storage warning no longer hides built-ins.
- **Migration Center:** the import form remains available when there are no reports. Submitted compatibility reports and optional parser drafts persist in Supabase.
- **Assurance Lab:** uses the production ParserPack adapter. The SMTP static prefix and KV tail are both included, with ordered attempt/stage telemetry and field source spans.
- **Parser Fleet Sync:** compares the published desired asset bundle with the
  controller and every enrolled client's reported applied revision and SHA-256.
  It covers regex, patterns, static layouts, parser logic, templates, field
  mappings, and known-key mappings. Web edits persist to Supabase as drafts;
  only an independently approved and published revision starts automatic
  rollout. Drift, offline nodes, failed validation, retry state, and last
  heartbeat remain visible until the fleet converges.

## 6. Assurance retention and downloads

A parse-only run uses `{}` for desired JSON and passes when the production parser emits fields and routes normally. Results remain until **Clear manually** is selected.

To retain a sample, enter a fixture name and select **Encrypt and retain in Supabase**. The controller encrypts raw/desired/actual content with AES-GCM before writing an immutable object to the private `nli-assurance-fixtures` bucket; only its bounded descriptor is stored as metadata. Retained fixtures do not expire automatically in this release; use **Purge** for explicit deletion. **Download .log** streams and decrypts one fixture only after NLI authentication and RBAC checks. A sample uploaded without the retention option remains transient.

The private `nli-rollout-artifacts` bucket accepts only signed, hash-verified
RPM and strict JSON configuration files approved for Rollouts. Parser
definitions stay in the revisioned metadata workflow. A separate private
`nli-geoip-databases` bucket accepts only SHA-256-addressed GeoLite2 MMDB
objects up to 128 MiB. GeoIP uploads require explicit license acknowledgment
and are saved as candidates pending independent approval; clients never get
the Supabase Storage credential. No normal production log, parsed event,
metric, quarantine record, or spool file is eligible for these buckets.

## 7. Supabase diagnostics

On Rocky, never print secret values. Check only names and health:

```bash
sudo grep -E '^(SUPABASE_URL|SUPABASE_PUBLISHABLE_KEY|NLI_SUPABASE_CONTROLLER_SECRET|SUPABASE_SECRET_KEY)=' \
  /etc/nli/controller.env | sed 's/=.*$/=<configured>/'
sudo nli-supabase-setup
```

In the WebView, **System Health & Storage** must show:

```json
{"mode":"supabase","status":"ok"}
```

If the RPC returns `NLI controller authentication failed`, rerun `/root/nli-supabase-bootstrap.sql` from the same controller installation so its stored digest matches `/etc/nli/controller.env`.

## 8. Vercel boundary

Vercel still needs a real public HTTPS NLI controller origin. Neither `<controller-private-ip>` nor the Supabase project URL is a Vercel controller origin. For private operation, use direct private TLS or SSH tunneling and do not deploy the WebView proxy publicly.
