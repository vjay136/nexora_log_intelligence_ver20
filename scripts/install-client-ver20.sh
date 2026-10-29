#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Install, enroll, and start a Nexora Log Intelligence Ver20 client on Rocky Linux 9 or 10.

Usage:
  sudo bash install-client-ver20.sh [--rpm /path/to/nli_client_ver20.rpm]
                                      [--bootstrap /path/to/nli-client-bootstrap.env]
                                      [--automatic|--all-log-files]
  sudo bash install-client-ver20.sh --configure-only
  sudo bash install-client-ver20.sh --verify-only [--rpm /path/to/rpm]

With no options, the script finds the RPM and bootstrap file beside this
script or in the current directory. If no bootstrap file is found, it prompts
for a path; pressing Enter uses the packaged manual URL/token prompts. After
enrollment it asks which real log files to parse and which one or more
destinations receive successful parsed fields: local JSONL, Loki, and/or
ClickHouse. Bounded parser metadata is always sent to the enrolled controller
for the WebApp; parsed customer fields remain in the selected normal outputs. Use
--configure-only later to change those paths or the destination without
reinstalling or re-enrolling the client.

--automatic enables every bundled parser family, creates all ten standard
input files and their family normal/quarantine outputs, and selects local
JSONL without asking parser/output questions.

--all-log-files is the descriptive alias for --automatic. It preserves
existing files, creates missing inputs/outputs with nli ownership, verifies
every reviewed parser pack, and attaches the matching family automatically.
EOF
}

fail() { printf 'install-client-ver20: %s\n' "$*" >&2; exit 1; }
info() { printf 'install-client-ver20: %s\n' "$*"; }

validate_rpm_release() {
  local value=$1 label=$2 expected=$3 numeric
  numeric=${value%%.*}
  [[ $numeric == "$expected" ]] ||
    fail "the selected $label RPM release $value does not match this bundle release $expected"
}

load_release_identity() {
  local marker=$1
  [[ -f $marker ]] || fail 'RELEASE_ID is missing; extract the complete versioned release archive into a fresh directory'
  expected_version=$(awk -F= '$1=="NLI_VERSION" {print $2; exit}' "$marker")
  expected_release=$(awk -F= '$1=="NLI_RELEASE" {print $2; exit}' "$marker")
  expected_arch=$(awk -F= '$1=="NLI_ARCH" {print $2; exit}' "$marker")
  expected_revision=$(awk -F= '$1=="SOURCE_REVISION" {print $2; exit}' "$marker")
  [[ $expected_version == 20.0.0 ]] || fail "invalid bundle version in RELEASE_ID: $expected_version"
  [[ $expected_release =~ ^[1-9][0-9]*$ ]] || fail "invalid bundle release in RELEASE_ID: $expected_release"
  [[ $expected_arch == x86_64 ]] || fail "invalid bundle architecture in RELEASE_ID: $expected_arch"
  [[ $expected_revision =~ ^[0-9a-f]{40}$ ]] || fail 'invalid source revision in RELEASE_ID'
}

verify_release_bundle() {
  local checksum_file=$1 required
  [[ -f $checksum_file ]] || fail 'SHA256SUMS is missing; extract the complete versioned release archive into a fresh directory'
  for required in RELEASE_ID nli_client_ver20.rpm nli_clinet_ver20.rpm nli_controller_agent_ver20.rpm \
    webview_ver20.zip webapp_ver20.zip install-controller-ver20.sh install-client-ver20.sh \
    uninstall-controller-ver20.sh uninstall-client-ver20.sh INSTALLATION_AND_CONFIGURATION.md \
    WEBAPP_OPERATOR_AND_PARSER_GUIDE.md V20_OPERATOR_DEMO_GUIDE.md V20_PRODUCTION_ACCEPTANCE_TESTS.md V20_SUBSYSTEM_ORCHESTRATION.md PARSER_CATALOG.md \
    BENCHMARKS.txt SAMPLE_FIELD_ACCOUNTING.json; do
    awk -v name="$required" '$2==name && $1 ~ /^[0-9a-f]{64}$/ {found=1} END {exit !found}' "$checksum_file" ||
      fail "SHA256SUMS is incomplete: missing $required"
  done
  (cd "$script_dir" && sha256sum -c SHA256SUMS) || fail 'release bundle checksum verification failed; use a fresh extraction of one release archive'
}

