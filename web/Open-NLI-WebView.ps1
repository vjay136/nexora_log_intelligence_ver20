[CmdletBinding()]
param(
    [string]$RockyHost = '',
    [string]$SshUser = '',
    [ValidateRange(1024, 65535)][int]$LocalPort = 12382,
    [switch]$NoBrowser
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Test-NLIController {
    param([string]$BaseUrl)
    try {
        $health = Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/api/v1/health" -Headers @{ Accept = 'application/json' } -TimeoutSec 3
        $healthIdentity = [string]$health.Headers['X-NLI-Controller']
        $body = $health.Content | ConvertFrom-Json
        if ($health.StatusCode -ne 200 -or $healthIdentity -ne 'v20' -or $body.service -ne 'nli-controller-agent' -or $body.api_version -ne 'v1') {
            return $false
        }

        $preflight = Invoke-WebRequest -UseBasicParsing -Method Options -Uri "$BaseUrl/api/v1/auth/login" -Headers @{ Accept = 'application/json' } -TimeoutSec 3
        $loginIdentity = [string]$preflight.Headers['X-NLI-Controller']
        $allowedMethods = @(([string]$preflight.Headers['Allow']) -split ',' | ForEach-Object { $_.Trim().ToUpperInvariant() })
        return $preflight.StatusCode -eq 204 -and $loginIdentity -eq 'v20' -and $allowedMethods.Contains('POST')
    } catch {
        return $false
    }
}

$baseUrl = "http://127.0.0.1:$LocalPort"
if (-not (Test-NLIController $baseUrl)) {
    if (-not (Get-Command ssh.exe -ErrorAction SilentlyContinue)) {
        throw 'Windows OpenSSH is required. Install the Windows OpenSSH Client capability, then rerun this launcher.'
    }
    if (-not $RockyHost) { $RockyHost = Read-Host 'Rocky controller hostname or IPv4 address' }
    if (-not $SshUser) { $SshUser = Read-Host 'Rocky SSH username' }
    $RockyHost = $RockyHost.Trim()
    $SshUser = $SshUser.Trim()
    if ($RockyHost -notmatch '^[A-Za-z0-9][A-Za-z0-9.:-]{0,252}$') {
        throw 'RockyHost must be a DNS name, IPv4 address, or safe SSH alias without spaces or shell characters.'
    }
    if ($SshUser -notmatch '^[A-Za-z0-9_][A-Za-z0-9._-]{0,63}$') {
        throw 'SshUser contains unsupported characters.'
    }
    if ($RockyHost -match '(^|\.)example\.(com|net|org)$' -or $RockyHost -eq 'nli-controller.example.com') {
        throw 'Enter the real Rocky controller hostname or IP, not a documentation placeholder.'
    }

    Write-Host "Opening a protected SSH tunnel to $SshUser@$RockyHost ..." -ForegroundColor Cyan
    & ssh.exe -f -N -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -L "127.0.0.1:${LocalPort}:127.0.0.1:12382" "$SshUser@$RockyHost"
    if ($LASTEXITCODE -ne 0) { throw "SSH tunnel failed with exit code $LASTEXITCODE" }

    $ready = $false
    foreach ($attempt in 1..30) {
        if (Test-NLIController $baseUrl) { $ready = $true; break }
        Start-Sleep -Seconds 1
    }
    if (-not $ready) {
        throw "The SSH tunnel opened, but $baseUrl does not expose the NLI Ver20 health and login contracts. Check nli-controller-agent on Rocky."
    }
}

Write-Host "Verified Nexora Log Intelligence Ver20 controller and login API: $baseUrl" -ForegroundColor Green
Write-Host 'Sign in as admin. If the stored password is unknown, run sudo nli-admin-reset on the controller.'
if (-not $NoBrowser) { Start-Process "$baseUrl/" }
