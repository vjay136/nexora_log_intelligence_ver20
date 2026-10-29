#!/usr/bin/env bash
set -euo pipefail

CONFIG=${NLI_CLIENT_CONFIG:-/etc/nli/client-control.json}
ENV_FILE=${NLI_CLIENT_ENV_FILE:-/etc/nli/client-control.env}
SERVICE=${NLI_CLIENT_SERVICE:-nli-client-control.service}
BINARY=${NLI_CLIENT_BINARY:-/usr/bin/nli-client-control}
CA_FILE=${NLI_CONTROLLER_CA_FILE:-/etc/nli/controller-ca.crt}
BOOTSTRAP_FILE=${1:-}
if [[ -z $BOOTSTRAP_FILE && -f /etc/nli/bootstrap.env ]]; then
  BOOTSTRAP_FILE=/etc/nli/bootstrap.env
fi

fail() { printf 'nli-client-setup: %s\n' "$*" >&2; exit 1; }
info() { printf 'nli-client-setup: %s\n' "$*"; }
[[ ${EUID:-$(id -u)} -eq 0 ]] || fail 'run as root: sudo nli-client-setup [/path/to/nli-client-bootstrap.env]'
for command in python3 curl systemctl base64 openssl; do command -v "$command" >/dev/null || fail "$command is required"; done
[[ -x $BINARY && -f $CONFIG ]] || fail 'install the client RPM before running setup'

