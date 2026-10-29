#!/usr/bin/env python3
"""Exercise the self-contained client input/output configurator."""

import json
import os
import pathlib
import shutil
import sys
import tempfile


ROOT = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else pathlib.Path(__file__).parents[2])
INSTALLER = ROOT / "scripts" / "install-client-ver20.sh"
START = '  python3 - "$agent_config" "$agent_env" /etc/nli/client-control.json <<\'PY\'\n'


def configurator_source():
    text = INSTALLER.read_text(encoding="utf-8").replace("\r\n", "\n")
    begin = text.index(START) + len(START)
    end = text.index("\nPY\n", begin)
    return text[begin:end]


def run_case(output_selections, **values):
    with tempfile.TemporaryDirectory() as directory:
        root = pathlib.Path(directory)
        config = root / "nli.json"
        env_file = root / "nli.env"
        control = root / "client-control.json"
        shutil.copyfile(ROOT / "configs" / "nli.json", config)
        shutil.copyfile(ROOT / "configs" / "nli.env", env_file)
        control.write_text(json.dumps({
            "controller_url": "https://controller.internal:12382",
            "ca_file": "/etc/nli/tls/controller-ca.crt",
            "tags": {"environment": "test", "region": "lab", "team": "qa"},
        }), encoding="utf-8")

        environment = {
            "NLI_SETUP_INPUT_PATHS": values.get("input_paths", "/var/log/app/one.log, /var/log/app/two.log"),
            "NLI_SETUP_PARSER_SERVICE": values.get("parser_service", "auto"),
            "NLI_SETUP_SELECTION_MODE": values.get("selection_mode", "default"),
            "NLI_SETUP_SELECTION_PRESET": values.get("selection_preset", "custom"),
            "NLI_SETUP_SELECTION_EVENTS": values.get("selection_events", ""),
            "NLI_SETUP_SELECTION_SERVICES": values.get("selection_services", ""),
            "NLI_SETUP_SELECTION_HOSTNAMES": values.get("selection_hostnames", ""),
            "NLI_SETUP_SELECTION_MATCH_SERVICE": values.get("selection_match_service", ""),
            "NLI_SETUP_QUARANTINE_RAW_UPLOAD": values.get("raw_upload", "false"),
            "NLI_SETUP_LOCAL_NORMALIZED_OUTPUT": values.get("local_normalized", "true"),
            "NLI_SETUP_OUTPUT_SELECTIONS": output_selections,
            "NLI_SETUP_OUTPUT_PATH": values.get("path", ""),
            "NLI_SETUP_LOKI_URL": values.get("loki_url", values.get("url", "")),
            "NLI_SETUP_CLICKHOUSE_URL": values.get("clickhouse_url", values.get("url", "")),
            "NLI_SETUP_LOKI_TENANT": values.get("tenant", ""),
            "NLI_SETUP_LOKI_TOKEN": values.get("loki_token", ""),
            "NLI_SETUP_CLICKHOUSE_DATABASE": values.get("database", ""),
            "NLI_SETUP_CLICKHOUSE_TABLE": values.get("table", ""),
            "NLI_SETUP_CLICKHOUSE_USERNAME": values.get("username", ""),
            "NLI_SETUP_CLICKHOUSE_PASSWORD": values.get("password", ""),
        }
        old_argv = sys.argv[:]
        old_environment = os.environ.copy()
        original_chown = shutil.chown
        try:
            sys.argv = ["configure-client", str(config), str(env_file), str(control)]
            os.environ.update(environment)
            shutil.chown = lambda *args, **kwargs: None
            exec(compile(configurator_source(), str(INSTALLER), "exec"), {"__name__": "__main__"})
        finally:
            sys.argv = old_argv
            os.environ.clear()
            os.environ.update(old_environment)
            shutil.chown = original_chown

        return (
            json.loads(config.read_text(encoding="utf-8")),
            env_file.read_text(encoding="utf-8"),
            json.loads(control.read_text(encoding="utf-8")),
        )


