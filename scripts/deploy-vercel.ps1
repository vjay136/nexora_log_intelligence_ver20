[CmdletBinding()]
param([string]$ControllerOrigin = '')

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$Root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path

function Invoke-Checked {
    param([string]$Command, [string[]]$Arguments)
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Command failed with exit code $LASTEXITCODE" }
}

function Get-LocalDotEnvValue {
    param([string]$Path, [string]$Name)
    if (-not (Test-Path -LiteralPath $Path)) { return '' }
    $escapedName = [Regex]::Escape($Name)
    foreach ($line in Get-Content -LiteralPath $Path) {
        if ($line -match "^\s*$escapedName\s*=\s*(.*?)\s*$") {
            $value = $Matches[1].Trim()
            if ($value.Length -ge 2 -and (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'")))) {
                $value = $value.Substring(1, $value.Length - 2)
            }
            return $value
        }
    }
    return ''
}

function Get-ControllerOriginHelp {
    @'
Vercel requires an EXISTING public HTTPS origin for the NLI controller.

The value must be a public Cloudflare hostname whose HTTPS route forwards to the Rocky controller at https://127.0.0.1:12382.
Do not use localhost, 192.168.x.x, nli-controller.example.com, a Supabase project URL, or the future Vercel application URL.

For local-only access, keep the controller private, use an SSH tunnel, and do not run this Vercel deployment script.
'@
}

function Test-NonPublicAddress {
    param([System.Net.IPAddress]$Address)

    if ([System.Net.IPAddress]::IsLoopback($Address)) { return $true }

    if ($Address.AddressFamily -eq [System.Net.Sockets.AddressFamily]::InterNetwork) {
        $octets = $Address.GetAddressBytes()
        return (
            $octets[0] -eq 0 -or
            $octets[0] -eq 10 -or
            $octets[0] -eq 127 -or
            ($octets[0] -eq 100 -and $octets[1] -ge 64 -and $octets[1] -le 127) -or
            ($octets[0] -eq 169 -and $octets[1] -eq 254) -or
            ($octets[0] -eq 172 -and $octets[1] -ge 16 -and $octets[1] -le 31) -or
            ($octets[0] -eq 192 -and $octets[1] -eq 168) -or
            ($octets[0] -eq 192 -and $octets[1] -eq 0 -and $octets[2] -eq 2) -or
            ($octets[0] -eq 198 -and $octets[1] -ge 18 -and $octets[1] -le 19) -or
            ($octets[0] -eq 198 -and $octets[1] -eq 51 -and $octets[2] -eq 100) -or
            ($octets[0] -eq 203 -and $octets[1] -eq 0 -and $octets[2] -eq 113) -or
            $octets[0] -ge 224
        )
    }

    $bytes = $Address.GetAddressBytes()
    $isUniqueLocal = (($bytes[0] -band 0xFE) -eq 0xFC)
    $isUnspecified = @($bytes | Where-Object { $_ -ne 0 }).Count -eq 0
    return ($Address.IsIPv6LinkLocal -or $Address.IsIPv6SiteLocal -or $Address.IsIPv6Multicast -or $isUniqueLocal -or $isUnspecified)
}

function Assert-PublicControllerOrigin {
    param([string]$Value)

    if ([string]::IsNullOrWhiteSpace($Value)) {
        throw "No controller origin was supplied.`n`n$(Get-ControllerOriginHelp)"
    }

    $uri = $null
    $created = [Uri]::TryCreate($Value.Trim(), [UriKind]::Absolute, [ref]$uri)
    if (-not $created -or $uri.Scheme -ne 'https' -or -not $uri.Host) {
        throw "Controller origin must be an absolute HTTPS URL.`n`n$(Get-ControllerOriginHelp)"
    }

    $hostName = $uri.DnsSafeHost.ToLowerInvariant()
    $reservedExample = $hostName -eq 'example.com' -or $hostName.EndsWith('.example.com') -or
        $hostName -eq 'example.net' -or $hostName.EndsWith('.example.net') -or
        $hostName -eq 'example.org' -or $hostName.EndsWith('.example.org') -or
        $hostName.EndsWith('.invalid') -or $hostName.EndsWith('.test')
    if ($reservedExample) {
        throw "'$hostName' is documentation-only and cannot resolve to your controller.`n`n$(Get-ControllerOriginHelp)"
    }
    if ($hostName.EndsWith('.supabase.co')) {
        throw "A Supabase project URL is not the NLI controller origin. Supabase stores metadata behind the Rocky controller.`n`n$(Get-ControllerOriginHelp)"
    }
    if ($uri.UserInfo -or $uri.Query -or $uri.Fragment -or $uri.AbsolutePath -ne '/') {
        throw 'Controller origin must contain only scheme, host, and optional port; do not include credentials, a path, query, or fragment'
    }

    $literalAddress = $null
    if ([System.Net.IPAddress]::TryParse($hostName, [ref]$literalAddress)) {
        throw "Controller origin must use a Cloudflare DNS hostname, not an IP literal.`n`n$(Get-ControllerOriginHelp)"
    }
    $localSuffix = @('.localhost','.local','.internal','.lan','.corp','.intranet','.home.arpa') |
        Where-Object { $hostName.EndsWith($_) } |
        Select-Object -First 1
    if ($hostName -eq 'localhost' -or $null -ne $localSuffix) {
        throw "Controller origin must not be localhost or a local-only hostname.`n`n$(Get-ControllerOriginHelp)"
    }

    try {
        $addresses = @([System.Net.Dns]::GetHostAddresses($hostName))
    } catch {
        throw "Controller hostname '$hostName' does not resolve in public DNS. Provision DNS/TLS first, then rerun this script.`nUnderlying DNS error: $($_.Exception.Message)"
    }
    if ($addresses.Count -eq 0) {
        throw "Controller hostname '$hostName' returned no DNS addresses. Provision DNS/TLS first, then rerun this script."
    }
    foreach ($address in $addresses) {
        if (Test-NonPublicAddress $address) {
            throw "Controller hostname '$hostName' resolves to private/non-routable address $address; Vercel cannot reach it."
        }
    }

    return $uri.GetLeftPart([UriPartial]::Authority).TrimEnd('/')
}

if (-not (Get-Command vercel -ErrorAction SilentlyContinue)) { throw 'Vercel CLI is required' }
if (-not $ControllerOrigin) {
    $ControllerOrigin = Get-LocalDotEnvValue (Join-Path $Root '.env.local') 'NLI_CONTROLLER_ORIGIN'
}
if (-not $ControllerOrigin) {
    Write-Host (Get-ControllerOriginHelp) -ForegroundColor Yellow
    $ControllerOrigin = Read-Host 'Enter the real public HTTPS controller origin already provisioned for this deployment'
}
$ControllerOrigin = Assert-PublicControllerOrigin $ControllerOrigin

$healthUrl = "$ControllerOrigin/api/v1/health"
Write-Host "Checking controller health at $healthUrl"
try {
    $response = Invoke-WebRequest -UseBasicParsing -TimeoutSec 15 -Headers @{ Accept = 'application/json' } $healthUrl
    $health = $response.Content | ConvertFrom-Json
} catch {
    throw "The controller origin is valid but its health endpoint is unreachable or incompatible: $healthUrl`nCheck public DNS, TLS, reverse-proxy/tunnel routing, and firewall policy.`nUnderlying error: $($_.Exception.Message)"
}
$healthIdentity = [string]$response.Headers['X-NLI-Controller']
if ($response.StatusCode -ne 200 -or $healthIdentity -ne 'v20' -or $health.service -ne 'nli-controller-agent' -or $health.api_version -ne 'v1' -or -not $response.Headers['CF-RAY']) {
    throw "The public endpoint is not an NLI v20 controller: $healthUrl"
}

$loginUrl = "$ControllerOrigin/api/v1/auth/login"
try {
    $preflight = Invoke-WebRequest -UseBasicParsing -Method Options -TimeoutSec 15 -Headers @{ Accept = 'application/json' } $loginUrl
} catch {
    throw "The controller health endpoint passed, but the login preflight failed: $loginUrl`nEnsure the reverse proxy preserves OPTIONS, Allow, and X-NLI-Controller."
}
$loginIdentity = [string]$preflight.Headers['X-NLI-Controller']
$allowedMethods = @(([string]$preflight.Headers['Allow']) -split ',' | ForEach-Object { $_.Trim().ToUpperInvariant() })
if ($preflight.StatusCode -ne 204 -or $loginIdentity -ne 'v20' -or -not $allowedMethods.Contains('POST')) {
    throw 'The public endpoint does not expose the compatible NLI v20 login contract.'
}

Push-Location $Root
try {
    & vercel whoami | Out-Host
    if ($LASTEXITCODE -ne 0) { throw 'Vercel is not authenticated. Run: vercel login' }
    if (-not (Test-Path -LiteralPath (Join-Path $Root '.vercel\project.json'))) {
        Invoke-Checked 'vercel' @('link', '--yes')
    }
    $ControllerOrigin | & vercel env add NLI_CONTROLLER_ORIGIN production --force
    if ($LASTEXITCODE -ne 0) { throw 'Unable to configure NLI_CONTROLLER_ORIGIN in Vercel' }
    Invoke-Checked 'npm' @('run', 'web:check')
    Invoke-Checked 'vercel' @('deploy', '--prod', '--yes')
} finally {
    Pop-Location
}

Write-Host 'Vercel deployment completed. The browser uses same-origin cookies and a server-side controller proxy.' -ForegroundColor Green
Write-Host 'Supabase keys and the controller-only RPC secret remain on the Rocky controller.'
