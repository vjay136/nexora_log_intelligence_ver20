# V20 Cloudflare Tunnel on an AWS Red Hat controller

This is an additive V20 deployment path. It does not change the existing V20 controller installer or deployment modes. Use a **named tunnel** for a stable hostname consumed by Vercel or production users. Use a **Quick Tunnel** only for temporary diagnosis; its random public URL changes and anyone with it may reach the controller.

The controller listens locally at `https://127.0.0.1:12382`. `cloudflared` makes outbound connections to Cloudflare and forwards the configured hostname to that loopback origin. A named tunnel needs no AWS inbound rule for TCP 12382, and that port must not be opened to the public internet.

## Prerequisites and firewall

- AWS EC2 on x86_64 RHEL or Rocky Linux 8, 9, or 10, with the NLI controller installed and healthy.
- `dnf` or `yum`, `curl`, and `systemd`. The setup script installs `cloudflared` from Cloudflare's official RPM repository and requires version 2025.4.0 or newer for token-file support.
- A Cloudflare account and a domain whose authoritative DNS zone is already **Active** on Cloudflare. The hostname must belong to that zone, such as `nli.example.com`.
- Permit outbound DNS (UDP/TCP 53), UDP 7844 for QUIC, and TCP 7844 for HTTP/2 fallback. Permit outbound TCP 443 for Cloudflare control/package endpoints and OS updates. If policy permits only one tunnel transport, allow TCP 7844. Scope destinations to current Cloudflare Tunnel egress guidance and organizational policy.
- Keep inbound TCP 12382 closed to the internet. Keep inbound SSH restricted to administrator IPs. Inbound 80/443 is not needed for the tunnel.

## Production: named tunnel with stable hostname

### 1. Create the named tunnel in Cloudflare

In the Cloudflare dashboard, select the account and go to **Networking > Tunnels** (some layouts show **Zero Trust > Networks > Tunnels**). Select **Create a tunnel**, choose **Cloudflared**, name it (for example `nli-controller-prod`), and save. Keep the connector installation token private. Copy it into a root-readable file on the AWS controller, for example `/root/cloudflare-tunnel-token.txt`, with mode `0600`. Do not put the token in a command argument, ticket, shell history, or logs. The setup script copies it to `/etc/cloudflared/tunnel-token` with mode `0640`, readable by root and the dedicated `cloudflared` service group.

### 2. Add the published application route

In **Networking > Tunnels**, open the tunnel, choose **Add a public hostname** / **Published application route**, and enter these values:

| Dashboard field | Value |
| --- | --- |
| Subdomain | `nli` (or your chosen subdomain) |
| Domain | `example.com` (choose the active Cloudflare zone) |
| Path | Leave blank |
| Service type | `HTTPS` |
| URL / Service | `127.0.0.1:12382` (or `https://127.0.0.1:12382` if the field requires a scheme) |
| Additional application settings > TLS > No TLS Verify | Enable only for the self-signed/private controller certificate, and only while the service URL is exactly `127.0.0.1:12382` |

The route connects the public hostname through the connector to the host's loopback HTTPS listener. Never enter an EC2 public IP or bind this route to a public interface. For a private CA you administer, prefer configuring its CA pool and the certificate's Origin Server Name in the route's TLS settings, keeping **No TLS Verify** disabled. If you enable **No TLS Verify**, it applies only between `cloudflared` and this loopback origin; public visitors still use HTTPS to Cloudflare. Confirm the dashboard creates the proxied DNS record in the active zone.

### 3. Install and start the connector on the controller

Copy `scripts/setup-cloudflare-tunnel-v20.sh` and `scripts/verify-cloudflare-tunnel-v20.sh` to the AWS controller, then run:

```sh
sudo bash setup-cloudflare-tunnel-v20.sh \
  --hostname 'nli.example.com' \
  --token-file '/root/cloudflare-tunnel-token.txt'
```

The setup is safe to rerun with the same values. It installs the official RPM package, creates a restricted system account, protects the connector token in a file, and enables `cloudflared.service`. The systemd unit uses `--token-file`, so the token does not appear in the unit or process arguments. The Cloudflare dashboard owns the public hostname/origin route; setup records the expected hostname for verification and does not create/delete Cloudflare-side resources.

Validate:

```sh
sudo bash verify-cloudflare-tunnel-v20.sh
sudo systemctl --no-pager --full status cloudflared.service
```

The verifier checks controller `/api/v1/health` over loopback HTTPS, public hostname `/api/v1/health`, controller and tunnel systemd state, and recent registered Cloudflare edge connections. It never prints credentials. Test the hostname from outside the VPC as well. Set Vercel's `NLI_CONTROLLER_ORIGIN` to `https://nli.example.com` (origin only, with no API path) in the server-side environment.

### 4. Remove the local connector

```sh
sudo bash uninstall-cloudflare-tunnel-v20.sh
```

This disables/removes the local service, config, and token. It deliberately leaves the Cloudflare RPM repository/package and dashboard tunnel/hostname in place; remove those separately in Cloudflare when retiring the deployment.

## Temporary diagnosis: Quick Tunnel

For a one-time check, run the separate foreground helper:

```sh
sudo bash cloudflare-quick-diagnosis-v20.sh
```

It installs the official RPM if needed, validates local controller health, and starts `cloudflared tunnel --url https://127.0.0.1:12382 --no-tls-verify`. Copy the ephemeral `https://<random>.trycloudflare.com` URL from the terminal only for the immediate diagnosis and stop it with Ctrl+C. The process uses an isolated temporary home, so a named-tunnel configuration cannot collide with Quick Tunnel behavior. Quick Tunnels are public, temporary, and have no uptime guarantee; never use one as a production/Vercel origin.

## Troubleshooting

- **Local health fails:** inspect `systemctl status nli-controller-agent`; confirm the controller serves HTTPS on loopback port 12382 and test `curl -k https://127.0.0.1:12382/api/v1/health` locally.
- **Tunnel is active but public hostname gives 1033:** check connector state in the Cloudflare dashboard and outbound UDP/TCP 7844 rules; the connector must register an edge connection.
- **Tunnel is healthy but hostname fails:** verify the active zone, published route's exact HTTPS service `127.0.0.1:12382`, blank path, and DNS record. Do not open port 12382 as a workaround.
- **Origin TLS errors:** prefer a valid private CA pool and the matching Origin Server Name. `No TLS Verify` is a last resort and belongs only on the loopback origin route.
- **Inspect service logs:** `sudo journalctl -u cloudflared.service -n 100 --no-pager`. Never enable shell tracing around credentials or print the token file.

## Test scripts without Cloudflare access

Run `node --test tests/cloudflare-tunnel-v20.test.js` from the repository root. These tests check script invariants and do not contact Cloudflare or require a live tunnel.