read_bundle() {
  local key=$1
  sed -n "s/^${key}=//p" "$BOOTSTRAP_FILE" | tail -n 1
}
controller_url=''
enrollment_token=''
ca_base64=''
ca_candidate=''
ca_commit_required=false
cleanup() {
  [[ -z $ca_candidate ]] || rm -f -- "$ca_candidate"
}
trap cleanup EXIT
if [[ -n $BOOTSTRAP_FILE ]]; then
  if [[ -f $BOOTSTRAP_FILE ]]; then
    controller_url=$(read_bundle NLI_CONTROLLER_URL)
    enrollment_token=$(read_bundle NLI_ENROLLMENT_TOKEN)
    ca_base64=$(read_bundle NLI_CONTROLLER_CA_BASE64)
  elif [[ $BOOTSTRAP_FILE == http://* || $BOOTSTRAP_FILE == https://* ]]; then
    controller_url=$BOOTSTRAP_FILE
    [[ -t 0 ]] || fail 'a controller URL requires an interactive enrollment-token prompt; pass a bootstrap file for unattended setup'
    read -r -s -p 'Controller enrollment token: ' enrollment_token
    printf '\n'
  else
    fail "bootstrap file not found: $BOOTSTRAP_FILE (pass a file path or enter a controller URL)"
  fi
else
  printf '\nNLI v20 managed-client setup\n'
  read -r -p 'Controller URL (production requires HTTPS): ' controller_url
  read -r -s -p 'Controller enrollment token: ' enrollment_token
  printf '\n'
fi

NLI_SETUP_URL=$controller_url python3 - <<'PY'
import os, urllib.parse
value = os.environ.get("NLI_SETUP_URL", "")
parsed = urllib.parse.urlparse(value)
if parsed.scheme not in {"http", "https"} or not parsed.hostname:
    raise SystemExit("controller URL must be absolute HTTP(S)")
try:
    parsed.port
except ValueError as error:
    raise SystemExit(f"controller URL has an invalid port: {error}")
if parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.path not in {"", "/"}:
    raise SystemExit("controller URL must contain only scheme, host, and optional port")
host = parsed.hostname.rstrip(".").lower()
if host == "example.com" or host.endswith(".example.com") or host.endswith(".example"):
    raise SystemExit("documentation placeholder controller host is not allowed")
PY
[[ $(LC_ALL=C printf '%s' "$enrollment_token" | wc -c) -ge 24 ]] || fail 'enrollment token must contain at least 24 bytes'
[[ $enrollment_token =~ ^[A-Za-z0-9._~!@#%^+=,:-]+$ ]] || fail 'enrollment token contains unsafe characters'

scheme=${controller_url%%:*}
custom_ca=''
existing_controller_url=$(python3 - "$CONFIG" <<'PY'
import json, pathlib, sys
try:
    value = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
    print(str(value.get("controller_url", "")).rstrip("/"))
except (OSError, ValueError):
    pass
PY
)
existing_ca=$(python3 - "$CONFIG" <<'PY'
import json, pathlib, sys
try:
    value = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
    print(str(value.get("ca_file", "")).strip())
except (OSError, ValueError):
    pass
PY
)
if [[ $scheme == https ]]; then
  if [[ -n $ca_base64 ]]; then
    umask 077
    ca_candidate=$(mktemp "${CA_FILE}.candidate.XXXXXX")
    printf '%s' "$ca_base64" | base64 -d >"$ca_candidate" || fail 'bootstrap CA is not valid base64'
    openssl x509 -in "$ca_candidate" -noout >/dev/null 2>&1 || fail 'bootstrap CA is not a valid PEM certificate'
    chmod 0644 "$ca_candidate"
    custom_ca=$ca_candidate
    ca_commit_required=true
  elif [[ -t 0 ]]; then
    read -r -p 'Private CA file (Enter to use the system trust store): ' supplied_ca
    if [[ -n $supplied_ca ]]; then
      [[ -f $supplied_ca ]] || fail "CA file not found: $supplied_ca"
      openssl x509 -in "$supplied_ca" -noout >/dev/null 2>&1 || fail 'supplied CA is not a valid PEM certificate'
      custom_ca=$supplied_ca
      ca_commit_required=true
    fi
  fi
  if [[ -z $custom_ca && ${controller_url%/} == "$existing_controller_url" && -n $existing_ca && -f $existing_ca ]]; then
    openssl x509 -in "$existing_ca" -noout >/dev/null 2>&1 || fail "configured controller CA is not a valid PEM certificate: $existing_ca"
    custom_ca=$existing_ca
    info "preserving verified controller CA from the current configuration: $existing_ca"
  fi
else
  host=$(NLI_SETUP_URL=$controller_url python3 -c 'import os,urllib.parse; print(urllib.parse.urlparse(os.environ["NLI_SETUP_URL"]).hostname)')
  if [[ $host != 127.0.0.1 && $host != localhost && $host != ::1 ]]; then
    read -r -p 'Plain HTTP exposes enrollment traffic. Type INSECURE-LAB to continue: ' confirmation
    [[ $confirmation == INSECURE-LAB ]] || fail 'refused non-loopback plain HTTP controller'
  fi
fi

environment=${NLI_SETUP_ENVIRONMENT:-}
region=${NLI_SETUP_REGION:-}
team=${NLI_SETUP_TEAM:-}
if [[ -t 0 ]]; then
  [[ -n $environment ]] || read -r -p 'Environment tag [production]: ' environment || true
  [[ -n $region ]] || read -r -p 'Region tag [local]: ' region || true
  [[ -n $team ]] || read -r -p 'Team tag [operations]: ' team || true
fi
environment=${environment:-production}
region=${region:-local}
team=${team:-operations}
for value in "$environment" "$region" "$team"; do [[ $value =~ ^[A-Za-z0-9._:-]+$ ]] || fail 'tags may contain only letters, digits, . _ : -'; done

curl_args=(--fail --silent --show-error --connect-timeout 5 --max-time 15 --header 'Accept: application/json')
[[ -n $custom_ca ]] && curl_args+=(--cacert "$custom_ca")
health_headers=$(mktemp "${CONFIG}.health.XXXXXX")
curl_diagnostics=$(mktemp "${CONFIG}.curl-diagnostics.XXXXXX")
if ! health_json=$(curl "${curl_args[@]}" --verbose --dump-header "$health_headers" "${controller_url%/}/api/v1/health" 2>"$curl_diagnostics"); then
  rm -f "$health_headers"
  controller_host=$(NLI_SETUP_URL=$controller_url python3 -c 'import os,urllib.parse; print(urllib.parse.urlparse(os.environ["NLI_SETUP_URL"]).hostname)')
  controller_port=$(NLI_SETUP_URL=$controller_url python3 -c 'import os,urllib.parse; print(urllib.parse.urlparse(os.environ["NLI_SETUP_URL"]).port or (443 if os.environ["NLI_SETUP_URL"].startswith("https:") else 80))')
  info "controller health check failed for ${controller_host}:${controller_port}; connection details follow"
  if command -v getent >/dev/null 2>&1; then
    if ! getent ahosts "$controller_host"; then
      info "DNS lookup failed for $controller_host; use the controller's reachable private IP or correct the corporate DNS record"
    fi
  fi
  if grep -Eqi 'SSL certificate problem|certificate verify failed|no alternative certificate subject name|self-signed certificate' "$curl_diagnostics"; then
    info 'TLS trust or hostname verification failed. Recopy a fresh nli-client-bootstrap.env from this controller; it carries the generated private CA and exact URL. Never disable certificate verification.'
  elif grep -Eqi 'Could not resolve host|Name or service not known' "$curl_diagnostics"; then
    info 'DNS resolution failed. Use a controller hostname resolvable by this client or its private IP, and confirm the client DNS settings.'
  elif grep -Eqi 'Connection refused|Failed to connect' "$curl_diagnostics"; then
    info 'TCP connection was refused. Confirm nli-controller-agent is active, TCP 12382 is allowed from this client subnet, and the controller URL is correct.'
  elif grep -Eqi 'timed out|Timeout was reached' "$curl_diagnostics"; then
    info 'Connection timed out. Check client/controller routes, firewalld, VMware networking, and upstream ACLs for TCP 12382.'
  fi
  sed -n '/Trying /p; /Connected to /p; /SSL certificate/p; /subject:/p; /issuer:/p; /curl:/p; /error:/p' "$curl_diagnostics" >&2 || true
  rm -f "$curl_diagnostics"
  fail 'controller health check failed; correct the specific DNS, route, firewall, certificate trust, or hostname issue above and rerun setup'
fi
rm -f "$curl_diagnostics"
tr -d '\r' <"$health_headers" | grep -Eiq '^X-NLI-Controller:[[:space:]]*v20$' || {
  rm -f "$health_headers"
  fail 'target did not identify itself as an NLI v20 controller'
}
rm -f "$health_headers"
printf '%s' "$health_json" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d.get("service")=="nli-controller-agent" and d.get("api_version")=="v1"' || fail 'target is not an NLI v20 controller'

if [[ $ca_commit_required == true ]]; then
  verified_ca=$(mktemp "${CA_FILE}.verified.XXXXXX")
  install -o root -g root -m 0644 "$custom_ca" "$verified_ca"
  mv -f -- "$verified_ca" "$CA_FILE"
  custom_ca=$CA_FILE
fi

# Commit controller trust and endpoint settings only after the candidate
# controller has passed certificate, identity, and service checks.
# A failed setup attempt must never erase the last-known working CA or endpoint.
NLI_SETUP_URL=$controller_url NLI_SETUP_CA=$custom_ca NLI_SETUP_ENV=$environment NLI_SETUP_REGION=$region NLI_SETUP_TEAM=$team \
python3 - "$CONFIG" <<'PY'
import json, os, pathlib, sys, tempfile
path = pathlib.Path(sys.argv[1])
value = json.loads(path.read_text(encoding="utf-8"))
value["controller_url"] = os.environ["NLI_SETUP_URL"].rstrip("/")
value["ca_file"] = os.environ["NLI_SETUP_CA"]
value["allow_insecure_http"] = value["controller_url"].startswith("http://")
value["tags"] = {
    "environment": os.environ["NLI_SETUP_ENV"],
    "region": os.environ["NLI_SETUP_REGION"],
    "team": os.environ["NLI_SETUP_TEAM"],
}
fd, name = tempfile.mkstemp(prefix=path.name + ".verified.", dir=path.parent)
candidate = pathlib.Path(name)
try:
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        json.dump(value, stream, indent=2, sort_keys=True)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    candidate.chmod(0o640)
    candidate.replace(path)
finally:
    if candidate.exists():
        candidate.unlink()
PY
chown root:nli "$CONFIG"
chmod 0640 "$CONFIG"

umask 077
env_tmp=$(mktemp "${ENV_FILE}.XXXXXX")
printf 'NLI_ENROLLMENT_TOKEN=%s\n' "$enrollment_token" >"$env_tmp"
install -o root -g nli -m 0640 "$env_tmp" "$ENV_FILE"
rm -f "$env_tmp"

systemctl stop "$SERVICE" >/dev/null 2>&1 || true
if [[ ${NLI_REENROLL:-0} == 1 && -s /var/lib/nli-control-client/node.token ]]; then
  mv /var/lib/nli-control-client/node.token "/var/lib/nli-control-client/node.token.backup.$(date +%s)"
fi
NLI_ENROLLMENT_TOKEN=$enrollment_token "$BINARY" --config "$CONFIG" --once || {
  enrollment_token=''
  journalctl -u "$SERVICE" --no-pager -n 60 >&2 || true
  info 'check controller URL, client route (`ip route`), name resolution (`getent hosts HOST`), TCP 12382 reachability, and the controller firewall'
  fail 'enrollment/heartbeat failed; services remain stopped. Fix connectivity and rerun sudo nli-client-setup with the same unused bootstrap file or fresh token'
}
[[ -s /var/lib/nli-control-client/node.token ]] || fail 'controller did not issue a node token'
chown root:nli /var/lib/nli-control-client/node.token
chmod 0640 /var/lib/nli-control-client/node.token
printf 'NLI_ENROLLMENT_TOKEN=\n' >"$ENV_FILE"
chown root:nli "$ENV_FILE"
chmod 0640 "$ENV_FILE"
enrollment_token=''

agent_config=${NLI_AGENT_CONFIG:-/etc/nli/nli.json}
agent_service=${NLI_AGENT_SERVICE:-nli-agent.service}
[[ -f $agent_config ]] || fail "agent config not found: $agent_config"
while IFS= read -r input_path; do
  [[ -n $input_path ]] || continue
  case "$input_path" in
    /var/log/nli/*)
      install -d -o nli -g nli -m 0750 "$(dirname "$input_path")"
      if [[ ! -e $input_path ]]; then
        install -o nli -g nli -m 0640 /dev/null "$input_path"
        info "created configured parser input: $input_path"
      fi
      ;;
    *) info "configured parser input is operator-managed and was not created: $input_path" ;;
  esac
done < <(python3 - "$agent_config" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
value = json.loads(path.read_text(encoding="utf-8"))
for item in value.get("inputs", []):
    candidate = pathlib.PurePosixPath(str(item.get("path", "")))
    if candidate.is_absolute() and ".." not in candidate.parts:
        print(candidate)
PY
)

while IFS= read -r output_path; do
  [[ -n $output_path ]] || continue
  case "$output_path" in
    /var/log/nli/*|/var/lib/nli/*)
      install -d -o nli -g nli -m 0750 "$(dirname "$output_path")"
      [[ -e $output_path ]] || install -o nli -g nli -m 0640 /dev/null "$output_path"
      ;;
  esac
done < <(python3 - "$agent_config" <<'PY'
import json, pathlib, sys
value = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
for item in value.get("outputs", []):
    if item.get("type") == "file" and item.get("path"):
        print(item["path"])
PY
)

# Send bounded parser telemetry to the controller. Customer parsed fields stay
# in the configured normal outputs and never go to the controller or Supabase.
python3 - "$agent_config" "$CONFIG" <<'PY'
import json, os, pathlib, shutil, sys, tempfile
agent_path = pathlib.Path(sys.argv[1])
control = json.loads(pathlib.Path(sys.argv[2]).read_text(encoding="utf-8"))
agent = json.loads(agent_path.read_text(encoding="utf-8"))
url = str(control.get("controller_url", "")).rstrip("/")
if not url:
    raise SystemExit("client-control.json has no controller URL after enrollment")
telemetry = {
    "type": "telemetry", "routes": ["telemetry"], "url": url,
    "node_id_file": "/var/lib/nli-control-client/install.id",
    "token_file": "/var/lib/nli-control-client/node.token",
    "retry_max": 3, "retry_backoff_ms": 200,
}
ca_file = str(control.get("ca_file", "")).strip()
if ca_file:
    telemetry["ca_file"] = ca_file
# Migrate old setup files away from the disabled raw-event endpoint. Preserve
# every other selected customer output; keep a local normal output if it was
# the only destination and remove it accidentally with the legacy controller.
outputs = [item for item in agent.get("outputs", []) if item.get("type") != "controller"]
normal = [item for item in outputs if "quarantine" not in item.get("routes", []) and "unknown" not in item.get("routes", [])]
if not normal:
    outputs.insert(0, {"type": "file", "routes": ["normal", "warning"], "path": "/var/log/nli/normalized.jsonl", "max_mb": 500, "max_backups": 5})
agent["outputs"] = outputs
agent["telemetry_outputs"] = [telemetry]
fd, tmp = tempfile.mkstemp(prefix=agent_path.name + ".", dir=agent_path.parent)
try:
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        json.dump(agent, handle, indent=2)
        handle.write("\n")
        handle.flush()
        os.fsync(handle.fileno())
    os.chmod(tmp, 0o640)
    shutil.chown(tmp, user="root", group="nli")
    os.replace(tmp, agent_path)
finally:
    if os.path.exists(tmp):
        os.unlink(tmp)
PY

# Enrollment can apply an approved parser generation before the services start.
# Reassert the RPM runtime ownership contract after that atomic root-run swap so
# the unprivileged parser agent can traverse packs and maintain quarantine state.
install -d -o root -g nli -m 0750 /var/lib/nli/parsers
install -d -o nli -g nli -m 0750 /var/lib/nli/parsers/quarantine
find /var/lib/nli/parsers -maxdepth 1 -type f -exec chown root:nli {} + -exec chmod 0640 {} +

systemctl daemon-reload
systemctl enable --now "$agent_service" "$SERVICE"
sleep 2
for active_service in "$agent_service" "$SERVICE"; do
  systemctl is-active --quiet "$active_service" || {
    journalctl -u "$active_service" --no-pager -n 80 >&2 || true
    fail "$active_service did not remain active"
  }
done
if [[ -n $BOOTSTRAP_FILE && ${NLI_KEEP_BOOTSTRAP:-0} != 1 ]]; then
  rm -f "$BOOTSTRAP_FILE"
  info 'removed the local one-use bootstrap copy after successful enrollment'
fi
info 'PASS: parser and control services are active, enrollment succeeded, heartbeat passed, and the node token is protected'
info 'no sample events were injected; live counters advance only for operator-provided input records'
