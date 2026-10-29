# NLI v20 easy installation and configuration

## V20 automatic enterprise installation

For the smallest safe prompt surface on corporate, AWS, VMware, or ordinary
Linux nodes, use `sudo bash install-controller-ver20.sh --automatic`. This
generates unique controller credentials on the node, preserves existing
credentials and audit-chain keys during upgrades, and discovers the host short
name, FQDN, non-loopback addresses, and (through bounded IMDSv2 requests when
available) AWS private/public IPv4 and DNS identities. The generated private
TLS certificate covers every validated discovered identity. No universal
password or token is embedded in the RPM.

Secret values are deliberately not printed. Their authoritative recovery copy
is `/root/nli-controller-credentials.env` (`root:root`, mode `0600`). Copy only
`/root/nli-client-bootstrap.env` to a client; it contains the controller URL,
private CA when needed, and a single-use enrollment token. Generate a new
one-use bundle for each additional client with
`sudo nli-controller-setup --rotate-enrollment`.

Setup distinguishes a failed service, a missing TCP `12382` listener, a TLS
identity failure, and a host-firewall failure. On AWS, the EC2 security group
must separately allow inbound TCP `12382` from the client security group or
private client CIDR. Validate from a client with
`nc -vz CONTROLLER_IDENTITY 12382`.

Install a client and create all ten standard inputs, current-hour
normalised/quarantine files, and reviewed parser-family mappings without parser
prompts:

```bash
sudo bash install-client-ver20.sh \
  --bootstrap ./nli-client-bootstrap.env \
  --all-log-files
```

Existing logs are preserved. Missing files are created as `nli:nli` mode
`0640`, and every bundled ParserPack is self-tested before services start.

These scripts support Rocky Linux 9 and Rocky Linux 10 on x86_64. Download only
the versioned `nli-v20.0.0-3-x86_64.tar.gz` archive and its adjacent `.sha256`
file from the same GitHub release. Never copy loose release files into an old
`~/nli-ver20` directory. Extract each release into a new versioned directory:

```bash
mkdir -p ~/nli/releases
cd ~/nli/releases
sha256sum -c nli-v20.0.0-3-x86_64.tar.gz.sha256
tar -xzf nli-v20.0.0-3-x86_64.tar.gz
cd nli-v20.0.0-3-x86_64
cat RELEASE_ID
sha256sum -c SHA256SUMS
```

Do not add `--ignore-missing`. Both installers repeat complete bundle checksum
verification and require the RPM release to match `RELEASE_ID` exactly before
running `dnf`. This prevents files from two releases being mixed.

## Main controller

Run:

```bash
sudo bash install-controller-ver20.sh --verify-only
sudo bash install-controller-ver20.sh
```

To go straight to a specific setup mode after package verification, pass
`--mode 3` for Nginx ACME, `--mode 9` for Apache ACME, or `--mode 10` for the
temporary no-domain Quick Tunnel test. `--quickstart` remains the private
offline-TLS setup and cannot be combined with `--mode`.

For an isolated corporate network, choose deployment mode **7**. It creates a
private CA and controller certificate locally, includes the public CA in the
one-use client bootstrap file, and requires no public DNS, ACME service,
Cloudflare account, or corporate-issued certificate. Its default metadata
storage is local, so first boot does not wait for Internet or Supabase. Clients
must still be able to route to the controller and TCP `12382` must be allowed
from their subnet. This mode does not make a private controller reachable from
the public Internet or from Vercel; use an approved public HTTPS edge or VPN for
that topology.

Deployment mode **8** first checks only server certificate locations under
`/etc/pki/tls/certs`, `/etc/nginx`, and `/etc/nli`, and private-key locations
under `/etc/pki/tls/private`, `/etc/nginx`, and `/etc/nli`. It uses a pair only
when the leaf certificate is current, is not a CA, is valid for SSL server use,
matches the selected DNS name or IP, chains to the controller's system trust,
and matches an unencrypted private key. Katello trust anchors and consumer or
entitlement client certificates are not scanned. If no valid pair is found,
mode 8 automatically performs mode 7's offline private-CA setup. For the
prompt-free path, run `sudo bash install-controller-ver20.sh --quickstart`; it
selects an available controller identity and uses the same certificate scan
with the safe private-CA fallback.

