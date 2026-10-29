#!/usr/bin/env python3
"""Static installer contract for public edge and temporary tunnel modes."""

import pathlib
import json
import os
import subprocess
import tempfile
import sys


root = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else pathlib.Path(__file__).parents[2])
setup = (root / "scripts/setup-controller-rocky.sh").read_text(encoding="utf-8")
uninstall = (root / "scripts/uninstall-controller-ver20.sh").read_text(encoding="utf-8")
installer = (root / "scripts/install-controller-ver20.sh").read_text(encoding="utf-8")
docs = (root / "docs/INSTALLATION_AND_CONFIGURATION.md").read_text(encoding="utf-8")
spec = (root / "packaging/rpm/nli-controller-agent.spec").read_text(encoding="utf-8")


def require(condition, message):
    if not condition:
        raise SystemExit(message)


for token in (
    "NLI_SETUP_MODE",
    "Public DNS + Nginx + ACME",
    "Public DNS + Apache HTTP Server + ACME",
    "Temporary Cloudflare Quick Tunnel",
    "value.is_global",
    "dig +short A",
    "dig +short AAAA",
    "NLI_SETUP_PUBLIC_IPV6",
    "value.version == 6 and value.is_global",
    "TCP $port is held by a service other than $expected_listener",
    "could not discover the public IPv4 address",
    "set NLI_SETUP_PUBLIC_IP to this host or NAT/router public address",
    "for command in python3 curl systemctl openssl base64 hostname sha256sum ss",
    "httpd -t",
    "nginx -t",
    '"${endpoint}:443:127.0.0.1"',
    "trap 'edge_rollback || true; rollback_edge_firewall' EXIT",
    "trap 'nginx_rollback || true; rollback_edge_firewall' EXIT",
    "proxy_buffering off",
    "proxy_request_buffering off",
    "proxy_read_timeout 300s",
    "X-Forwarded-For",
    "X-Forwarded-Proto",
    "httpd_can_network_connect",
    "policycoreutils-python-utils",
    "systemctl status \"$service\" --no-pager -l",
    "journalctl -u \"$service\" --no-pager -n 80",
    "SSLProtocol TLSv1.2 TLSv1.3",
    "httpd_version=$(httpd -v",
    "if [[ $(printf '%s\\n%s\\n' 2.4.47",
    "apache_websocket_rules",
    "upgrade=websocket timeout=300",
    "nli-cloudflared-quick.service",
    "trycloudflare\\.com",
    "NLI_CONTROLLER_ORIGIN=$controller_url",
    "/etc/nli/controller-edge.json",
    "tempfile.mkstemp",
    "os.replace(temporary, target)",
    "tls_cert_path",
    "tls_ca_path",
    "selinux_proxy_boolean_changed",
    "selinux_proxy_boolean_prior_current",
    "selinux_proxy_boolean_prior_persistent",
    "setsebool -P httpd_can_network_connect \"$selinux_setup_prior_persistent\"",
    "setsebool httpd_can_network_connect \"$selinux_setup_prior_current\"",
    "discover_host_identities",
    "X-aws-ec2-metadata-token",
    "NLI_AUDIT_HMAC_KEY",
    "NLI_CSRF_HMAC_KEY",
    "/root/nli-controller-credentials.env",
    "existing_or_random_secret NLI_AUDIT_HMAC_KEY audit.key",
    "existing_or_random_secret NLI_CSRF_HMAC_KEY csrf.key",
    "existing_or_random_secret NLI_ASSURANCE_ENCRYPTION_KEY assurance.key",
    "$STATE_DIR/quarantine-metadata/quarantine.key",
    "controller is running but TCP ${PORT} is not listening",
    "EC2 security group must allow inbound TCP ${PORT}",
):
    require(token in setup, f"controller edge contract missing: {token}")

require(setup.count("assurance_key=$(existing_or_random_secret") == 1 and
        "assurance_key=$(read_env NLI_ASSURANCE_ENCRYPTION_KEY)" not in setup,
        "Supabase setup must not rotate the resolved Assurance encryption key")
require("[[ -z $value && -s $CREDENTIAL_FILE ]]" in setup,
        "reconfiguration must recover missing environment values from the root-only credential snapshot")

preflight = setup.index("if [[ $mode == 3 || $mode == 9 ]]; then")
acme_setup = setup.index("if [[ $mode == 9 ]]; then", preflight)
nginx_acme = setup.index("if [[ $mode == 3 ]]; then", acme_setup)
require(preflight < acme_setup < nginx_acme, "public DNS/IP/firewall preflight must precede both ACME paths")
for service in ("nli-cloudflared.service", "nli-cloudflared-quick.service", "nli-certbot-renew.timer"):
    require(service in uninstall and "systemctl disable --now" in uninstall,
            f"uninstall must stop the NLI-owned {service}")
for owned_path in (
    "/etc/nginx/conf.d/nli-controller.conf",
    "/etc/httpd/conf.d/nli-controller.conf",
    "/etc/systemd/system/nli-cloudflared-quick.service",
    "/etc/systemd/system/nli-certbot-renew.timer",
    "/etc/nli/cloudflare/tunnel-token",
):
    require(owned_path in uninstall, f"uninstall must remove NLI-owned artifact {owned_path}")
require("remove_managed_site /etc/nginx/conf.d/nli-controller.conf nginx" in uninstall and
        "remove_managed_site /etc/httpd/conf.d/nli-controller.conf httpd" in uninstall and
        "validator\" -t" in uninstall and "the previous site was restored" in uninstall,
        "uninstall must validate proxy config and restore the managed site if reload fails")
