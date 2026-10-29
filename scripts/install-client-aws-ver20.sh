#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fail() { printf 'nli-aws-client: %s\n' "$*" >&2; exit 1; }
[[ $# -ge 2 && $# -le 3 ]] || fail 'usage: sudo bash install-client-aws-ver20.sh /path/to/nli_client_ver20.rpm /secure/path/nli-client-bootstrap.env [--automatic]'
rpm_path=$1 bootstrap=$2 automatic=${3:-}
[[ -f $rpm_path ]] || fail "RPM not found: $rpm_path"
[[ -f $bootstrap && -s $bootstrap ]] || fail "bootstrap file missing or empty: $bootstrap"
[[ $(stat -c '%a' "$bootstrap") == 600 || $(stat -c '%a' "$bootstrap") == 400 ]] || fail 'bootstrap file must be mode 0600 or 0400; run chmod 600 on the file'
[[ -z $automatic || $automatic == --automatic ]] || fail 'third argument must be --automatic'
"$SCRIPT_DIR/aws-rhel-preflight-ver20.sh"
args=(--rpm "$rpm_path" --bootstrap "$bootstrap")
[[ $automatic != --automatic ]] || args+=(--automatic)
bash "$SCRIPT_DIR/install-client-ver20.sh" "${args[@]}"