Mode 2 is the same generated private-CA flow when operators want to choose the
metadata backend explicitly. Select Supabase storage when the controller has
outbound access and the supplied credentials are ready.

The setup asks for:

1. Deployment mode: SSH tunnel, private-network TLS, automated Nginx/ACME, Cloudflare Tunnel, an operator-managed HTTPS proxy, a corporate-PKI certificate, offline private TLS, automatic server-certificate discovery with offline fallback, automated Apache/ACME, or a temporary Cloudflare Quick Tunnel test.
2. Initial NLI administrator password on a new controller.
3. Controller DNS name or private IP for TLS mode, plus any additional stable
   DNS aliases/IP addresses used by clients, operators, or reverse proxies.
   Public modes 3–5 ask for their public hostname instead.
4. Supabase project URL and publishable key when Supabase metadata is selected.
5. The server-only Supabase secret key (`sb_secret_...`) used only by the
   controller for private Assurance fixture and approved Rollout RPM/JSON
   configuration storage. Retrieve it from the Supabase Dashboard API Keys
   page; never use a publishable key at this prompt.
6. Private-network TLS modes automatically open TCP port `12382` when firewalld
   is active. If it is inactive, allow that port from the client subnet in the
   VMware and upstream firewalls.
7. Whether exact quarantined source lines may be retained on the controller.
   Retention is optional, encrypted with a controller-local key, bounded, and
   stored in controller state rather than Supabase. Client raw upload must also
   be enabled before exact-source downloads are available.

When prompted, run `/root/nli-supabase-bootstrap.sql` once in the Supabase SQL
Editor and press Enter. The script then verifies the protected Supabase RPC,
starts `nli-controller-agent.service`, verifies `/api/v1/health`, and creates
the mode-0600 one-use client file `/root/nli-client-bootstrap.env`.

After controller and edge health checks pass, setup atomically records the
selected mode, listener addresses, public origin, and certificate paths in the
root-owned, non-secret `/etc/nli/controller-edge.json` manifest. It contains no
private keys or tunnel/enrollment tokens. The default uninstall preserves it
for recovery; `--purge` removes it with the controller configuration.

Copy that bootstrap file securely to one client. For each additional client,
create a fresh one-use token on the controller:

```bash
sudo nli-controller-setup --rotate-enrollment
```

The bootstrap file contains a one-use enrollment secret and a public CA
certificate. Keep it root-only, transfer it over SSH/SFTP, and never paste its
contents into chat, tickets, shell commands, or logs. Do not copy the CA
private key (`/var/lib/nli-controller/tls/ca.key`) off the controller.

Private-network TLS mode automatically includes the primary controller name,
the host short name and FQDN, detected interface IPs, `localhost`, `127.0.0.1`,
and `::1` in the server certificate SAN list. At the additional-identity
prompt, enter every other stable identity as a comma-separated list, for
example `nli-controller.internal.example,nli-controller.example.net,10.20.30.40`.
For unattended setup set `NLI_SETUP_TLS_IDENTITIES` to that list. Setup rejects
malformed identities, verifies the private CA chain and every SAN before it
starts the service, and prints the final identity list. Rerunning setup adds a
missing identity by replacing only the server certificate under the existing
private CA, so already enrolled clients retain trust. Do not add a DNS name
you do not control or an unstable client address.

