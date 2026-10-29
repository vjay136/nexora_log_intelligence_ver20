#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
ARTIFACT_DIR=${1:?usage: validate-two-node-webview.sh ARTIFACT_DIR}
: "${NLI_BUILD_SCRATCH:?NLI_BUILD_SCRATCH is required}"
# shellcheck disable=SC1091
source "$ROOT/packaging/docker/images.env"
ARTIFACT_DIR=$(cd "$ARTIFACT_DIR" && pwd)
controller_rpm="$ARTIFACT_DIR/nli_controller_agent_ver20.rpm"
client_rpm="$ARTIFACT_DIR/nli_client_ver20.rpm"
[[ -s $controller_rpm && -s $client_rpm ]] || { echo 'two-node acceptance: release RPMs are missing' >&2; exit 1; }

docker_path() {
  if [[ -n ${MSYSTEM:-} ]] && command -v cygpath >/dev/null 2>&1; then
    cygpath -am "$1"
  else
    printf '%s\n' "$1"
  fi
}

# Prevent Git Bash from rewriting container paths such as /artifacts and /run.
docker() {
  MSYS_NO_PATHCONV=1 command docker "$@"
}

DOCKER_ARTIFACT_DIR=$(docker_path "$ARTIFACT_DIR")
DOCKER_ROCKY_DIR=$(docker_path "$ROOT/tests/rocky")
DOCKER_ROCKYFILE=$(docker_path "$ROOT/tests/rocky/Dockerfile")

suffix="${$}-$(date +%s)"
network="nli-v20-acceptance-$suffix"
controller="nli-v20-controller-$suffix"
client="nli-v20-client-$suffix"
discovery_controller="nli-v20-discovery-$suffix"
fallback_controller="nli-v20-fallback-$suffix"
bootstrap_volume="nli-v20-bootstrap-$suffix"
work=$(mktemp -d "$NLI_BUILD_SCRATCH/nli-two-node.XXXXXXXX")
mkdir -p "$work/browser"
image='nli-rocky-test:rocky10'
admin_password='Release3-Browser-Admin-2026!'