require("restore_selinux_proxy_boolean || true" in setup and
        "restore_nli_selinux_boolean_on_purge" in uninstall and
        "preserving httpd_can_network_connect because its state changed" in uninstall,
        "setup rollback and explicit purge must restore SELinux state only when safe")
require("dnf -y remove nginx" not in uninstall and "dnf -y remove httpd" not in uninstall,
        "uninstall must leave shared proxy packages installed")
require("firewall-cmd --remove-service" not in uninstall,
        "uninstall must not remove shared/pre-existing firewall services")
manifest_write = setup.rindex("python3 - /etc/nli/controller-edge.json")
require(setup.index('info "PASS: controller is healthy at $controller_url"') < manifest_write,
        "the edge manifest must be updated only after controller and selected edge health checks pass")
quick_url_check = setup.index("curl --fail --silent --show-error --max-time 20 \"${controller_url}/api/v1/health\"")
bootstrap_write = setup.index('cat >"$BOOTSTRAP_FILE" <<EOF')
require(quick_url_check < bootstrap_write,
        "the client bootstrap must not be overwritten until the Quick Tunnel origin passes health")
require("quick_service_backup" in setup and "quick_service_was_active" in setup and
        "cp -a \"$quick_service_backup\" \"$quick_service\"" in setup,
        "Quick Tunnel rollback must preserve a prior managed service file and active state")
require("capture_renewal_state" in setup and "restore_renewal_state" in setup,
        "failed ACME setup must restore prior NLI renewal units and timer state")
purge_block = uninstall.index("if [[ $purge -eq 1 ]]; then")
require(uninstall.index("/etc/nli/controller-edge.json", purge_block) > purge_block,
        "uninstall may remove the edge manifest only in the explicit purge path")
require('"key"' not in setup[manifest_write:setup.index("PY", manifest_write)] and
        '"token"' not in setup[manifest_write:setup.index("PY", manifest_write)],
        "the edge manifest must not contain keys or tokens")
manifest_body_start = setup.index("\n", manifest_write) + 1
manifest_body_end = setup.index("\nPY\n", manifest_body_start)
manifest_writer = setup[manifest_body_start:manifest_body_end]
manifest_environment = os.environ.copy()
manifest_environment.update(
    {
        "NLI_EDGE_MODE": "9",
        "NLI_EDGE_PROXY_SERVICE": "httpd",
        "NLI_EDGE_PUBLIC_ORIGIN": "https://controller.example.net",
        "NLI_EDGE_CONTROLLER_LISTENER": "127.0.0.1:12382",
        "NLI_EDGE_LISTENERS": "80/tcp 443/tcp",
        "NLI_EDGE_TLS_CERT_PATH": "/etc/letsencrypt/live/controller.example.net/fullchain.pem",
        "NLI_EDGE_TLS_CA_PATH": "",
        "NLI_EDGE_SELINUX_MANAGED": "false",
        "NLI_EDGE_SELINUX_CHANGED": "false",
        "NLI_EDGE_SELINUX_PRIOR_CURRENT": "",
        "NLI_EDGE_SELINUX_PRIOR_PERSISTENT": "",
        "NLI_EDGE_CONFIGURED_AT": "2026-10-14T00:00:00+00:00",
    }
)
with tempfile.TemporaryDirectory() as directory:
    manifest_path = pathlib.Path(directory) / "controller-edge.json"
    manifest_path.write_text("stale contents\n", encoding="utf-8")
    subprocess.run(
        [sys.executable, "-c", manifest_writer, str(manifest_path)],
        env=manifest_environment,
        check=True,
    )
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if os.name == "posix":
        require(manifest_path.stat().st_mode & 0o777 == 0o644,
                "the edge manifest must be world-readable metadata without broad write access")
    require(manifest == {
        "mode": 9,
        "proxy_service": "httpd",
        "public_origin": "https://controller.example.net",
        "controller_listener": "127.0.0.1:12382",
        "edge_listeners": ["80/tcp", "443/tcp"],
        "tls_cert_path": "/etc/letsencrypt/live/controller.example.net/fullchain.pem",
        "tls_ca_path": "",
        "selinux_proxy_boolean_managed": False,
        "selinux_proxy_boolean_changed": False,
        "selinux_proxy_boolean_prior_current": "",
        "selinux_proxy_boolean_prior_persistent": "",
        "configured_at": "2026-10-14T00:00:00+00:00",
    }, "the edge manifest writer must replace old content with the expected secret-free schema")
require("--quickstart" in installer and "--mode" in installer and "NLI_SETUP_MODE=$deployment_mode" in installer,
        "V20 installer must expose explicit deployment-mode selection")
require("Nginx and Apache only configure this host" in docs,
        "installation docs must state that reverse proxies cannot create public IP or DNS")
require("Mode 10" in docs and "Mode 9" in docs and "NLI_CONTROLLER_ORIGIN" in docs,
        "installation docs must describe Apache ACME, Quick Tunnel, and the exact origin")
require("NLI_SETUP_PUBLIC_IP" in docs and "behind NAT" in docs and "router's public IPv4" in docs,
        "installation docs must explain manual public-IP fallback behind NAT")
require("normal uninstall preserves this shared SELinux setting" in docs and
        "explicit `--purge` restores the recorded values" in docs,
        "installation docs must describe SELinux rollback and purge behavior")
require("Requires:       python3 curl openssl hostname iproute" in spec,
        "controller RPM must provide the setup preflight dependencies, including ss")

print("controller edge installer contract tests: PASS")