Modes 3–5 and 9 keep the controller bound to `127.0.0.1:12382`, set secure
browser cookies, and generate an HTTPS client bootstrap URL without a private
CA. Port `12382` is never opened for public exposure. Setup checks the local
proxy health for Nginx/Apache and the public endpoint for the operator-managed
proxy. A public IP, DNS record, router/NAT mapping, and upstream firewall rules
must already exist: Nginx and Apache only configure this host; neither can
create a public IP address, register a DNS name, or make a private/NATed host
Internet-reachable. Setup tries outbound IP discovery first; if that is
blocked, enter the public IPv4 at the prompt or set `NLI_SETUP_PUBLIC_IP`.
For a host behind NAT, use the router's public IPv4 and forward TCP 80/443 to
the controller. Update public DNS A records to that same address. If the hostname
has AAAA records, provide `NLI_SETUP_PUBLIC_IPV6` (or enter it at the prompt);
each AAAA record must match a globally routable IPv6 address. The ACME check
rejects missing/mismatched A records, missing/mismatched IPv6 identity, a
non-public address, and unrelated local port conflicts before contacting the
certificate authority.
Also allow inbound TCP 80/443 in the cloud security group, router, and upstream
firewall. The installer can inspect local firewalld but cannot prove upstream
forwarding; verify reachability from outside the network if ACME cannot connect.

Mode 3 installs Nginx and Certbot from Rocky/EPEL packages, checks that every
resolved A record matches the detected or operator-provided public IPv4,
issues a Let’s Encrypt certificate through HTTP-01, configures a hardened
Nginx reverse proxy, opens HTTP/HTTPS in active firewalld, and enables
certificate renewal. The proxy preserves host, authorization, and forwarded
client/protocol headers, disables request and response buffering for streaming
and SSE, supports WebSocket upgrades, caps request bodies at 64 MiB, bounds
proxy timeouts, and permits TLS 1.2/1.3. Config syntax is checked before
reload; a failed ACME or config update restores the previous NLI site and any
firewalld services added during this attempt. Do not use a proxied Cloudflare
A record for this mode because setup checks DNS against the origin IP.

Mode 4 uses the Cloudflare Tunnel RPM repository and a remotely managed tunnel
created in Cloudflare Zero Trust. Before setup, configure its public hostname
route to `http://127.0.0.1:12382`, then paste the tunnel token at the hidden
prompt. Setup stores it in a root-managed file readable only by the dedicated
`nli-tunnel` service account and starts a hardened systemd service. The token
is never placed in service arguments or logs. This mode needs outbound
connectivity to Cloudflare and no inbound firewall ports. Protect the hostname
with an appropriate Cloudflare policy that still permits authenticated NLI
client API traffic; the client agent does not carry a Cloudflare Access token.

Mode 5 asks for a hostname and checks public HTTPS health after setup. Configure
your existing Nginx, load balancer, or tunnel to forward to
`http://127.0.0.1:12382`, preserve `Host`, cookies, Authorization, request
bodies, `X-Forwarded-Proto: https`, and WebSocket upgrade headers, and provide a
publicly trusted TLS certificate. Open only the proxy’s public ports. Keep NLI
accounts/RBAC and single-use enrollment tokens enabled. Changing setup modes removes only the
NLI-managed Nginx site or Cloudflare service/token from the previous mode;
other Nginx sites and controller state remain intact.

Mode 9 installs Apache HTTP Server, `mod_ssl`, and Certbot through
Rocky/EPEL packages. It performs the same public-address, DNS, port, and local
firewalld checks as mode 3, uses HTTP-01, and enables a renewal timer that
reloads Apache after renewal. The generated `mod_proxy_http` virtual host
preserves the incoming host and proxy-added client headers, sets HTTPS
forwarding, uses TLS 1.2/1.3, caps bodies at 64 MiB, and bounds proxy timeouts.
HTTP proxy responses stream without an NLI response cache; WebSocket Upgrade is
enabled only after checking Apache is version 2.4.47 or newer. Syntax is
validated before reload and the prior NLI virtual host/firewall changes are
restored if setup fails.