cleanup() {
  docker rm -f "$client" "$controller" "$discovery_controller" "$fallback_controller" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
  docker volume rm "$bootstrap_volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT

wait_for_systemd() {
  local container=$1 state=''
  for _ in $(seq 1 60); do
    state=$(docker exec "$container" systemctl is-system-running 2>/dev/null || true)
    [[ $state == running || $state == degraded ]] && return 0
    sleep 1
  done
  docker logs "$container" >&2 || true
  return 1
}

start_node() {
  local name=$1 hostname=$2
  docker run -d --name "$name" --hostname "$hostname" --network "$network" \
    --privileged --cgroupns=host --tmpfs /run --tmpfs /run/lock \
    --mount "type=bind,src=$DOCKER_ARTIFACT_DIR,dst=/artifacts,readonly" \
    --mount "type=volume,src=$bootstrap_volume,dst=/bootstrap" \
    "$image" >/dev/null
  wait_for_systemd "$name" || { echo "two-node acceptance: systemd did not start in $name" >&2; exit 1; }
}

docker build --pull=false --build-arg "BASE_IMAGE=$ROCKY10_IMAGE" -t "$image" -f "$DOCKER_ROCKYFILE" "$DOCKER_ROCKY_DIR"
docker network create "$network" >/dev/null
docker volume create "$bootstrap_volume" >/dev/null
start_node "$controller" controller
start_node "$client" client

docker exec "$controller" dnf -y install /artifacts/nli_controller_agent_ver20.rpm >/dev/null
docker exec -i "$controller" python3 - <<'PY'
import json, pathlib
path = pathlib.Path('/etc/nli/controller.json')
value = json.loads(path.read_text(encoding='utf-8'))
value['storage_provider'] = 'local'
# The packaged browser acceptance suite traverses every workspace in rapid
# succession. Keep the production limiter enabled while sizing this isolated
# controller high enough that the test measures UI/API contracts rather than
# exhausting the fixture's anonymous and authenticated request budget.
value['rate_limit'] = 5000
path.write_text(json.dumps(value, indent=2, sort_keys=True) + '\n', encoding='utf-8')
PY
docker exec -e NLI_TEST_ADMIN_PASSWORD="$admin_password" "$controller" bash -c \
  'umask 077; printf "%s\n" "$NLI_TEST_ADMIN_PASSWORD" > /root/nli-admin-password'
docker exec -e NLI_SETUP_STORAGE_PROVIDER=local -e NLI_SETUP_QUARANTINE_RAW_RETENTION=false \
  -e NLI_SETUP_MODE=2 -e NLI_SETUP_ENDPOINT=controller \
  -e NLI_SETUP_ADMIN_PASSWORD_FILE=/root/nli-admin-password \
  -e NLI_SETUP_TLS_IDENTITIES=controller.internal.test \
  -e NLI_CLIENT_BOOTSTRAP_FILE=/bootstrap/nli-client-bootstrap.env "$controller" /usr/sbin/nli-controller-setup

docker exec "$controller" test -s /bootstrap/nli-client-bootstrap.env
docker exec "$controller" systemctl is-active --quiet nli-controller-agent.service
docker exec "$controller" openssl verify -CAfile /var/lib/nli-controller/tls/ca.crt /var/lib/nli-controller/tls/server.crt
docker exec "$controller" openssl x509 -in /var/lib/nli-controller/tls/server.crt -noout -checkhost controller
docker exec "$controller" openssl x509 -in /var/lib/nli-controller/tls/server.crt -noout -checkhost controller.internal.test
docker exec "$controller" openssl x509 -in /var/lib/nli-controller/tls/server.crt -noout -checkhost localhost
docker exec "$controller" openssl x509 -in /var/lib/nli-controller/tls/server.crt -noout -checkip 127.0.0.1
ca_before_rotation=$(docker exec "$controller" sha256sum /var/lib/nli-controller/tls/ca.crt | awk '{print $1}')
docker exec "$controller" grep -Fxq 'Description=Nexora Log Intelligence - Main Controller' /usr/lib/systemd/system/nli-controller-agent.service
docker exec "$controller" sh -c '! grep -q "^Documentation=" /usr/lib/systemd/system/nli-controller-agent.service'

docker exec "$client" dnf -y install /artifacts/nli_client_ver20.rpm >/dev/null
docker exec "$client" sh -c 'cp /bootstrap/nli-client-bootstrap.env /root/nli-client-bootstrap.env && chmod 0600 /root/nli-client-bootstrap.env'
printf 'production\nrocky10\noperations\n' | docker exec -i "$client" /usr/sbin/nli-client-setup /root/nli-client-bootstrap.env
docker exec "$client" test ! -e /root/nli-client-bootstrap.env
docker exec "$client" test -s /var/lib/nli-control-client/node.token
for input_name in auto imap pop webtop mta smtp-in smtp-out pabd capd pss; do
  docker exec "$client" test -f "/var/log/nli/input/${input_name}.log"
done
for output_family in auto imap pop webtop-webmail mta smtp-in smtp-out pabd capd pss; do
  docker exec "$client" sh -c "! ls /var/log/nli/output/${output_family}_normalised_[0-9]*.jsonl >/dev/null 2>&1"
  docker exec "$client" sh -c "! ls /var/log/nli/output/${output_family}_quarantine_[0-9]*.jsonl >/dev/null 2>&1"
done
docker exec "$client" systemctl is-active --quiet nli-agent.service
docker exec "$client" systemctl is-active --quiet nli-client-control.service
docker exec "$client" grep -Fxq 'Description=Nexora Log Intelligence - Client Agent' /usr/lib/systemd/system/nli-agent.service
docker exec "$client" grep -Fxq 'Description=Nexora Log Intelligence - Client' /usr/lib/systemd/system/nli-client-control.service
docker exec "$client" sh -c '! grep -q "^Documentation=" /usr/lib/systemd/system/nli-agent.service'
docker exec "$client" sh -c '! grep -q "^Documentation=" /usr/lib/systemd/system/nli-client-control.service'
docker exec -i "$client" python3 - <<'PY'
import json, pathlib
value = json.loads(pathlib.Path('/etc/nli/client-control.json').read_text(encoding='utf-8'))
assert value['controller_url'] == 'https://controller:12382'
assert value['ca_file'] == '/etc/nli/controller-ca.crt'
assert value['allow_insecure_http'] is False
PY
docker exec "$client" curl --fail --silent --show-error --cacert /etc/nli/controller-ca.crt https://controller:12382/api/v1/health >/dev/null

replay_status=$(docker exec "$client" bash -c '
  token=$(sed -n "s/^NLI_ENROLLMENT_TOKEN=//p" /bootstrap/nli-client-bootstrap.env)
  curl --silent --show-error --cacert /etc/nli/controller-ca.crt -o /tmp/replayed-enrollment.json -w "%{http_code}" \
    -H "Content-Type: application/json" -H "X-NLI-Enrollment: ${token}" \
    -d "{\"id\":\"replayed-node\",\"hostname\":\"replayed-node\",\"os\":\"linux\",\"arch\":\"x86_64\",\"version\":\"20.0.0\"}" \
    https://controller:12382/api/v1/node/register
')
[[ $replay_status == 401 ]] || { echo "two-node acceptance: consumed enrollment token returned HTTP $replay_status" >&2; exit 1; }

printf '2\ncontroller\n' | docker exec -i -e NLI_SETUP_STORAGE_PROVIDER=local -e NLI_CLIENT_BOOTSTRAP_FILE=/bootstrap/nli-client-bootstrap.env \
  -e NLI_SETUP_QUARANTINE_RAW_RETENTION=false -e NLI_SETUP_TLS_IDENTITIES=controller.internal.test "$controller" /usr/sbin/nli-controller-setup --rotate-enrollment
ca_after_rotation=$(docker exec "$controller" sha256sum /var/lib/nli-controller/tls/ca.crt | awk '{print $1}')
[[ $ca_before_rotation == "$ca_after_rotation" ]] || { echo 'two-node acceptance: enrollment rotation changed the private CA' >&2; exit 1; }
rotated_status=$(docker exec "$client" bash -c '
  token=$(sed -n "s/^NLI_ENROLLMENT_TOKEN=//p" /bootstrap/nli-client-bootstrap.env)
  curl --silent --show-error --cacert /etc/nli/controller-ca.crt -o /tmp/rotated-enrollment.json -w "%{http_code}" \
    -H "Content-Type: application/json" -H "X-NLI-Enrollment: ${token}" \
    -d "{\"id\":\"rotated-node\",\"hostname\":\"rotated-node\",\"os\":\"linux\",\"arch\":\"x86_64\",\"version\":\"20.0.0\"}" \
    https://controller:12382/api/v1/node/register
')
[[ $rotated_status == 201 ]] || { echo "two-node acceptance: rotated enrollment token returned HTTP $rotated_status" >&2; exit 1; }

# Prove the separately installed client appears online through the authenticated controller API.
docker exec -e NLI_TEST_ADMIN_PASSWORD="$admin_password" "$controller" bash -c '
  set -e
  curl --fail --silent --show-error --cacert /var/lib/nli-controller/tls/ca.crt \
    --resolve controller:12382:127.0.0.1 -c /tmp/nli.cookies -H "Content-Type: application/json" \
    -d "{\"username\":\"admin\",\"password\":\"${NLI_TEST_ADMIN_PASSWORD}\"}" \
    https://controller:12382/api/v1/auth/login >/dev/null
  curl --fail --silent --show-error --cacert /var/lib/nli-controller/tls/ca.crt \
    --resolve controller:12382:127.0.0.1 -b /tmp/nli.cookies \
    https://controller:12382/api/v1/fleet >/tmp/fleet.json
'
docker exec -i "$controller" python3 - <<'PY'
import json, pathlib
value = json.loads(pathlib.Path('/tmp/fleet.json').read_text(encoding='utf-8'))
items = value if isinstance(value, list) else value.get('nodes', [])
assert any(item.get('hostname') == 'client' and item.get('status') == 'online' for item in items), value
PY

# Trust the generated controller CA and run every browser workspace against the
# exact HTML/CSS/JS files served from the installed controller RPM.
docker run --rm --network "$network" \
  --mount "type=bind,src=$ROOT/web,dst=/source-web,readonly" \
  --mount "type=bind,src=$work/browser,dst=/work" \
  --mount "type=volume,src=$bootstrap_volume,dst=/bootstrap,readonly" \
  -e NLI_BASE_URL=https://controller:12382 \
  -e NLI_ADMIN_PASSWORD="$admin_password" \
  -e NLI_E2E_OUTPUT=/work/results \
  -e NLI_E2E_IGNORE_HTTPS_ERRORS=1 \
  mcr.microsoft.com/playwright:v1.64.1-noble bash -lc '
    set -euo pipefail
    sed -n "s/^NLI_CONTROLLER_CA_BASE64=//p" /bootstrap/nli-client-bootstrap.env | base64 -d >/usr/local/share/ca-certificates/nli-controller.crt
    update-ca-certificates >/dev/null
    cp /source-web/package.json /source-web/playwright.config.js /source-web/e2e.spec.js /work/
    cd /work
    npm install --ignore-scripts --no-audit --no-fund >/dev/null
    npx playwright test
  '

# Exercise automatic TLS discovery with real OpenSSL fixtures. First prove that
# a trusted server leaf plus matching key is selected. Then put only
# CA/client/entitlement materials on another node and prove they are ignored in
# favor of the offline generated-CA fallback.
start_node "$discovery_controller" discovery-controller
docker exec "$discovery_controller" dnf -y install /artifacts/nli_controller_agent_ver20.rpm >/dev/null
docker exec -i "$discovery_controller" bash -s <<'SH'
set -euo pipefail
mkdir -p /etc/pki/tls/certs /etc/pki/tls/private /etc/pki/ca-trust/source/anchors
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out /tmp/nli-test-ca.key >/dev/null 2>&1
openssl req -x509 -new -sha256 -days 30 -key /tmp/nli-test-ca.key -subj '/CN=NLI fixture root' \
  -addext 'basicConstraints=critical,CA:TRUE' -addext 'keyUsage=critical,keyCertSign,cRLSign' \
  -out /etc/pki/ca-trust/source/anchors/nli-fixture-root.crt
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out /etc/pki/tls/private/nli-fixture-server.key >/dev/null 2>&1
openssl req -new -sha256 -key /etc/pki/tls/private/nli-fixture-server.key \
  -subj '/CN=discovery-controller.internal.test' -out /tmp/nli-fixture-server.csr
cat >/tmp/nli-fixture-server.ext <<'EOF'
subjectAltName=DNS:discovery-controller.internal.test
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
EOF
openssl x509 -req -sha256 -days 20 -in /tmp/nli-fixture-server.csr \
  -CA /etc/pki/ca-trust/source/anchors/nli-fixture-root.crt -CAkey /tmp/nli-test-ca.key \
  -CAcreateserial -extfile /tmp/nli-fixture-server.ext -out /etc/pki/tls/certs/nli-fixture-server.crt
update-ca-trust extract
umask 077
printf '%s\n' 'NLI Fixture Admin Password 2026!' >/root/nli-admin-password
SH
docker exec -e NLI_SETUP_MODE=8 -e NLI_SETUP_ENDPOINT=discovery-controller.internal.test \
  -e NLI_SETUP_ADMIN_PASSWORD_FILE=/root/nli-admin-password \
  -e NLI_SETUP_QUARANTINE_RAW_RETENTION=false \
  -e NLI_CLIENT_BOOTSTRAP_FILE=/bootstrap/discovery-bootstrap.env \
  "$discovery_controller" /usr/sbin/nli-controller-setup
docker exec "$discovery_controller" cmp /etc/pki/tls/certs/nli-fixture-server.crt /var/lib/nli-controller/tls/discovered-server.crt
docker exec "$discovery_controller" cmp /etc/pki/tls/private/nli-fixture-server.key /var/lib/nli-controller/tls/discovered-server.key
docker exec "$discovery_controller" grep -Fxq 'NLI_CONTROLLER_CA_BASE64=' /bootstrap/discovery-bootstrap.env
docker exec "$discovery_controller" test ! -e /var/lib/nli-controller/tls/ca.key

start_node "$fallback_controller" fallback-controller
docker exec "$fallback_controller" dnf -y install /artifacts/nli_controller_agent_ver20.rpm >/dev/null
docker exec -i "$fallback_controller" bash -s <<'SH'
set -euo pipefail
mkdir -p /etc/rhsm/ca /etc/pki/entitlement /etc/nli
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out /tmp/nli-client-root.key >/dev/null 2>&1
openssl req -x509 -new -sha256 -days 30 -key /tmp/nli-client-root.key -subj '/CN=Katello fixture trust anchor' \
  -addext 'basicConstraints=critical,CA:TRUE' -addext 'keyUsage=critical,keyCertSign,cRLSign' \
  -out /etc/rhsm/ca/katello-server-ca.pem
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out /etc/nli/consumer.key >/dev/null 2>&1
openssl req -new -sha256 -key /etc/nli/consumer.key -subj '/CN=consumer entitlement client' -out /tmp/nli-consumer.csr
cat >/tmp/nli-consumer.ext <<'EOF'
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=clientAuth
EOF
openssl x509 -req -sha256 -days 20 -in /tmp/nli-consumer.csr -CA /etc/rhsm/ca/katello-server-ca.pem \
  -CAkey /tmp/nli-client-root.key -CAcreateserial -extfile /tmp/nli-consumer.ext -out /etc/pki/entitlement/consumer.pem
cp /etc/pki/entitlement/consumer.pem /etc/nli/consumer.pem
umask 077
printf '%s\n' 'NLI Fixture Admin Password 2026!' >/root/nli-admin-password
SH
docker exec -e NLI_SETUP_MODE=8 -e NLI_SETUP_ENDPOINT=fallback-controller \
  -e NLI_SETUP_ADMIN_PASSWORD_FILE=/root/nli-admin-password \
  -e NLI_SETUP_QUARANTINE_RAW_RETENTION=false \
  -e NLI_CLIENT_BOOTSTRAP_FILE=/bootstrap/fallback-bootstrap.env \
  "$fallback_controller" /usr/sbin/nli-controller-setup
docker exec "$fallback_controller" test -s /var/lib/nli-controller/tls/ca.key
docker exec "$fallback_controller" test -s /var/lib/nli-controller/tls/server.crt
docker exec "$fallback_controller" grep -Eq '^NLI_CONTROLLER_CA_BASE64=.{100,}$' /bootstrap/fallback-bootstrap.env

echo 'two-node TLS, automatic TLS discovery/fallback, and packaged WebView acceptance: PASS (Rocky Linux 10.2)'