def main():
    installer_text = INSTALLER.read_text(encoding="utf-8")
    setup_text = (ROOT / "scripts" / "setup-client-rocky.sh").read_text(encoding="utf-8")
    assert "--automatic|--all-log-files" in installer_text
    assert "configured_output=${configured_output//\\{hour_utc\\}/$current_hour}" in installer_text
    assert 'with target.open("a"' not in installer_text
    assert "nli-v20-sample" not in setup_text

    config, env_file, control = run_case("1")
    assert [item["path"] for item in config["inputs"]] == [
        "/var/log/app/one.log",
        "/var/log/app/two.log",
    ]
    assert [item["service"] for item in config["inputs"]] == ["auto", "auto"]
    assert config["tail_from_end"] is True
    assert all(item["selection"]["mode"] == "default" for item in config["inputs"])
    assert config["outputs"][0]["path_template"] == "/var/log/nli/output/auto_normalised_{hour_utc}.jsonl"
    assert config["outputs"][0]["families"] == ["auto"]
    assert config["outputs"][1]["path"] == "/var/log/nli/quarantine.jsonl"
    assert config["outputs"][1]["routes"] == ["quarantine", "unknown"]
    assert config["outputs"][1]["durable_raw"] is True
    assert config["telemetry_outputs"][0]["url"] == "https://controller.internal:12382"
    assert config["telemetry_outputs"][0]["token_file"] == "/var/lib/nli-control-client/node.token"
    assert config["telemetry_outputs"][0]["node_id_file"] == "/var/lib/nli-control-client/install.id"
    assert config["telemetry_outputs"][0]["ca_file"] == "/etc/nli/tls/controller-ca.crt"
    assert "NLI_LOKI_BEARER_TOKEN=\n" in env_file
    assert config["telemetry_outputs"][0]["routes"] == ["telemetry"]
    assert control["quarantine_raw_upload_enabled"] is False
    assert control["quarantine_raw_upload_max_records"] == 100
    assert control["quarantine_raw_upload_max_bytes"] == 1048576

    config, _, _ = run_case("1", input_paths="automatic", parser_service="automatic")
    assert [item["service"] for item in config["inputs"]] == [
        "auto", "imap", "pop", "webtop", "mta", "smtp-in", "smtp-out", "pabd", "capd", "pss"
    ]
    assert "generic_continuation" not in config["inputs"][0]["multiline"]["rules"]
    assert "generic_continuation" in config["inputs"][1]["multiline"]["rules"]
    assert [item["path"].rsplit("/", 1)[-1] for item in config["inputs"]] == [
        "auto.log", "imap.log", "pop.log", "webtop.log", "mta.log",
        "smtp-in.log", "smtp-out.log", "pabd.log", "capd.log", "pss.log",
    ]
    assert [item["path_template"].rsplit("/", 1)[-1] for item in config["outputs"][:10]] == [
        "auto_normalised_{hour_utc}.jsonl", "imap_normalised_{hour_utc}.jsonl", "pop_normalised_{hour_utc}.jsonl",
        "webtop-webmail_normalised_{hour_utc}.jsonl", "mta_normalised_{hour_utc}.jsonl",
        "smtp-in_normalised_{hour_utc}.jsonl", "smtp-out_normalised_{hour_utc}.jsonl",
        "pabd_normalised_{hour_utc}.jsonl", "capd_normalised_{hour_utc}.jsonl", "pss_normalised_{hour_utc}.jsonl",
    ]
    config, _, control = run_case("1", input_paths="/var/log/mail/imap.log,/var/log/mail/mta.log", parser_service="imap,mta", raw_upload="true")
    assert [item["service"] for item in config["inputs"]] == ["imap", "mta"]
    assert control["quarantine_raw_upload_enabled"] is True

    config, _, _ = run_case("3", loki_url="https://loki.example.com", local_normalized="false")
    assert not any(item.get("type") == "file" and "normal" in item.get("routes", []) for item in config["outputs"])
    assert any(item.get("type") == "file" and item.get("durable_raw") is True for item in config["outputs"])

    config, _, _ = run_case("1", input_paths="/var/log/mail/imap.log", selection_mode="filtered", selection_preset="imap-disconnected")
    assert config["inputs"][0]["selection"] == {
        "mode": "filtered", "events": ["ImapDisconnected"], "methods": [], "services": ["imapserv"],
        "hostnames": [], "match_service": "imap",
    }
    config, _, _ = run_case("1", input_paths="/var/log/mail/custom.log", selection_mode="hybrid", selection_events="CARDDAV,AcctBadPswd", selection_services="pabd,popserv", selection_hostnames="mail01,mail02", selection_match_service="auto")
    assert config["inputs"][0]["selection"]["events"] == ["CARDDAV", "AcctBadPswd"]
    assert config["inputs"][0]["selection"]["services"] == ["pabd", "popserv"]

    config, _, _ = run_case("2", path="/var/log/nli/test-output.jsonl")
    assert config["outputs"][0]["path"] == "/var/log/nli/test-output.jsonl"

    config, env_file, _ = run_case(
        "3", loki_url="https://loki.example.com", tenant="tenant-a", loki_token="safe-token.1"
    )
    assert config["outputs"][0]["bearer_token_env"] == "NLI_LOKI_BEARER_TOKEN"
    assert config["outputs"][0]["labels"]["environment"] == "test"
    assert "NLI_LOKI_BEARER_TOKEN=safe-token.1\n" in env_file
    assert "safe-token.1" not in json.dumps(config)

    config, env_file, _ = run_case(
        "4",
        clickhouse_url="https://clickhouse.example.com:8443",
        database="observability",
        table="nli_logs",
        username="nli_ingest",
        password="safe-password.1",
    )
    assert config["outputs"][0]["database"] == "observability"
    assert config["outputs"][0]["password_env"] == "NLI_CLICKHOUSE_PASSWORD"
    assert "NLI_CLICKHOUSE_PASSWORD=safe-password.1\n" in env_file
    assert "safe-password.1" not in json.dumps(config)

    config, env_file, _ = run_case(
        "1,2,3,4",
        path="/var/log/nli/mirror.jsonl",
        loki_url="https://loki.example.com/loki/api/v1/push",
        tenant="tenant-a",
        loki_token="safe-token.1",
        clickhouse_url="https://clickhouse.example.com:8443",
        database="observability",
        table="nli_logs",
        username="nli_ingest",
        password="safe-password.1",
    )
    assert [item["type"] for item in config["outputs"]] == [
        "file", "file", "loki", "clickhouse", "file", "file"
    ]
    assert config["telemetry_outputs"][0]["retry_max"] == 8
    assert config["outputs"][2]["fallback_path"] == "/var/lib/nli/spool/loki.jsonl"
    assert config["outputs"][3]["fallback_path"] == "/var/lib/nli/spool/clickhouse.jsonl"
    assert all(item["routes"] == ["quarantine", "unknown"] for item in config["outputs"][-2:])
    assert "NLI_LOKI_BEARER_TOKEN=safe-token.1\n" in env_file
    assert "NLI_CLICKHOUSE_PASSWORD=safe-password.1\n" in env_file
    print("client installer configurator tests: PASS")


if __name__ == "__main__":
    main()