For both reverse-proxy modes, setup records the prior runtime and persistent
`httpd_can_network_connect` SELinux boolean values when it enables the setting.
If setup fails, it restores those values only while the boolean still has the
state NLI applied. A normal uninstall preserves this shared SELinux setting;
explicit `--purge` restores the recorded values only when no later change is
detected.

Mode 10 starts an anonymous Cloudflare Quick Tunnel to the loopback controller
for temporary testing. It needs no domain, DNS record, Cloudflare account,
token, or inbound firewall rule. The random `*.trycloudflare.com` URL is
printed with the exact `NLI_CONTROLLER_ORIGIN` and Vercel environment line and
is also written into the one-use client bootstrap file. Anyone with the URL
can reach the NLI login page; use only for short tests, never production or
sensitive data. The URL expires when the `cloudflared` process stops and a
restart receives a different URL, so generate and transfer a fresh client
bootstrap after restarting. Stop the test tunnel with
`sudo systemctl stop nli-cloudflared-quick.service`.

Mode 6 is for restricted enterprise networks with an internal PKI. Supply an
unencrypted PEM server key, its PEM certificate/full chain, the DNS name or IP
present in the certificate SAN, and, when clients do not already trust it, the
public corporate CA certificate. Setup validates expiry, key matching, and SAN
identity before copying protected files into controller state. Only the public
CA enters the one-use client bootstrap; the controller private key never
leaves the controller.

### Unattended enterprise controller install

Configuration management may create a mode-0600 password file and
`/etc/nli/controller-bootstrap.env` before installing the RPM. The RPM enables
`nli-controller-bootstrap.service`, which waits for networking and retries
after temporary failures. Successful setup removes both one-time files.

```bash
sudo install -d -m 0750 /etc/nli
sudo install -o root -g root -m 0600 /secure/admin-password /etc/nli/initial-admin-password
sudo tee /etc/nli/controller-bootstrap.env >/dev/null <<'EOF'
NLI_SETUP_MODE=6
NLI_SETUP_ENDPOINT=nli-controller.corp.example
NLI_SETUP_TLS_CERT_FILE=/etc/pki/nli/controller-fullchain.pem
NLI_SETUP_TLS_KEY_FILE=/etc/pki/nli/controller.key
NLI_SETUP_TLS_CA_FILE=/etc/pki/ca-trust/source/anchors/corporate-root.pem
NLI_SETUP_ADMIN_PASSWORD_FILE=/etc/nli/initial-admin-password
NLI_SETUP_STORAGE_PROVIDER=local
NLI_SETUP_QUARANTINE_RAW_RETENTION=false
EOF
sudo chmod 0600 /etc/nli/controller-bootstrap.env
sudo dnf -y install ./nli_controller_agent_ver20.rpm
sudo systemctl status nli-controller-bootstrap nli-controller-agent --no-pager
```

For Supabase metadata use `NLI_SETUP_STORAGE_PROVIDER=supabase` and add
`NLI_SETUP_SUPABASE_URL`, `NLI_SETUP_SUPABASE_PUBLISHABLE_KEY`, and
`NLI_SETUP_SUPABASE_SECRET_KEY`. Proxy-restricted hosts may provide standard
`HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` values in the profile.

In local SSH mode, use the packaged SSH-tunnel WebView helper. Private-network
TLS mode still opens TCP `12382` for managed client subnets when firewalld is
active.
The controller installer opens TCP `12382` in an active firewalld instance
when private-network TLS mode is selected. Also permit that port from the
client subnet in VMware and any upstream firewall. NLI does not advertise
controller URLs or enrollment secrets over mDNS.

## Managed client

Run:

```bash
sudo bash install-client-ver20.sh --verify-only
sudo bash install-client-ver20.sh
```

The script discovers the RPM and bootstrap file beside it. If the bootstrap
file is elsewhere, enter its path when prompted or pass it explicitly. If you
enter an `https://...` controller URL at that prompt, setup asks for the
one-use enrollment token separately; the URL is never treated as a file path.
For unattended setup, use a bootstrap file:

