# NLI Ver20 support matrix

Updated: 2026-10-28

This matrix separates locally measured results from release gates that require
Linux containers or real Rocky/RHEL hosts.

| Area | Intended support | Current Ver20 evidence |
|---|---|---|
| Upgrade source | Released Ver18 `8a084b8` | Source baseline identified; in-place package lifecycle awaits RPM gate |
| Client/controller OS | x86_64 RHEL/Rocky 8, 9, 10 | Full Go, vet, and race suites pass in the pinned Linux container; Rocky lifecycle remains a release-CI gate |
| Controller access | private TLS, corporate certificate, Nginx/ACME, existing HTTPS proxy, Cloudflare Tunnel | Static and contract checks pass; deployment remains environment-specific |
| Hosted WebApp | Vercel and Cloudflare Pages through same-origin proxy | Core and Vercel-mirror Vite builds pass; 24 deployment, proxy, documentation, and export contracts pass |
| Browser | current Chromium-class browsers | R8 includes 61 Playwright Chromium scenarios covering every primary workspace, visible inner tab, and the guided quarantine lifecycle; final clean release execution is recorded in the R8 test report |
| Parser evolution | NUPIRE discovery, declarative compilation, held-out evaluation, shadow, approval, canary, promotion, drift, rollback | Unit, API, persistence, UI, and fuzz checks pass |
| Production rollout | existing signed, independently approved parser rollout | Preserved; NUPIRE promotion cannot bypass it |
| Supabase | protected metadata and explicitly retained encrypted artifacts | Existing Ver18 contract retained; secrets remain controller-only |
| Arbitrary unknown logs | lossless quarantine and explicit partial/abstain outcomes | The nine-stage wizard maps retained evidence, blocks unresolved values, and produces review-gated declarative candidates; universal semantic accuracy is not claimed |

Release status is recorded in [V20_ACCEPTANCE_CHECKLIST.md](V20_ACCEPTANCE_CHECKLIST.md).
