# Vercel deployment

Vercel serves the static web application and its same-origin `/api/v1/*` serverless proxy. `NLI_CONTROLLER_ORIGIN` is the only required Vercel variable. It must be a public HTTPS origin routed through a Cloudflare public hostname to the controller. Do not include a path, credentials, query, or fragment. The proxy rejects localhost, private IP literals, and local/internal hostnames; run the preflight to check public DNS resolution as well.

The browser calls the Vercel app at its own origin. Vercel forwards the authenticated request headers and cookies to the controller, so the browser does not make cross-origin requests to the controller and does not need a permissive CORS policy. The controller login `OPTIONS` response is checked to ensure Cloudflare preserves the API preflight contract. Do not put Supabase keys, controller secrets, enrollment tokens, or any other credentials in Vercel environment variables.

## GitHub import

1. Push the repository to a private GitHub repository and import it in Vercel. Set the project root to the repository root; keep the configured `node` version at 24.x. `vercel.json` supplies the build command and static output directory.
2. In Project Settings → Environment Variables, add `NLI_CONTROLLER_ORIGIN` for **Production** only (add Preview or Development only when those deployments should reach a controller). Use the exact public Cloudflare hostname that routes to the controller, for example `https://nli.example.net`.
3. From a trusted workstation with Node 24, run `npm run vercel:preflight` after setting that value in the process environment or in ignored `.env.local`. This validates public DNS, the Cloudflare edge response, controller health, and login `OPTIONS` without sending credentials.
4. Push to the configured production branch or use Vercel's Deploy action. Re-run the preflight after changing the controller hostname or its Cloudflare route.

To create a dedicated deployment repository without copying the controller or client implementation, run `npm run web:export`. The script writes a minimal source tree to `build/web-only` with the supported Vite preset, the same-origin Vercel function, and a generated `DEPLOYMENT.md`. That document describes the static output and the serverless `/api/v1/*` forwarding contract for other hosts; use one shared `web/` UI and add a host-specific function adapter only when the target platform requires one.

## CLI deployment

Install and authenticate the Vercel CLI, then link the project and set the production-scoped variable. Keep the local origin in ignored `.env.local` (copy `.env.local.example`; do not commit `.env.local`). The script below loads that file for preflight, but does not read or print credentials.

```powershell
Copy-Item .env.local.example .env.local
# Edit .env.local and enter the public Cloudflare HTTPS controller origin.
npm run vercel:preflight
vercel login
vercel link
vercel env add NLI_CONTROLLER_ORIGIN production
npm run build
npm run web:check
vercel deploy --prod
```

Vercel environment changes apply to new deployments. After changing `NLI_CONTROLLER_ORIGIN`, redeploy production; do not assume an existing deployment will pick up the new value. Scope the variable only to the environments that are intended to use the controller. Avoid putting the value into a client-prefixed variable or static build output.

## Verify deployment

After deployment, check the public controller first, then the Vercel proxy:

```powershell
npm run vercel:preflight -- --app-url https://your-app.vercel.app
```

The preflight checks the controller's `/api/v1/health` identity and service payload, confirms the hostname response carries Cloudflare's `CF-RAY`, verifies `/api/v1/auth/login` responds to `OPTIONS` with `POST`, `OPTIONS`, `X-NLI-Controller`, and `Cache-Control: no-store`, and optionally checks the deployed Vercel `/api/v1/health` same-origin proxy headers. It prints only pass/fail diagnostics; it never prints response bodies, cookies, or authorization headers. A successful check means the configured public route is reaching the expected V20 controller API contract.

### Cloudflare Access caveat

Cloudflare Access policies that require an interactive browser identity or an Access service token will block Vercel's server-to-server proxy. NLI browser login cookies are not Cloudflare Access credentials. Configure the controller hostname so its health, login, and authenticated API routes are reachable from Vercel through the intended public Cloudflare route, while relying on NLI's own authentication and RBAC. Do not weaken unrelated Cloudflare policies to make a check pass; use an approved route/policy for this application.
