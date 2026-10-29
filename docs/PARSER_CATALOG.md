# NLI V20 client parser catalog

The client RPM bundles these eight reviewed ParserPacks in its parser catalog.
The runtime embeds and enables all eight by default; selecting a family on an
input narrows routing to that family, while `auto` detects supported headers
per event. Managed, approved ParserPacks synchronized by the controller are
loaded alongside the reviewed packs.

| ParserPack file | Installer family | Accepted aliases | Suggested input | Sample line shape |
| --- | --- | --- | --- | --- |
| `nli-reviewed-imap.parserpack.json` | `imap` | `imap`, `imapserv` | `/var/log/nli/input/imap.log` | `20261010 123456789+0530 mail01 IMAP ...` |
| `nli-reviewed-mss-capd.parserpack.json` | `mss-capd` | `mss-capd`, `mss`, `capd` | `/var/log/nli/input/mss-capd.log` | `20261010 123456789+0530 mail01 CAPD ...` |
| `nli-reviewed-mta.parserpack.json` | `mta` | `mta` | `/var/log/nli/input/mta.log` | `20261010 123456789+0530 mail01 MTA ...` |
| `nli-reviewed-pop.parserpack.json` | `pop` | `pop`, `popserv` | `/var/log/nli/input/pop.log` | `20261010 123456789+0530 mail01 POP ...` |
| `nli-reviewed-pss-pabd.parserpack.json` | `pss-pabd` | `pss-pabd`, `pss`, `pabd` | `/var/log/nli/input/pss-pabd.log` | `20261010 123456789+0530 mail01 PABD ...` |
| `nli-reviewed-smtp-in.parserpack.json` | `smtp-in` | `smtp-in`, `smtp` | `/var/log/nli/input/smtp-in.log` | `20261010 123456789+0530 rgi01 SMTP policy=inbound ...` |
| `nli-reviewed-smtp-out.parserpack.json` | `smtp-out` | `smtp-out`, `smtp` | `/var/log/nli/input/smtp-out.log` | `20261010 123456789+0530 rgo01 SMTP policy=outbound ...` |
| `nli-reviewed-webtop-webmail.parserpack.json` | `webtop-webmail` | `webtop-webmail`, `webtop`, `webmail` | `/var/log/nli/input/webtop-webmail.log` | `2026-10-10 15:20:11:123+0530 user=... action=...` |

Run the guided installer and enter `automatic` for log paths to create all
eight suggested files and associate each with its family parser. Those empty
files are examples; replace them with real inputs or edit the client config
before expecting production records. To use existing files, enter one or more
absolute paths separated by commas. Enter one parser family to apply it to all
paths, or provide one comma-separated family per path. Use `auto` for each path
to infer supported families from record headers. Use `automatic` for paths to
create all eight sample inputs. Parser selection does not remove bundled packs.

Examples:

```text
Paths: automatic
Families: automatic
```

```text
Paths: /var/log/mail/imap.log,/var/log/mail/mta.log
Families: imap,mta
```

```text
Paths: /var/log/mail/all-services.log
Families: auto
```

List the installed pack files with `ls -1 /etc/nli/parsers/*.parserpack.json`.
The complete catalog is installed at `/usr/share/doc/nli-client/PARSER_CATALOG.md`.

Unmatched or ambiguous records are retained locally in
`/var/log/nli/quarantine.jsonl` with exact raw text, source position, a stable
record ID, event hash, parser revision, error codes, and attempt status. A
controller retry uses the current approved parser generation, sends successful
fields to the configured file/Loki/ClickHouse destinations, and removes only
resolved rows from the active queue. Attempt history is kept in
`/var/log/nli/quarantine.jsonl.history.jsonl` without raw text. Raw upload to the
controller is opt-in during setup, authenticated with the enrolled node token,
and bounded to 100 records / 1 MiB per poll by default; the normal telemetry
stream never contains raw lines.