```bash
sudo bash install-client-ver20.sh \
  --rpm /path/to/nli_client_ver20.rpm \
  --bootstrap /path/to/nli-client-bootstrap.env
```

After the controller issues a protected node token, the installer clears the
one-use enrollment secret and removes its local bootstrap copy. It then asks:

1. Which real log file path or comma-separated paths the agent must parse.
2. Which parser/service family to use; `auto` is the recommended default.
3. Optional input selection: `default` parses every line, `filtered` sends only
   exact event/service/hostname matches to the parser and silently ignores
   other lines, and `hybrid` sends matches to the selected parser service while
   other lines continue through automatic parsing. When multiple criteria are
   set they are combined with AND; multiple values within one criterion are OR.
   Presets cover `ImapDisconnected`/imapserv, `AcctBadPswd`/popserv,
   `auth.loginByToken`/webtop, `MtaRequestingDomainTableUpdate`/mta,
   `CARDDAV`/pabd, and `SieveRuleMsg`/smtp. Custom comma-separated lists accept
   exact event names, services, and hostnames. Metadata matching reads only
   bounded header fields and does not run parsers on ignored lines.
4. Choose whether to write successful parsed records to local normalised JSONL,
   then select one or more destinations (for example `3,4` for Loki and
   ClickHouse only):

New managed production configurations begin reading at the end of existing
files (`tail_from_end: true`). Empty managed input files begin at byte zero.
Installer parser self-tests use temporary files and never inject records into
production input paths.
   - family-isolated local JSONL files under `/var/log/nli/output`;
   - a local JSONL file below `/var/log/nli` or `/var/lib/nli`;
   - a Loki HTTP endpoint, with optional tenant and bearer token; or
   - a ClickHouse HTTP endpoint, database, table, username, and password.

The client fans each successful parsed JSON event out to every selected normal
destination. Local normalised JSONL can be disabled while remote destinations
remain enabled. Raw unparsed records always stay in a local quarantine JSONL
file and remain available to the retry workflow. Independently, it always sends bounded parser telemetry to the
enrolled controller using the protected node identity, token, and private CA.
That metadata powers the central WebApp, Live Metadata, overview, and time-series
views without sending customer fields. Quarantine metadata is always reported;
exact raw is sent only when the client and controller retention prompts are
both enabled. It remains encrypted local controller state and never goes to
Supabase. Each Loki or ClickHouse destination has its own batch, retry policy,
and local delivery-failure receipt spool.

The script writes `/etc/nli/nli.json`, stores enabled output credentials in
the root-owned `/etc/nli/nli.env`, retains quarantine and unknown records in
`/var/log/nli/quarantine.jsonl`, and restarts and health-checks the parser.
For log files outside `/var/log/nli`, grant the `nli` service account read
permission and directory traversal permission. Both `nli-agent.service` and
`nli-client-control.service` must remain active.

### Unattended enterprise client install

Securely place the controller-generated one-use file at
`/etc/nli/bootstrap.env`, mode `0600`, before installing the RPM. The enabled
`nli-client-bootstrap.service` retries until networking, DNS, TLS, and the
controller are available. It deletes the one-use file after enrollment.

```bash
sudo install -d -m 0750 /etc/nli
sudo install -o root -g root -m 0600 nli-client-bootstrap.env /etc/nli/bootstrap.env
sudo dnf -y install ./nli_client_ver20.rpm
sudo systemctl status nli-client-bootstrap nli-client-control nli-agent --no-pager
```

The client RPM creates empty input files as `nli:nli`, mode `0640`. The agent
creates a family-isolated output file lazily only after the first routed record
for that family, using the current UTC-hour timestamp pattern shown below:

