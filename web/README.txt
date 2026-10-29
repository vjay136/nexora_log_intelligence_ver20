Nexora Log Intelligence Ver20 Enterprise WebView
=========================

This archive contains the authenticated frontend embedded in
nli-controller-agent plus a Windows access launcher. The HTML files are not a
standalone control plane: login, all 24 workspaces, RBAC, CSRF, live metrics,
WebSockets, and Supabase-backed metadata require the same-origin controller API.

Do not host index.html with Python http.server, IIS static hosting, or another
static server and attempt to sign in. Static copies are visual assets only;
credentials remain disabled because those servers cannot handle
POST /api/v1/auth/login.

Controller setup for <controller-private-ip>
----------------------------------
1. Install the Ver20 controller RPM on Rocky Linux.
2. Run `sudo nli-controller-setup`.
3. Choose mode `2`.
4. Enter controller IP `<controller-private-ip>`.
5. Accept the packaged Supabase project URL or enter the required project URL.
6. Press Enter to keep the packaged anon/public key, or paste a replacement.
7. If requested, run `/root/nli-supabase-bootstrap.sql` once in Supabase SQL
   Editor and return to the setup prompt.

The schema denies direct anon/authenticated table access. The public project key
is accepted only together with the controller-only secret generated on Rocky.
That secret is never sent to the browser or stored in Supabase plaintext.

Recommended Windows access
--------------------------
For direct private-TLS access, install/trust the generated controller CA and
open:

   https://<controller-private-ip>:12382/

For an SSH tunnel, extract this ZIP and run:

   powershell -ExecutionPolicy Bypass -File .\Open-NLI-WebView.ps1

The launcher verifies the NLI Ver20 health/login contract before enabling
credentials.

Managed clients
---------------
The controller writes `/root/nli-client-bootstrap.env`, valid for exactly one
successful registration. Copy it securely to one client and run:

   sudo nli-client-setup /path/to/nli-client-bootstrap.env

Before each additional client, rotate the bundle on the controller:

   sudo nli-controller-setup --rotate-enrollment

Initial administrator
---------------------
Username: admin
Password: the value selected during first controller setup.

If the password is unavailable, run:

   sudo nli-admin-reset

This preserves all users and rotates only the admin password. Accounts and RBAC
are managed through **Users, RBAC & Audit**. Supabase stores bounded controller
metadata; it does not bypass NLI authentication.

Assurance Lab
-------------
Assurance uses the same production ParserPack pipeline as clients. The complete
static prefix and KV tail, source spans, ordered parser attempts, and stage
results remain visible until **Clear manually** is selected.

The Live Dashboard charts aggregated ingest, parsed, quarantine, latency,
processing error, and AI request metrics. The Assurance Lab accepts individual
log/text samples and supported archives, offers a mixed-format corpus workflow,
and provides links to revisioned regex, known-key, projection, template, and
semantic extraction policies. Network identity, URL, email address, and RFC
2047 encoded-subject engine pages show examples and report controller-supported
capability state. Encoded MIME subjects are decoded; they are not decrypted.

**Encrypt and retain in Supabase** stores the fixture as controller-encrypted
ciphertext. Retained rows stay until an authorized operator selects **Purge**.
Use **Download .log** to retrieve the decrypted original through the
controller's authenticated endpoint.

Fleet topology
--------------
Install the controller RPM on one management node or a designed HA tier. Install
the client RPM on each managed/log-producing node. Install both only when the
controller host also processes local logs.
