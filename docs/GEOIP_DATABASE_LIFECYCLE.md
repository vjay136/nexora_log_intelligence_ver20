# V20 GeoIP database lifecycle

NLI performs GeoIP lookups only from local MaxMind DB (`.mmdb`) files. A log parse never calls GitHub, Supabase, or a geolocation API. The client uses the configured exact files:

```text
/etc/geoip-db/GeoLite2-Country.mmdb
/etc/geoip-db/GeoLite2-ASN.mmdb
/etc/geoip-db/GeoLite2-City.mmdb
```

If a configured file is absent, parsing continues and GeoIP fields are omitted. A present but invalid database is reported at agent startup. The MMDB reader already used by the parser validates each candidate before activation.

## Source and licensing

The optional updater reads the `download` branch in [P3TERX/GeoLite.mmdb](https://github.com/P3TERX/GeoLite.mmdb), which republishes MaxMind GeoLite2 Country, City, and ASN files. It is a community mirror, not an official MaxMind endpoint. The upstream README lists the [GeoLite2 EULA](https://www.maxmind.com/en/geolite2/eula) and [CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/); both terms must be reviewed for the organization's use and redistribution. NLI does not bundle the databases.

An update is pinned to one 40-character source commit. The updater downloads only the three fixed database filenames using HTTPS from `api.github.com` and `raw.githubusercontent.com`, rejects redirects, enforces a per-file size limit, calculates SHA-256, and checks that the MMDB's declared database type matches its filename. The manifest stores the source repository, commit, observed HTTP metadata, checksums, file sizes, license links, attribution, and fetch time. SHA-256 detects change or corruption; the source does not provide a separate signed per-file checksum, so candidates remain reviewable before use.

## Candidate and activation flow

The controller RPM includes the operational command `nli geoip-update` and the
disabled-by-default `nli-geoip-update.timer`. The command calls
`geoipdb.FetchCandidate` with `/var/lib/nli-geoip` as its default state root. It
creates a temporary generation under `/var/lib/nli-geoip/generations`, validates
all three files, writes `manifest.json`, then renames the complete generation
into place. An interrupted or invalid download cannot become a candidate.

Fetching creates a candidate only. It does not upload the database or change client files. Before activation, an administrator reviews its source commit, license metadata, and hashes. `geoipdb.ValidateGeneration` repeats the manifest, checksum, and MMDB checks. `geoipdb.Activate` requires a nonempty approving administrator identity, then atomically replaces each exact configured database path with a symlink to the immutable generation. The previous activated generation is kept and recorded in `/var/lib/nli-geoip/activation.json`; `geoipdb.Rollback` switches those paths back after revalidation. The agent should be restarted only after all three links have been switched.

Activation refuses an existing database directory that is itself a symlink, any pre-existing ordinary file at a database path, and any symlink that does not point into the managed generations directory. This protects manually installed databases and stops the updater from following arbitrary filesystem links. For a first managed install, move manually provisioned files to a separately retained backup and activate only after the paths are clear. A second activation retains the prior managed generation for rollback.

## Optional Supabase candidate archive

NLI's ordinary artifact bucket is reserved for signed RPM and configuration rollouts. GeoIP data uses its separate private `nli-geoip-databases` bucket and content-addressed paths. The Supabase schema provisions the bucket as private with a 128 MiB object limit. The controller-only Storage credential is used; browser code and clients must never receive that credential.

`geoipdb.PublishCandidate` requires explicit license acceptance and the admin
username. It first revalidates the candidate, uploads each file by SHA-256 path,
and saves a `geoip-candidates` document with source/version/hash/license and
attribution metadata. The saved document is `pending_approval`, says
`candidate_only=true`, and sets `auto_rollout=false`.

To enable scheduled candidate checks, review the licenses, edit the root-owned
`/etc/nli/geoip.env`, and set at least:

```text
NLI_GEOIP_LICENSE_ACCEPTED=true
NLI_GEOIP_OPERATOR=admin-username
```

Set `NLI_GEOIP_PUBLISH_SUPABASE=true` only on a controller already configured
with `SUPABASE_URL`, a publishable/anon key, `NLI_SUPABASE_CONTROLLER_SECRET`,
and the server-only `SUPABASE_SECRET_KEY`. Set
`NLI_GEOIP_AUTO_ACTIVATE=true` only if this controller itself parses logs and
the administrator accepts automatic local activation. Then run:

```bash
sudo systemctl enable --now nli-geoip-update.timer
sudo systemctl start nli-geoip-update.service
sudo journalctl -u nli-geoip-update.service --no-pager
```

The timer checks once per day with a randomized delay. Supabase upload is
opt-in. Client delivery remains approval-gated: download the reviewed immutable
generation through the organization's artifact rollout, verify its manifest and
hashes on each client, run
`sudo NLI_GEOIP_OPERATOR=admin-username nli geoip-activate --generation COMMIT`,
and restart `nli-agent`. Use `nli geoip-rollback` to restore the previous managed
generation after revalidation. V20 does
not silently push newly mirrored databases to clients or treat a third-party
mirror change as production approval.

For environments that disallow internet access on the controller, download and validate a candidate in an approved staging environment, transfer the complete candidate folder and manifest through the enterprise artifact process, then independently verify hashes and stage it locally. No per-event network request is made.