| Input | Successful JSONL | Quarantine JSONL |
|---|---|---|
| `/var/log/nli/input/auto.log` | `/var/log/nli/output/auto_normalised_YYYYMMDD_HH00.jsonl` | `/var/log/nli/output/auto_quarantine_YYYYMMDD_HH00.jsonl` |
| `/var/log/nli/input/imap.log` | `/var/log/nli/output/imap_normalised_YYYYMMDD_HH00.jsonl` | `/var/log/nli/output/imap_quarantine_YYYYMMDD_HH00.jsonl` |
| `/var/log/nli/input/pop.log` | `/var/log/nli/output/pop_normalised_YYYYMMDD_HH00.jsonl` | `/var/log/nli/output/pop_quarantine_YYYYMMDD_HH00.jsonl` |
| `/var/log/nli/input/webtop.log` | `/var/log/nli/output/webtop-webmail_normalised_YYYYMMDD_HH00.jsonl` | `/var/log/nli/output/webtop-webmail_quarantine_YYYYMMDD_HH00.jsonl` |
| `/var/log/nli/input/mta.log` | `/var/log/nli/output/mta_normalised_YYYYMMDD_HH00.jsonl` | `/var/log/nli/output/mta_quarantine_YYYYMMDD_HH00.jsonl` |
| `/var/log/nli/input/smtp-in.log` | `/var/log/nli/output/smtp-in_normalised_YYYYMMDD_HH00.jsonl` | `/var/log/nli/output/smtp-in_quarantine_YYYYMMDD_HH00.jsonl` |
| `/var/log/nli/input/smtp-out.log` | `/var/log/nli/output/smtp-out_normalised_YYYYMMDD_HH00.jsonl` | `/var/log/nli/output/smtp-out_quarantine_YYYYMMDD_HH00.jsonl` |
| `/var/log/nli/input/pabd.log` | `/var/log/nli/output/pabd_normalised_YYYYMMDD_HH00.jsonl` | `/var/log/nli/output/pabd_quarantine_YYYYMMDD_HH00.jsonl` |
| `/var/log/nli/input/capd.log` | `/var/log/nli/output/capd_normalised_YYYYMMDD_HH00.jsonl` | `/var/log/nli/output/capd_quarantine_YYYYMMDD_HH00.jsonl` |
| `/var/log/nli/input/pss.log` | `/var/log/nli/output/pss_normalised_YYYYMMDD_HH00.jsonl` | `/var/log/nli/output/pss_quarantine_YYYYMMDD_HH00.jsonl` |

The aggregate `/var/log/nli/quarantine.jsonl` remains available for durable
replay and the WebApp quarantine workflow, including when local normalised
output is disabled and events go directly to Loki or ClickHouse. Agent configs
that omit a catch-all quarantine file receive a local default at
`<state_dir>/quarantine/records.jsonl`. A missing input does not stop the
agent; after the path is created again, the running agent reopens it.

To use every bundled parser without assigning a family to each path, enter
`auto` at **Parser/service family**. To create and verify all ten configured
empty input paths, enter `automatic` for both log paths and parser family. Event-only
ingestion uses `filtered` plus a reviewed preset or exact custom selectors.
These values are stored under each input's `selection` object in
`/etc/nli/nli.json` and may be changed with `--configure-only`.

The noninteractive default enables all ten input families. The interactive
`automatic` choice creates the empty paths under `/var/log/nli/input` (mode
`0640`, owned by `nli:nli`) and executes each reviewed parser self-test against
a temporary file before starting the service. Existing files are preserved and
no synthetic event is appended. After writing your own authorized test record,
confirm its output with:

```bash
sudo systemctl status nli-agent nli-client-control --no-pager
sudo tail -n 5 /var/log/nli/output/*_normalised_*.jsonl
```

Then open **Live Metadata** and **Live Dashboard** in the WebApp; the test
record's bounded parser telemetry should appear immediately after its controller POST.

If a real log is unreadable, grant the `nli` account directory traversal and
file read access, then use `sudo bash install-client-ver20.sh --configure-only`
to apply and verify the updated paths. The control service needs root access
for restricted package and certificate operations; do not copy its enrollment
token into the parser environment file.

### VMware connectivity and recovery

On the controller, confirm the private address and listener before enrolling:

