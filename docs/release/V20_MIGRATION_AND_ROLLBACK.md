# Ver20 migration and rollback note

## Release identity

Ver20 release 3 is version `20.0.0-3`, Go module `nexora.local/nli-v20`, and WebView root `/usr/share/nli/webview_ver20`. The release archive, RPM aliases, web bundles, installer filenames, and CI release tag use the Ver20 identity. Ver20 release 2 is the required release-to-release upgrade and rollback baseline; Ver19 release 8 remains the supported major-version migration baseline.

## Upgrade path

1. Keep the exact Ver20 release 2 client and controller RPMs and its verified release archive available. Confirm the Release 3 archive checksums before extraction.
2. Back up `/etc/nli`, `/var/lib/nli`, `/var/lib/nli-control-client`, and `/var/lib/nli-controller` using the site's approved protected backup process. Preserve TLS material, credentials, node enrollment state, parser assets, and audit-chain keys.
3. Use the Release 3 installer and RPMs. Do not remove the existing packages first; the RPM package names and service identities remain stable so the package manager performs an in-place upgrade.
4. Verify RPM versions, systemd service health, controller health/API identity, client enrollment, configured input/output paths, and WebView access. Verify representative parsing/output and review quarantine before declaring completion.

## Rollback

If service or compatibility checks fail, stop the affected NLI services, reinstall the saved Ver20 Release 2 client/controller RPMs with the site's normal package manager downgrade procedure, then start services and verify health, enrollment, and data paths. Keep the Release 3 archive and logs for diagnosis.

The RPM downgrade restores executable files but does not automatically reverse configuration or database changes. Before rollback, compare the saved `/etc/nli` backup and database schema/state with the Release 2 baseline. Restore a database backup only when the migration actually changed persistent schema and the approved recovery plan requires it; coordinate client/controller rollback so their protocol and parser revisions remain compatible. Never purge `/var/lib/nli`, secrets, certificates, parser assets, or audit history as part of rollback.

## Evidence boundary

This note describes the release identity and recovery procedure. It is not proof that a Release 3 build or Release 2-to-Release 3 upgrade passed. Record exact RPM hashes, source revision, test results, and upgrade/rollback outcomes in the release evidence before publishing.
