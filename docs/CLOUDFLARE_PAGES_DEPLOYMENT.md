# Cloudflare Pages deployment

This is an additional hosted-WebApp path. The controller remains on the AWS
Red Hat node. A named Cloudflare Tunnel publishes the controller at one stable
DNS hostname, and Cloudflare Pages serves the static WebApp plus its
same-origin `/api/v1/*` Pages Function proxy.

The two public names must be distinct, for example:

```text
Controller tunnel: https://nli-controller.example.com
Cloudflare Pages:  https://nli.example.com
```

## Git-connected deployment

1. Create a Cloudflare Pages project from the private V20 GitHub repository.
2. Set the production branch to `main`.
3. Use build command `node scripts/build-webapp.mjs` and output directory
   `build/web-public`. Keep the repository root as the root directory.
4. In **Settings > Variables and Secrets**, add the runtime variable
   `NLI_CONTROLLER_ORIGIN=https://nli-controller.example.com` for Production.
   Do not add `/api/v1` or a trailing slash.
5. Do not add Supabase secrets, controller secrets, enrollment tokens, tunnel
   tokens, TLS keys, or AI provider keys to Pages. Those remain on the
   controller.
6. Deploy. The repository's `functions/api/v1/[[path]].js` is the Pages
   Function proxy; `wrangler.jsonc` declares the static output directory.

After changing the environment variable, create a new deployment so the
Function receives the new value.

## Verification

Confirm the tunnel first:

```bash
curl -fsS https://nli-controller.example.com/api/v1/health
```

Then confirm the Pages proxy:

```bash
curl -fsS https://nli.example.com/api/v1/health
```

The Pages response must include `X-NLI-Upstream: controller` and
`X-NLI-Controller: v20`. A redirect, Cloudflare Access login page, 403, or HTML
response means the Pages Function cannot reach the API contract. Configure an
approved Cloudflare route that permits the server-to-server proxy while NLI's
own authentication and RBAC protect application operations.

The browser calls Pages on the same origin, so no permissive cross-origin
browser policy is required. Auth cookies, CSRF headers, bounded uploads, normal
API requests, and SSE telemetry are forwarded by the Function. Raw WebSocket
streaming remains available only when opening the controller hostname directly;
the hosted app uses SSE and bounded polling fallbacks.