```bash
ip -brief address
sudo systemctl status nli-controller-agent --no-pager
sudo firewall-cmd --query-port=12382/tcp
```

On a client, confirm the expected route and reachability:

```bash
ip route get CONTROLLER_IP
getent hosts CONTROLLER_DNS_NAME
curl --fail --show-error --cacert /etc/nli/controller-ca.crt \
  https://CONTROLLER_DNS_NAME:12382/api/v1/health
```

Use the same DNS name or IP that was entered during controller TLS setup; the
certificate is tied to that name. If enrollment fails before the token is
consumed, fix the route, firewalls, or CA and rerun the client installer with
the same bootstrap file. If registration consumed the one-use token, rotate a
fresh one on the controller with
`sudo nli-controller-setup --rotate-enrollment`, transfer the new file securely,
and rerun setup on the client. Setup leaves services stopped on an enrollment
failure and prints the
service journal plus route/DNS/firewall checks. After success, confirm
`/var/lib/nli-control-client/node.token` exists and both client services are
active before investigating WebView polling.

To change log paths or switch the destination later without reinstalling or
re-enrolling the client, retain the installer and run:

```bash
sudo bash install-client-ver20.sh --configure-only
```

## Three-node VMware test sequence

For one controller VM and two Rocky Linux client VMs:

1. Copy the versioned archive and its `.sha256` file to the controller through
   the MobaXterm SFTP pane. Verify and extract it into a new versioned directory,
   then run `sudo bash install-controller-ver20.sh --verify-only` followed by
   `sudo bash install-controller-ver20.sh` over SSH.
2. Securely copy the controller's `/root/nli-client-bootstrap.env` to Client 1
   together with the same versioned archive and its `.sha256` file. Extract it
   into a new versioned directory, run `sudo bash install-client-ver20.sh --verify-only`,
   then run `sudo bash install-client-ver20.sh` and enter Client 1's real log paths.
3. Back on the controller, run
   `sudo nli-controller-setup --rotate-enrollment`. This creates a fresh
   `/root/nli-client-bootstrap.env` because enrollment tokens are one-use.
4. Securely copy the new bootstrap file, versioned archive, and archive checksum
   to Client 2. Extract it into a new versioned directory, verify it, then run
   `sudo bash install-client-ver20.sh` and enter Client 2's own log paths.
5. Verify both clients from the controller web interface and run the service
   checks below on all three VMs.

Do not reuse Client 1's bootstrap file for Client 2. The successful Client 1
setup removes its local copy unless `NLI_KEEP_BOOTSTRAP=1` was deliberately
set.

## Service checks

Controller:

```bash
sudo systemctl status nli-controller-agent --no-pager
sudo journalctl -u nli-controller-agent -n 100 --no-pager
```

Client:

```bash
sudo systemctl status nli-agent nli-client-control --no-pager
sudo journalctl -u nli-agent -u nli-client-control -n 100 --no-pager
```

## Additional AWS Red Hat and Cloudflare path

The original guided modes above remain available. For a two-node AWS VPC, the
additional V20 wrappers configure the same RPMs without public DNS or ACME:

```bash
sudo bash aws-rhel-preflight-ver20.sh
sudo bash install-controller-aws-ver20.sh ./nli_controller_agent_ver20.rpm 10.20.1.10
sudo bash install-client-aws-ver20.sh ./nli_client_ver20.rpm ./nli-client-bootstrap.env --automatic
```

Generate a least-privilege AWS CLI security-group plan with the controller SG,
client SG, and administrator CIDR:

```bash
bash aws-security-group-plan-ver20.sh sg-CONTROLLER sg-CLIENT 203.0.113.44/32
```

For the combined AWS plus named-Cloudflare path, first create the tunnel and
published-application route in Cloudflare, mapping the chosen hostname to
`https://127.0.0.1:12382`. Then run:

