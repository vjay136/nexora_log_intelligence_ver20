#!/usr/bin/env python3
"""Exercise SELinux edge-boolean capture, rollback, and purge preservation."""

import json
import os
import pathlib
import subprocess
import sys
import tempfile

root = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else pathlib.Path(__file__).parents[2])
setup = (root / "scripts/setup-controller-rocky.sh").read_text(encoding="utf-8")
uninstall = (root / "scripts/uninstall-controller-ver20.sh").read_text(encoding="utf-8")


def function(source, name):
    marker = name + "() {\n"
    start = source.index(marker)
    end = source.index("\n}\n", start) + 3
    return source[start:end]


def run_bash(body):
    result = subprocess.run(
        [os.environ.get("NLI_TEST_BASH", "bash"), "-c", body],
        text=True,
        capture_output=True,
    )
    if result.returncode:
        raise SystemExit(
            "SELinux fixture failed with exit "
            f"{result.returncode}: stdout={result.stdout!r} stderr={result.stderr!r}"
        )
    return result


def bash_path(path):
    value = str(path)
    if os.name == "nt":
        drive, rest = os.path.splitdrive(value)
        return f"/{drive[0].lower()}{rest.replace(chr(92), '/')}"
    return value


read_boolean = function(setup, "read_selinux_proxy_boolean")
enable_boolean = function(setup, "enable_selinux_proxy_boolean")
restore_boolean = function(setup, "restore_selinux_proxy_boolean")

mock_tools = r'''
mock_current=${mock_current:-off}
mock_persistent=${mock_persistent:-off}
getenforce() { printf 'Enforcing\n'; }
getsebool() { printf 'httpd_can_network_connect --> %s\n' "$mock_current"; }
semanage() { printf 'httpd_can_network_connect (%s , %s) Allow mocked\n' "$mock_current" "$mock_persistent"; }
setsebool() {
  local persistent=0 name value
  if [[ $1 == -P ]]; then persistent=1; shift; fi
  name=$1; value=$2
  [[ $name == httpd_can_network_connect ]] || return 8
  mock_current=$value
  if [[ $persistent == 1 ]]; then mock_persistent=$value; fi
}
fail() { printf 'FAIL: %s\n' "$*" >&2; return 1; }
'''

for original_current, original_persistent in (("off", "off"), ("on", "off"), ("off", "on")):
    script = "\n".join(
        [
            mock_tools,
            f"mock_current={original_current}; mock_persistent={original_persistent}",
            "selinux_proxy_managed=false; selinux_proxy_changed_this_run=0",
            "selinux_setup_prior_current=''; selinux_setup_prior_persistent=''",
            read_boolean,
            enable_boolean,
            restore_boolean,
            "enable_selinux_proxy_boolean",
            '[[ $mock_current == on && $mock_persistent == on ]] || exit 10',
            "restore_selinux_proxy_boolean",
            f'[[ $mock_current == {original_current} && $mock_persistent == {original_persistent} ]] || exit 11',
            "printf '%s %s\\n' \"$mock_current\" \"$mock_persistent\"",
        ]
    )
    result = run_bash(script)
    if result.stdout.strip() != f"{original_current} {original_persistent}":
        raise SystemExit(f"SELinux rollback did not restore {original_current}/{original_persistent}")

# A later admin change must win over the setup rollback.
script = "\n".join(
    [mock_tools, "mock_current=off; mock_persistent=off", "selinux_proxy_managed=false; selinux_proxy_changed_this_run=0",
     "selinux_setup_prior_current=''; selinux_setup_prior_persistent=''", read_boolean, enable_boolean,
     restore_boolean, "enable_selinux_proxy_boolean", "mock_current=off", "restore_selinux_proxy_boolean",
     '[[ $mock_current == off && $mock_persistent == on ]] || exit 12',
     "printf '%s %s\\n' \"$mock_current\" \"$mock_persistent\""]
)
if run_bash(script).stdout.strip() != "off on":
    raise SystemExit("SELinux rollback overwrote a later administrator change")

purge_function = function(uninstall, "restore_nli_selinux_boolean_on_purge")
with tempfile.TemporaryDirectory() as directory:
    manifest = pathlib.Path(directory) / "controller-edge.json"
    purge_function = purge_function.replace("/etc/nli/controller-edge.json", bash_path(manifest))
    base = "\n".join([mock_tools, "purge_confirmed=1", "info() { :; }", "command() { builtin command \"$@\"; }",
                      "command -v getenforce >/dev/null", "command -v getsebool >/dev/null", "command -v semanage >/dev/null", "command -v setsebool >/dev/null"])
    for admin_changed, expected in ((False, "off off"), (True, "off on")):
        manifest.write_text(json.dumps({
            "selinux_proxy_boolean_changed": True,
            "selinux_proxy_boolean_prior_current": "off",
            "selinux_proxy_boolean_prior_persistent": "off",
        }), encoding="utf-8")
        state = "mock_current=on; mock_persistent=on"
        if admin_changed:
            state = "mock_current=off; mock_persistent=on"
        script = "\n".join([base, state, purge_function, "restore_nli_selinux_boolean_on_purge",
                             "printf '%s %s\\n' \"$mock_current\" \"$mock_persistent\""])
        if run_bash(script).stdout.strip() != expected:
            raise SystemExit("purge did not safely restore/preserve SELinux boolean state")

print("controller edge SELinux rollback tests: PASS")