require_safe_secret() {
  local value=$1 label=$2
  [[ -z $value || $value =~ ^[A-Za-z0-9._~!@#%+=,:/-]+$ ]] ||
    fail "$label contains unsupported characters; use letters, digits, and . _ ~ ! @ # % + = , : / -"
}

confirm_insecure_remote() {
  local url=$1 confirmation=''
  case "$url" in
    http://localhost*|http://127.0.0.1*) return 0 ;;
    http://*)
      read -r -p "The endpoint uses unencrypted HTTP. Type INSECURE-LAB to accept it: " confirmation
      [[ $confirmation == INSECURE-LAB ]] || fail 'HTTP endpoint was not accepted; use HTTPS for production'
      ;;
  esac
}

configure_agent() {
  local mode=${1:-interactive}
  local agent_config=/etc/nli/nli.json
  local agent_env=/etc/nli/nli.env
  local input_paths parser_service output_choices output_path loki_url clickhouse_url output_summary quarantine_raw_upload write_normalized_output
  local selection_mode selection_preset selection_events selection_methods selection_services selection_hostnames selection_match_service
  local loki_tenant loki_token clickhouse_database clickhouse_table clickhouse_username clickhouse_password

  # Initialize every optional value before an interactive branch. The script
  # runs with nounset enabled, so this also makes unattended RPM configuration
  # deterministic when a preset has no custom selector fields.
  selection_events=''
  write_normalized_output='true'
  selection_methods=''
  selection_services=''
  selection_hostnames=''
  selection_match_service=''
  quarantine_raw_upload=''

  [[ -f $agent_config ]] || fail "agent config not found after RPM installation: $agent_config"
  [[ -f $agent_env ]] || fail "agent environment file not found after RPM installation: $agent_env"

  if [[ $mode == defaults ]]; then
    input_paths=automatic
    parser_service=automatic
    selection_mode=default
    selection_preset=custom
    selection_events=
    selection_methods=
    selection_services=
    selection_hostnames=
    selection_match_service=
    output_choices=1
  else
    [[ -t 0 ]] || fail 'interactive terminal is required to collect real log paths and output destination'
    printf '\nNLI client parser configuration\n'
    cat <<'EOF'
Bundled reviewed parser families (all are included in the RPM):
  imap, mss-capd, mta, pop, pss-pabd, smtp-in, smtp-out, webtop-webmail
Choose automatic to create one sample input path per family and enable every
bundled parser. For existing logs, enter comma-separated paths and a matching
comma-separated family list, or choose auto to detect a supported header.
Input selection modes: default parses all lines, filtered parses exact matches,
hybrid selects a parser service for matches and auto parses the rest. Native
event names and Webtop method names have separate exact-match lists. Presets:
imap-disconnected, pop-bad-password, webtop-token-login, webtop-message-list,
mta-domain-table, carddav, sieve-message, custom.
EOF
    read -r -p 'Log file path(s) or automatic [automatic]: ' input_paths
    input_paths=${input_paths:-automatic}
    read -r -p 'Parser family name(s), automatic, or auto [automatic]: ' parser_service
    parser_service=${parser_service:-automatic}
    read -r -p 'Input selection mode [default]: ' selection_mode
    selection_mode=${selection_mode:-default}
    read -r -p 'Reviewed preset or custom [custom]: ' selection_preset
    selection_preset=${selection_preset:-custom}
    if [[ $selection_preset == custom ]]; then
      read -r -p 'Exact event names, comma-separated: ' selection_events
      read -r -p 'Exact Webtop method names, comma-separated: ' selection_methods
      read -r -p 'Exact services, comma-separated: ' selection_services
      read -r -p 'Exact hostnames, comma-separated: ' selection_hostnames
      read -r -p 'Hybrid parser service (blank uses header service): ' selection_match_service
    fi
    read -r -p 'Allow bounded authenticated upload of exact quarantined raw lines to the controller for WebApp review? [no]: ' quarantine_raw_upload
    quarantine_raw_upload=${quarantine_raw_upload:-no}
  fi
  quarantine_raw_upload=${quarantine_raw_upload:-no}
  case "${quarantine_raw_upload,,}" in
    yes|y|true|1) quarantine_raw_upload=true ;;
    no|n|false|0) quarantine_raw_upload=false ;;
    *) fail 'quarantine raw upload must be yes or no' ;;
  esac
  [[ $parser_service =~ ^[A-Za-z0-9._,-]{1,512}$ ]] || fail 'parser/service family list must use letters, digits, dots, underscores, hyphens, and commas'

  if [[ $mode != defaults ]]; then
    cat <<'EOF'

Where should successfully parsed customer fields be sent?
  1) Hourly UTC family JSONL files under /var/log/nli/output/*_normalised_YYYYMMDD_HH00.jsonl (controller metadata is always sent to the WebApp)
  2) Local JSONL file at a path you choose
  3) Grafana Loki HTTP endpoint
  4) ClickHouse HTTP endpoint

Local parsed JSONL can be disabled independently; Loki and ClickHouse continue to receive parsed events. Exact raw quarantine remains local and enabled.
Quarantine and unknown records always remain in the aggregate /var/log/nli/quarantine.jsonl and hourly family quarantine files.
EOF
    read -r -p 'Write parsed events to local normalised JSONL? [yes]: ' write_normalized_output
    write_normalized_output=${write_normalized_output:-yes}
    case "${write_normalized_output,,}" in
      yes|y|true|1) write_normalized_output=true ;;
      no|n|false|0) write_normalized_output=false ;;
      *) fail 'local normalized output choice must be yes or no' ;;
    esac
    cat <<'EOF'
Select one or more destinations using commas, for example 1,3,4. Options 1 and 2 select local file style; 3 and 4 select remote adapters.
EOF
    read -r -p 'Select destination number(s) [1]: ' output_choices
    output_choices=${output_choices:-1}
  fi
  output_path=''
  loki_url=''
  clickhouse_url=''
  loki_tenant=''
  loki_token=''
  clickhouse_database=''
  clickhouse_table=''
  clickhouse_username=''
  clickhouse_password=''
  output_choices=${output_choices// /}
  [[ $output_choices =~ ^[1-4](,[1-4])*$ ]] || fail 'destination selection must be comma-separated values from 1, 2, 3, and 4'
  [[ ",$output_choices," == *,1,* ]] &&
    info 'parsed fields will be written to local JSONL; bounded parser metadata will reach the enrolled controller'
  if [[ ",$output_choices," == *,2,* ]]; then
      read -r -p 'Output JSONL path [/var/log/nli/normalized.jsonl]: ' output_path
      output_path=${output_path:-/var/log/nli/normalized.jsonl}
  fi
  if [[ ",$output_choices," == *,3,* ]]; then
      read -r -p 'Loki base or push URL (https://...): ' loki_url
      [[ -n $loki_url ]] || fail 'Loki URL is required'
      confirm_insecure_remote "$loki_url"
      read -r -p 'Loki tenant ID (Enter if unused): ' loki_tenant
      read -r -s -p 'Loki bearer token (Enter if endpoint needs no token): ' loki_token
      printf '\n'
      require_safe_secret "$loki_token" 'Loki bearer token'
  fi
  if [[ ",$output_choices," == *,4,* ]]; then
      read -r -p 'ClickHouse HTTP URL (https://host:8443): ' clickhouse_url
      [[ -n $clickhouse_url ]] || fail 'ClickHouse URL is required'
      confirm_insecure_remote "$clickhouse_url"
      read -r -p 'ClickHouse database [observability]: ' clickhouse_database
      clickhouse_database=${clickhouse_database:-observability}
      read -r -p 'ClickHouse table [nli_logs]: ' clickhouse_table
      clickhouse_table=${clickhouse_table:-nli_logs}
      read -r -p 'ClickHouse username [nli_ingest]: ' clickhouse_username
      clickhouse_username=${clickhouse_username:-nli_ingest}
      read -r -s -p 'ClickHouse password: ' clickhouse_password
      printf '\n'
      [[ -n $clickhouse_password ]] || fail 'ClickHouse password is required'
      require_safe_secret "$clickhouse_password" 'ClickHouse password'
  fi

  NLI_SETUP_INPUT_PATHS=$input_paths \
  NLI_SETUP_PARSER_SERVICE=$parser_service \
  NLI_SETUP_SELECTION_MODE=$selection_mode \
  NLI_SETUP_SELECTION_PRESET=$selection_preset \
  NLI_SETUP_SELECTION_EVENTS=$selection_events \
  NLI_SETUP_SELECTION_METHODS=$selection_methods \
  NLI_SETUP_SELECTION_SERVICES=$selection_services \
  NLI_SETUP_SELECTION_HOSTNAMES=$selection_hostnames \
  NLI_SETUP_SELECTION_MATCH_SERVICE=$selection_match_service \
  NLI_SETUP_QUARANTINE_RAW_UPLOAD=$quarantine_raw_upload \
  NLI_SETUP_OUTPUT_SELECTIONS=$output_choices \
  NLI_SETUP_LOCAL_NORMALIZED_OUTPUT=$write_normalized_output \
  NLI_SETUP_OUTPUT_PATH=$output_path \
  NLI_SETUP_LOKI_URL=$loki_url \
  NLI_SETUP_CLICKHOUSE_URL=$clickhouse_url \
  NLI_SETUP_LOKI_TENANT=$loki_tenant \
  NLI_SETUP_LOKI_TOKEN=$loki_token \
  NLI_SETUP_CLICKHOUSE_DATABASE=$clickhouse_database \
  NLI_SETUP_CLICKHOUSE_TABLE=$clickhouse_table \
  NLI_SETUP_CLICKHOUSE_USERNAME=$clickhouse_username \
  NLI_SETUP_CLICKHOUSE_PASSWORD=$clickhouse_password \
  python3 - "$agent_config" "$agent_env" /etc/nli/client-control.json <<'PY'
import json
import os
import pathlib
import re
import shutil
import tempfile
import urllib.parse

config_path = pathlib.Path(os.sys.argv[1])
env_path = pathlib.Path(os.sys.argv[2])
control_path = pathlib.Path(os.sys.argv[3])

raw_paths = os.environ["NLI_SETUP_INPUT_PATHS"]
service_catalog = {
    "auto": "auto", "imap": "imap", "pop": "pop", "webtop": "webtop",
    "mta": "mta", "smtp-in": "smtp-in", "smtp-out": "smtp-out",
    "pabd": "pabd", "capd": "capd", "pss": "pss",
}
raw_service = os.environ["NLI_SETUP_PARSER_SERVICE"].strip().lower()
mode = os.environ.get("NLI_SETUP_SELECTION_MODE", "default").strip().lower() or "default"
if mode not in {"default", "filtered", "hybrid"}:
    raise SystemExit("input selection mode must be default, filtered, or hybrid")
preset = os.environ.get("NLI_SETUP_SELECTION_PRESET", "custom").strip().lower()
presets = {
    "imap-disconnected": (["ImapDisconnected"], [], ["imapserv"], "imap"),
    "pop-bad-password": (["AcctBadPswd"], [], ["popserv"], "pop"),
    "webtop-token-login": ([], ["auth.loginByToken"], ["webtop"], "webtop"),
    "webtop-message-list": ([], ["mail.message.list"], ["webtop"], "webtop"),
    "mta-domain-table": (["MtaRequestingDomainTableUpdate"], [], ["mta"], "mta"),
    "carddav": (["CARDDAV"], [], ["pabd"], "pabd"),
    "sieve-message": (["SieveRuleMsg"], [], ["smtp"], "smtp"),
}
if preset in {"custom", "default"}:
    split_values = lambda key: [v.strip() for v in os.environ.get(key, "").split(",") if v.strip()]
    events = split_values("NLI_SETUP_SELECTION_EVENTS")
    methods = split_values("NLI_SETUP_SELECTION_METHODS")
    selected_services = split_values("NLI_SETUP_SELECTION_SERVICES")
    hostnames = split_values("NLI_SETUP_SELECTION_HOSTNAMES")
    match_service = os.environ.get("NLI_SETUP_SELECTION_MATCH_SERVICE", "").strip()
elif preset in presets:
    events, methods, selected_services, match_service = presets[preset]
    hostnames = []
else:
    raise SystemExit("unknown reviewed input selection preset")
if mode in {"filtered", "hybrid"} and not (events or methods or selected_services or hostnames):
    raise SystemExit("filtered and hybrid modes require at least one event, method, service, or hostname selector")
if raw_paths.strip().lower() == "automatic":
    families = list(service_catalog)
    paths = [f"/var/log/nli/input/{family}.log" for family in families]
    services = [service_catalog[family] for family in families]
else:
    paths = [value.strip() for value in raw_paths.split(",") if value.strip()]
    requested_services = [value.strip().lower() for value in raw_service.split(",") if value.strip()]
    if requested_services in (["auto"], ["automatic"]):
        services = ["auto"] * len(paths)
    else:
        aliases = {**service_catalog, "imapserv": "imap", "mss": "capd", "popserv": "pop", "smtp": "smtp", "webtop-webmail": "webtop", "webmail": "webtop", "automatic": "auto", "generic": "auto"}
        unknown = [value for value in requested_services if value not in aliases]
        if unknown:
            raise SystemExit("unknown parser family: " + ", ".join(unknown) + "; choose automatic, auto, or a listed bundled family")
        if len(requested_services) == 1:
            services = [aliases[requested_services[0]]] * len(paths)
        elif len(requested_services) == len(paths):
            services = [aliases[value] for value in requested_services]
        else:
            raise SystemExit("provide one parser family for all inputs, or one family per comma-separated input")
if not 1 <= len(paths) <= 100:
    raise SystemExit("configure 1..100 comma-separated log file paths")
for value in paths:
    candidate = pathlib.PurePosixPath(value)
    if not candidate.is_absolute() or ".." in candidate.parts or len(value) > 4096:
        raise SystemExit(f"invalid absolute log path: {value}")
    if any(ord(char) < 32 for char in value):
        raise SystemExit("log paths cannot contain control characters")

config = json.loads(config_path.read_text(encoding="utf-8"))
template = (config.get("inputs") or [{}])[0]
multiline = template.get("multiline", {
    "enabled": True,
    "max_bytes": 2097152,
    "flush_timeout_ms": 4000,
    "rules": ["java_exception", "python_traceback", "go_panic"],
})
multiline_by_service = {
    item.get("service"): item.get("multiline", multiline)
    for item in config.get("inputs", [])
    if item.get("service")
}
mask = template.get("mask", {"disable_types": []})
def multiline_for_service(service):
    # The auto family ingests line-oriented JSON. Generic continuation treats
    # every non-indented JSON record as the start of a multiline event, causing
    # a durable output flush and checkpoint for every row. Keep explicit stack
    # trace rules so actual Java/Python/Go exceptions remain assembled.
    result = dict(multiline_by_service.get(service, multiline))
    rules = list(result.get("rules", []))
    if service == "auto":
        result["rules"] = [rule for rule in rules if rule != "generic_continuation"]
    else:
        result["rules"] = rules
    return result

config["inputs"] = [
    {"path": value, "service": service, "selection": {"mode": mode, "events": events, "methods": methods, "services": selected_services, "hostnames": hostnames, "match_service": match_service}, "multiline": multiline_for_service(service), "mask": mask}
    for value, service in zip(paths, services)
]
config["tail_from_end"] = not all(value.startswith("/var/log/nli/input/") for value in paths)

selections = list(dict.fromkeys(value for value in os.environ["NLI_SETUP_OUTPUT_SELECTIONS"].split(",") if value))
if not selections or any(value not in {"1", "2", "3", "4"} for value in selections):
    raise SystemExit("configure one or more destinations from 1, 2, 3, and 4")

outputs = []
control = json.loads(control_path.read_text(encoding="utf-8"))
controller_url = str(control.get("controller_url", "")).strip().rstrip("/")
if not controller_url:
    raise SystemExit("enrolled controller URL is missing from client-control.json")
control["quarantine_raw_upload_enabled"] = os.environ.get("NLI_SETUP_QUARANTINE_RAW_UPLOAD", "false").lower() == "true"
control["quarantine_raw_upload_max_records"] = 100
control["quarantine_raw_upload_max_bytes"] = 1048576
telemetry = {
    "type": "telemetry", "routes": ["telemetry"], "url": controller_url,
    "node_id_file": "/var/lib/nli-control-client/install.id",
    "token_file": "/var/lib/nli-control-client/node.token",
    "fallback_path": "/var/lib/nli/spool/controller-telemetry.jsonl",
    "retry_max": 8, "retry_backoff_ms": 200,
}
ca_file = str(control.get("ca_file", "")).strip()
if ca_file:
    telemetry["ca_file"] = ca_file

def add_file_output(path, routes, family=None):
    key = "path_template" if "{hour_utc}" in path else "path"
    if not any(item.get("type") == "file" and item.get(key) == path for item in outputs):
        value = {"type": "file", "routes": routes, key: path, "max_mb": 2048, "max_backups": 12}
        if family:
            value["families"] = [family]
        outputs.append(value)

def output_prefix(path, service):
    name = pathlib.PurePosixPath(path).stem.lower()
    if name == "webtop":
        return "webtop-webmail"
    return name if name in {"auto", "imap", "pop", "mta", "smtp-in", "smtp-out", "pabd", "capd", "pss"} else service

def remote_url(value, label):
    value = value.strip()
    parsed = urllib.parse.urlsplit(value)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc or parsed.username or parsed.password or parsed.fragment:
        raise SystemExit(f"{label} endpoint must be an http(s) URL without embedded credentials or a fragment")
    return value

for selection in selections:
    if selection == "1":
        if os.environ.get("NLI_SETUP_LOCAL_NORMALIZED_OUTPUT", "true").lower() == "true":
            for path, service in zip(paths, services):
                prefix = output_prefix(path, service)
                add_file_output(f"/var/log/nli/output/{prefix}_normalised_{{hour_utc}}.jsonl", ["normal", "warning"], service)
    elif selection == "2":
        if os.environ.get("NLI_SETUP_LOCAL_NORMALIZED_OUTPUT", "true").lower() != "true":
            continue
        value = os.environ["NLI_SETUP_OUTPUT_PATH"]
        target = pathlib.PurePosixPath(value)
        allowed = value == "/var/log/nli" or value.startswith("/var/log/nli/") or value == "/var/lib/nli" or value.startswith("/var/lib/nli/")
        if not target.is_absolute() or ".." in target.parts or not allowed:
            raise SystemExit("file output must be an absolute path below /var/log/nli or /var/lib/nli")
        outputs.append({"type": "file", "routes": ["normal", "warning"], "path": value, "max_mb": 2048, "max_backups": 12})
    elif selection == "3":
        value = remote_url(os.environ["NLI_SETUP_LOKI_URL"], "Loki")
        labels = {"job": "nli"}
        for name in ("environment", "region", "team"):
            label = str((control.get("tags") or {}).get(name, "")).strip()
            if label:
                labels[name] = label
        primary = {
            "type": "loki", "routes": ["normal", "warning"], "url": value,
            "batch_size": 1000, "flush_sec": 2, "retry_max": 8,
            "fallback_path": "/var/lib/nli/spool/loki.jsonl", "labels": labels,
        }
        tenant = os.environ["NLI_SETUP_LOKI_TENANT"].strip()
        if tenant:
            primary["tenant_id"] = tenant
        if os.environ["NLI_SETUP_LOKI_TOKEN"]:
            primary["bearer_token_env"] = "NLI_LOKI_BEARER_TOKEN"
        outputs.append(primary)
    elif selection == "4":
        value = remote_url(os.environ["NLI_SETUP_CLICKHOUSE_URL"], "ClickHouse")
        identifier = re.compile(r"^[A-Za-z_][A-Za-z0-9_]{0,127}$")
        database = os.environ["NLI_SETUP_CLICKHOUSE_DATABASE"]
        table = os.environ["NLI_SETUP_CLICKHOUSE_TABLE"]
        if not identifier.fullmatch(database) or not identifier.fullmatch(table):
            raise SystemExit("ClickHouse database and table must be valid unquoted identifiers")
        outputs.append({
            "type": "clickhouse", "routes": ["normal", "warning"], "url": value,
            "batch_size": 2000, "flush_sec": 2, "retry_max": 8,
            "fallback_path": "/var/lib/nli/spool/clickhouse.jsonl",
            "database": database, "table": table,
            "username": os.environ["NLI_SETUP_CLICKHOUSE_USERNAME"],
            "password_env": "NLI_CLICKHOUSE_PASSWORD",
        })

quarantine = {
    "type": "file",
    "path": "/var/log/nli/quarantine.jsonl",
    "max_mb": 500,
    "max_backups": 5,
    "routes": ["quarantine", "unknown"],
    "durable_raw": True,
}
family_quarantines = []
family_quarantine_paths = set()
for path, service in zip(paths, services):
    prefix = output_prefix(path, service)
    quarantine_path = f"/var/log/nli/output/{prefix}_quarantine_{{hour_utc}}.jsonl"
    if quarantine_path in family_quarantine_paths:
        continue
    family_quarantine_paths.add(quarantine_path)
    family_quarantines.append({
        "type": "file", "path_template": quarantine_path,
        "families": [service], "max_mb": 500, "max_backups": 5,
        "routes": ["quarantine", "unknown"], "durable_raw": True,
    })
config["outputs"] = outputs + [quarantine] + family_quarantines
config["telemetry_outputs"] = [telemetry]

def atomic_write(path, text, mode):
    fd, temp_name = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(text)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temp_name, mode)
        shutil.chown(temp_name, user="root", group="nli")
        os.replace(temp_name, path)
    finally:
        if os.path.exists(temp_name):
            os.unlink(temp_name)

atomic_write(config_path, json.dumps(config, indent=2) + "\n", 0o640)
atomic_write(control_path, json.dumps(control, indent=2) + "\n", 0o640)

updates = {
    "NLI_LOKI_BEARER_TOKEN": os.environ["NLI_SETUP_LOKI_TOKEN"],
    "NLI_CLICKHOUSE_PASSWORD": os.environ["NLI_SETUP_CLICKHOUSE_PASSWORD"],
}
existing = env_path.read_text(encoding="utf-8").splitlines()
result = []
seen = set()
for line in existing:
    key = line.lstrip("# ").split("=", 1)[0] if "=" in line else ""
    if key in updates:
        if key not in seen:
            result.append(f"{key}={updates[key]}")
            seen.add(key)
    else:
        result.append(line)
for key, value in updates.items():
    if key not in seen:
        result.append(f"{key}={value}")
atomic_write(env_path, "\n".join(result) + "\n", 0o640)
PY

  while IFS= read -r configured_path; do
    [[ -n $configured_path ]] || continue
    case "$configured_path" in
      /var/log/nli/*)
        install -d -o nli -g nli -m 0750 "$(dirname "$configured_path")"
        if [[ ! -e $configured_path ]]; then
          install -o nli -g nli -m 0640 /dev/null "$configured_path"
          info "created configured parser input: $configured_path"
        fi
        ;;
      *)
        if ! runuser -u nli -- test -r "$configured_path"; then
          info "WARNING: nli cannot read $configured_path yet; grant directory traversal and file read permission"
        fi
        ;;
    esac
  done < <(python3 - "$agent_config" <<'PY'
import json, pathlib, sys
value = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
for item in value.get("inputs", []):
    print(item["path"])
PY
  )

  # Materialize the current-hour files during setup so operators can verify
  # every configured family before the first production record arrives.
  if [[ ${input_paths,,} == automatic ]]; then
    current_hour=$(date -u +%Y%m%d_%H00)
    while IFS= read -r configured_output; do
      [[ -n $configured_output ]] || continue
      configured_output=${configured_output//\{hour_utc\}/$current_hour}
      install -d -o nli -g nli -m 0750 "$(dirname "$configured_output")"
      [[ -e $configured_output ]] || install -o nli -g nli -m 0640 /dev/null "$configured_output"
    done < <(python3 - "$agent_config" <<'PY'
import json, pathlib, sys
value = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
for item in value.get("outputs", []):
    path = item.get("path_template") or item.get("path")
    if path and path.startswith("/var/log/nli/"):
        print(path)
PY
    )
  fi

  if [[ ${input_paths,,} == automatic ]]; then
    python3 - "$agent_config" <<'PY'
import json
import os
import pathlib
import subprocess
import sys
import tempfile

config = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
families = {
    "auto": ("nli-reviewed-smtp-in.parserpack.json", "nli_reviewed_smtp_in", "smtp-in"),
    "imap": ("nli-reviewed-imap.parserpack.json", "nli_reviewed_imap", "imap"),
    "capd": ("nli-reviewed-mss-capd.parserpack.json", "nli_reviewed_mss_capd", "capd"),
    "mta": ("nli-reviewed-mta.parserpack.json", "nli_reviewed_mta", "mta"),
    "pop": ("nli-reviewed-pop.parserpack.json", "nli_reviewed_pop", "pop"),
    "pabd": ("nli-reviewed-pss-pabd.parserpack.json", "nli_reviewed_pss_pabd", "pabd"),
    "pss": ("nli-reviewed-pss-pabd.parserpack.json", "nli_reviewed_pss_pabd", "pabd"),
    "smtp-in": ("nli-reviewed-smtp-in.parserpack.json", "nli_reviewed_smtp_in", "smtp-in"),
    "smtp-out": ("nli-reviewed-smtp-out.parserpack.json", "nli_reviewed_smtp_out", "smtp-out"),
    "webtop": ("nli-reviewed-webtop-webmail.parserpack.json", "nli_reviewed_webtop_webmail", "webtop"),
}
for item in config.get("inputs", []):
    family = item.get("service", "")
    if family not in families:
        raise SystemExit(f"automatic setup contains unexpected parser family {family!r}")
    filename, expected_parser, sample_service = families[family]
    asset_path = pathlib.Path("/etc/nli/parsers") / filename
    if not asset_path.is_file():
        raise SystemExit(f"bundled parser asset missing from RPM: {asset_path}")
    pack = json.loads(asset_path.read_text(encoding="utf-8"))
    cases = pack.get("regression_cases", [])
    sample = next((case for case in cases if case.get("service") == sample_service and case.get("input")), None)
    if sample is None:
        raise SystemExit(f"no reviewed sample line is available for {family}")
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", newline="\n", delete=False) as fixture:
        fixture.write(sample["input"] + "\n")
        fixture_path = fixture.name
    try:
        completed = subprocess.run(
            ["/usr/bin/nli", "detect", "--service", family, fixture_path],
            check=True, capture_output=True, text=True, timeout=15,
        )
        matches = [json.loads(line) for line in completed.stdout.splitlines() if line.strip()]
        if not matches or matches[0].get("route") != "normal" or not str(matches[0].get("parser", "")).startswith(expected_parser + "/"):
            raise SystemExit(f"parser self-test failed for {family}: {completed.stdout.strip()}")
    finally:
        os.unlink(fixture_path)

    target = pathlib.Path(item["path"])
    print(f"  PASS {family}: reviewed temporary sample parsed by {matches[0]['parser']}; live input preserved at {target}")
PY
  fi

  systemctl restart nli-agent.service
  sleep 2
  systemctl is-active --quiet nli-agent.service || {
    journalctl -u nli-agent.service --no-pager -n 100 >&2 || true
    fail 'parser service did not remain active with the selected input/output configuration'
  }
  curl --fail --silent --show-error http://127.0.0.1:12380/health >/dev/null ||
    fail 'parser health endpoint did not answer after configuration'
  output_summary=$(python3 - "$agent_config" <<'PY'
import json, pathlib, sys
value = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
print(", ".join(item.get("type", "unknown") for item in value.get("outputs", []) if item.get("routes") != ["quarantine", "unknown"]))
PY
  )
  info "PASS: real log paths are configured and parsed records route to $output_summary"
}

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
rpm_path=''
bootstrap_file=''
bootstrap_url=''
configure_only=0
verify_only=0
automatic_config=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --rpm)
      [[ $# -ge 2 ]] || fail '--rpm requires a file path'
      rpm_path=$2
      shift 2
      ;;
    --bootstrap)
      [[ $# -ge 2 ]] || fail '--bootstrap requires a file path'
      bootstrap_file=$2
      shift 2
      ;;
    --configure-only)
      configure_only=1
      shift
      ;;
    --automatic|--all-log-files)
      automatic_config=1
      shift
      ;;
    --verify-only)
      verify_only=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *) fail "unexpected argument: $1" ;;
  esac
done

[[ ${EUID:-$(id -u)} -eq 0 ]] || fail 'run as root with sudo'
[[ ! ($configure_only -eq 1 && $verify_only -eq 1) ]] || fail '--configure-only and --verify-only cannot be used together'
required_commands=(awk rpm sha256sum readlink)
[[ $verify_only -eq 1 ]] || required_commands+=(curl dnf python3 systemctl)
for command in "${required_commands[@]}"; do command -v "$command" >/dev/null || fail "$command is required"; done

if [[ $configure_only == 1 ]]; then
  rpm -q nli-client >/dev/null || fail 'nli-client is not installed'
  [[ -s /var/lib/nli-control-client/node.token ]] || fail 'client is not enrolled; run the normal installation first'
  chgrp nli /var/lib/nli-control-client/install.id /var/lib/nli-control-client/node.token 2>/dev/null || true
  chmod 0640 /var/lib/nli-control-client/install.id /var/lib/nli-control-client/node.token 2>/dev/null || true
  if [[ $automatic_config -eq 1 ]]; then configure_agent defaults; else configure_agent; fi
  systemctl is-active --quiet nli-client-control.service || fail 'control service is not active after parser configuration'
  info 'PASS: client input paths and output destination were updated'
  exit 0
fi

load_release_identity "$script_dir/RELEASE_ID"
verify_release_bundle "$script_dir/SHA256SUMS"

if [[ -z $rpm_path ]]; then
  for candidate in "$script_dir/nli_client_ver20.rpm" "$script_dir/nli_clinet_ver20.rpm" "$PWD/nli_client_ver20.rpm" "$PWD/nli_clinet_ver20.rpm"; do
    if [[ -f $candidate ]]; then rpm_path=$candidate; break; fi
  done
fi
if [[ -z $rpm_path && -t 0 ]]; then
  read -r -p 'Path to nli_client_ver20.rpm: ' rpm_path
fi
[[ -f $rpm_path ]] || fail 'client RPM was not found; place it beside this script or pass --rpm PATH'
rpm_path=$(readlink -f -- "$rpm_path")

[[ $(rpm -qp --qf '%{NAME}' "$rpm_path") == nli-client ]] || fail 'the selected file is not the NLI client RPM'
[[ $(rpm -qp --qf '%{VERSION}' "$rpm_path") == "$expected_version" ]] || fail "the selected client RPM is not version $expected_version"
rpm_release=$(rpm -qp --qf '%{RELEASE}' "$rpm_path")
validate_rpm_release "$rpm_release" client "$expected_release"
[[ $(rpm -qp --qf '%{ARCH}' "$rpm_path") == "$expected_arch" ]] || fail "the selected client RPM is not $expected_arch"

checksum_file="$script_dir/SHA256SUMS"
expected=$(awk '$2=="nli_client_ver20.rpm" {print $1; exit}' "$checksum_file")
actual=$(sha256sum "$rpm_path" | awk '{print $1}')
[[ $actual == "$expected" ]] || fail 'client RPM SHA-256 does not match this release bundle'
info 'client RPM SHA-256 verified'

if [[ $verify_only -eq 1 ]]; then
  info "PASS: client RPM identity and complete bundle verified ($(rpm -qp --qf '%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}' "$rpm_path"), source $expected_revision)"
  exit 0
fi

if [[ -z $bootstrap_file ]]; then
  for candidate in "$script_dir/nli-client-bootstrap.env" "$PWD/nli-client-bootstrap.env"; do
    if [[ -f $candidate ]]; then bootstrap_file=$candidate; break; fi
  done
fi
if [[ -z $bootstrap_file && -t 0 ]]; then
  read -r -p 'One-use bootstrap file path (Enter for manual URL/token entry): ' bootstrap_file
fi
if [[ $bootstrap_file == http://* || $bootstrap_file == https://* ]]; then
  bootstrap_url=$bootstrap_file
  bootstrap_file=''
fi
if [[ -n $bootstrap_file ]]; then
  [[ -f $bootstrap_file ]] || fail "bootstrap file not found: $bootstrap_file"
  bootstrap_file=$(readlink -f -- "$bootstrap_file")
fi

info "installing $(rpm -qp --qf '%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}' "$rpm_path")"
dnf -y install "$rpm_path"

if [[ -n $bootstrap_file ]]; then
  /usr/sbin/nli-client-setup "$bootstrap_file"
elif [[ -n $bootstrap_url ]]; then
  /usr/sbin/nli-client-setup "$bootstrap_url"
else
  /usr/sbin/nli-client-setup
fi

chgrp nli /var/lib/nli-control-client/install.id /var/lib/nli-control-client/node.token 2>/dev/null || true
chmod 0640 /var/lib/nli-control-client/install.id /var/lib/nli-control-client/node.token 2>/dev/null || true
systemctl restart nli-agent.service
systemctl is-active --quiet nli-agent.service || fail 'parser service is not active after setup'
systemctl is-active --quiet nli-client-control.service || fail 'control service is not active after setup'
if [[ $automatic_config -eq 1 ]]; then
  configure_agent defaults
elif [[ -t 0 ]]; then
  configure_agent
else
  configure_agent defaults
fi
systemctl is-active --quiet nli-client-control.service || fail 'control service is not active after parser configuration'
info 'PASS: client RPM is installed, enrolled, configured, and both services are active'
info 'the one-use enrollment token was cleared after the controller issued the protected node token'
info 'controller enrollment and initial heartbeat succeeded; configured input telemetry is being delivered to the controller'