```bash
sudo bash install-controller-aws-cloudflare-ver20.sh \
  --rpm ./nli_controller_agent_ver20.rpm \
  --private-endpoint 10.20.1.10 \
  --public-hostname nli-controller.example.com \
  --tunnel-token-file /root/cloudflare-tunnel-token.txt
```

See [AWS Red Hat installation](V20_AWS_RED_HAT_INSTALLATION.md) and
[Cloudflare Tunnel](V20_CLOUDFLARE_TUNNEL.md). This path needs controller
outbound TCP/UDP 7844 and TCP 443 according to Cloudflare's current egress
requirements. It needs no public inbound rule for TCP 12382.

## Webapp and live synchronization

No separate data-sync script is required for the deployed webapp. The webapp
does not copy or cache controller records. Its `/api/v1/*` function proxies
authenticated requests to the live NLI controller, while the controller reads
and writes control-plane metadata through protected Supabase RPCs. New client
heartbeats, parser revisions, rollouts, reports, and Assurance records appear
through normal polling without redeploying the webapp.

Supabase private file storage is restricted to two workflows: an operator must
explicitly retain an encrypted Assurance Lab fixture, or approve a signed RPM
or JSON configuration file for a Rollout. Normal production raw logs, parsed
events, metrics, local quarantine records, and delivery spools are never
uploaded to Supabase.

Vercel requires one server-side production environment variable:

```text
NLI_CONTROLLER_ORIGIN=https://your-public-controller-host
```

Controller setup modes 3–5 and 9–10 print the exact public HTTPS controller origin.
Use that origin here without `/api/v1` or a trailing slash. Nginx/Apache ACME
modes require public DNS and inbound TCP 80/443; named Cloudflare Tunnel and
Quick Tunnel need no inbound port. Mode 10 is temporary testing only. An
existing proxy must forward to `http://127.0.0.1:12382` with the documented
headers.

On the deployment workstation, keep this value in the ignored local file and
let the guarded deployment script validate it before writing Vercel's
production environment:

```powershell
Copy-Item .env.local.example .env.local
notepad .env.local
powershell -ExecutionPolicy Bypass -File scripts/deploy-vercel.ps1
```

That value must be a real public HTTPS origin which forwards to the NLI
controller. A private address such as `192.168.x.x`, the Supabase URL, or the
Vercel application URL cannot be used. If the controller remains private, use
the RPM WebView directly or `Open-NLI-WebView.ps1` with an SSH tunnel instead
of the Vercel proxy.

When the private GitHub repository is connected to Vercel, pushes to its
configured production branch deploy code automatically. Live NLI data does
not require a Git push or Vercel redeployment. Changing
`NLI_CONTROLLER_ORIGIN` does require a new production deployment.

Keep the controller origin in Vercel's server-side environment. Keep all
Supabase values and service secrets only in the Rocky controller's protected
environment file; the browser and Vercel proxy do not connect to Supabase.
Never put the controller secret, enrollment token, Supabase secret key,
admin password, or TLS private key in a web bundle or deployment log. The
installer does not print entered credentials.

For GitHub project import, a step-by-step CLI workflow, environment scopes,
the read-only deployment preflight, and post-deployment health checks, see
[Vercel deployment](VERCEL_DEPLOYMENT.md). These instructions supplement the
existing guarded `scripts/deploy-vercel.ps1` workflow.

To host the same WebApp on Cloudflare Pages instead, use
[Cloudflare Pages deployment](CLOUDFLARE_PAGES_DEPLOYMENT.md). Both hosted
paths use the named tunnel's public controller hostname and keep all secrets on
the controller.

## Uninstallation

Controller package only, preserving local configuration and state:

```bash
sudo bash uninstall-controller-ver20.sh
```

Client package only, preserving local configuration, identity, spool, cache,
and logs:

```bash
sudo bash uninstall-client-ver20.sh
```

To permanently remove the preserved local data, add `--purge`. Each script
asks for an additional typed confirmation before deletion. Controller purge
does not delete Supabase cloud data, and client purge does not delete controller
or Supabase data.
