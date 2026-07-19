<#
.SYNOPSIS
    red-engine.ps1 — RED Engine unified node management (Podman).
.DESCRIPTION
    Manages one or more RED Engine nodes as Podman containers.
    Each node is identified by its host port (-p / -Port).
    Multiple independent nodes can run simultaneously for federation testing.

    Usage:  .\red-engine.ps1 <command> [-p PORT] [options]

    SETUP & BUILD
      setup               Interactive first-time wizard for the node on -p PORT
      build               Build (or rebuild) the red-engine:latest container image

    NODE LIFECYCLE
      start [-Tunnel]     Start the node; add -Tunnel to also open a cloudflared tunnel
      stop                Stop the node (container + tunnel)
      restart             Restart without rebuilding
      logs [-Follow]      Show last 100 lines; -Follow to stream
      status              Table of all active red_engine_* containers + health

    TUNNEL
      tunnel              Start a cloudflared quick tunnel for this node's port
                          (auto-updates the node's public_url setting via the API)

    MAINTENANCE
      token               Rotate the admin token for this node
      backup              Zip this node's data directory into backups/
      update              git pull -> test -> rebuild image -> restart

    DEVELOPMENT
      test                Run Go unit tests
      dev                 Vite dev server + Go live reload (no container)
      help                Show this message

    FLAGS
      -p, -Port <n>       Port of the node to operate on  [default: 8080]
      -NodeName <s>       Override node name  (setup / start)
      -Token <s>          Override admin token  (start)
      -Tunnel             Also start a cloudflared tunnel  (start)
      -Follow             Stream logs  (logs)

    MULTI-NODE EXAMPLE (3 independent nodes + 3 tunnels)
      .\red-engine.ps1 setup  -p 8080
      .\red-engine.ps1 setup  -p 8081
      .\red-engine.ps1 setup  -p 8082
      .\red-engine.ps1 build
      .\red-engine.ps1 start  -p 8080 -Tunnel
      .\red-engine.ps1 start  -p 8081 -Tunnel
      .\red-engine.ps1 start  -p 8082 -Tunnel
      .\red-engine.ps1 status
#>

param(
    [Parameter(Position=0)]
    [string]$Command = "",

    [Alias('p')]
    [ValidateRange(1, 65535)]
    [int]$Port = 8080,

    [Alias('d')]
    [string]$Dir      = "data",

    [string]$NodeName = "",
    [string]$Token    = "",
    [switch]$Tunnel,
    [switch]$Follow,
    [switch]$WipeData
)

$ErrorActionPreference = "Stop"
$OutputEncoding        = [System.Text.Encoding]::UTF8

$ROOT       = $PSScriptRoot
if (!$ROOT) { $ROOT = (Get-Location).Path }
Set-Location $ROOT

$IMAGE_NAME = "localhost/red-engine:latest"
$NODES_DIR  = Join-Path $ROOT ".red-nodes"

# ── Platform detection ────────────────────────────────────────────────────────
# $IsLinux / $IsMacOS / $IsWindows are built-ins in PowerShell 6+.
# On PS 5 (Windows-only) they are undefined; treat absence as Windows.
if ($null -eq (Get-Variable IsLinux -ErrorAction SilentlyContinue)) {
    $IsLinuxOS   = $false
    $IsMacOS_    = $false
    $IsWindowsOS = $true
} else {
    $IsLinuxOS   = $IsLinux
    $IsMacOS_    = $IsMacOS
    $IsWindowsOS = $IsWindows
}

# ── Logging ───────────────────────────────────────────────────────────────────
function Write-Info { param($msg) Write-Host "[*] $msg" -ForegroundColor Cyan    }
function Write-Ok   { param($msg) Write-Host "[+] $msg" -ForegroundColor Green   }
function Write-Warn { param($msg) Write-Host "[!] $msg" -ForegroundColor Yellow  }
function Write-Fail { param($msg) Write-Host "[x] $msg" -ForegroundColor Red     }
function Write-Head { param($msg) Write-Host "`n==> $msg" -ForegroundColor Cyan  }
function Write-Die  { param($msg) Write-Fail $msg; exit 1 }

# ── Per-port path helpers ─────────────────────────────────────────────────────
# Port 8080 (the default/production node) keeps all files at the repo root for
# backwards compatibility with the existing config.json / .env layout.
# Every other port lives entirely under .red-nodes/<port>/.

function Get-NodeDir {
    if ($Port -eq 8080) { return $ROOT }
    return Join-Path $NODES_DIR "$Port"
}

function Get-DataDir {
    if ($Port -eq 8080) { return Join-Path $ROOT $Dir }
    return Join-Path (Get-NodeDir) $Dir
}

function Get-StateDir { return Join-Path (Get-NodeDir) "state" }
function Get-LogDir   { return Get-NodeDir }

function Get-EnvFile {
    if ($Port -eq 8080) { return Join-Path $ROOT ".env" }
    return Join-Path (Get-NodeDir) ".env"
}

function Get-ContainerName { return "red_engine_$Port" }
function Get-TunnelLog     { return Join-Path (Get-NodeDir) "cloudflared.log" }
function Get-TunnelPidFile { return Join-Path (Get-NodeDir) "cloudflared.pid" }
function Get-TunnelUrlFile { return Join-Path (Get-NodeDir) "tunnel-url" }

# ── .env helpers ──────────────────────────────────────────────────────────────
function Read-EnvKey {
    param([string]$Key, [string]$File)
    if (!(Test-Path $File)) { return "" }
    $line = Select-String -Path $File -Pattern "^${Key}=" | Select-Object -First 1
    if ($line) { return ($line.Line -replace "^${Key}=", "") }
    return ""
}

function Write-EnvKey {
    param([string]$Key, [string]$Value, [string]$File)
    $dir = Split-Path $File -Parent
    if (!(Test-Path $dir))  { New-Item -ItemType Directory -Force $dir | Out-Null }
    if (!(Test-Path $File)) { New-Item $File -ItemType File | Out-Null }
    [string]$content = Get-Content $File -Raw -ErrorAction SilentlyContinue
    if (!$content) { $content = "" }
    if ($content -match "(?m)^${Key}=") {
        $content = $content -replace "(?m)^${Key}=.*", "${Key}=${Value}"
    } else {
        $content = $content.TrimEnd() + "`n${Key}=${Value}`n"
    }
    Set-Content $File $content.TrimStart() -NoNewline
}

function New-SecureToken {
    $bytes = [byte[]]::new(32)
    [System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
    $chars = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'
    -join ($bytes | ForEach-Object { $chars[$_ % $chars.Length] })
}

# ── Prerequisite checks ───────────────────────────────────────────────────────
function Assert-Tool {
    param([string]$Name, [string]$Hint = "")
    if (!(Get-Command $Name -ErrorAction SilentlyContinue)) {
        $msg = "$Name not found."
        if ($Hint) { $msg += " $Hint" }
        Write-Die $msg
    }
}

# ── Container helpers ─────────────────────────────────────────────────────────
function Get-ContainerStatus {
    param([string]$Name)
    $s = podman inspect --format '{{.State.Status}}' $Name 2>$null
    if ($LASTEXITCODE -ne 0) { return "" }
    return ($s -join "").Trim()
}

function Wait-NodeHealth {
    param([int]$NodePort, [int]$Timeout = 30)
    $url = "http://localhost:${NodePort}/-/health"
    for ($i = 0; $i -lt $Timeout; $i++) {
        try {
            $r = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 2 -ErrorAction Stop
            if ($r.StatusCode -eq 200) { return $true }
        } catch {}
        Start-Sleep 1
    }
    return $false
}

# ── Cloudflared tunnel helpers ────────────────────────────────────────────────
function Stop-Tunnel {
    param([int]$NodePort)
    $savedPort = $Port; $Port = $NodePort
    $pidFile = Get-TunnelPidFile
    $urlFile = Get-TunnelUrlFile
    $Port = $savedPort

    if (Test-Path $pidFile) {
        [string]$rawPid = Get-Content $pidFile -ErrorAction SilentlyContinue
        $rawPid = $rawPid.Trim()
        if ($rawPid -match '^\d+$') {
            Stop-Process -Id ([int]$rawPid) -Force -ErrorAction SilentlyContinue
            Write-Info "Stopped cloudflared (pid $rawPid) for port $NodePort"
        }
        Remove-Item $pidFile -ErrorAction SilentlyContinue
        Remove-Item $urlFile -ErrorAction SilentlyContinue
    }
}

function Start-Tunnel {
    # Returns the tunnel URL or throws.
    Assert-Tool cloudflared "Install from https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/install-and-setup/"

    $logFile = Get-TunnelLog
    $pidFile = Get-TunnelPidFile
    $urlFile = Get-TunnelUrlFile
    $nodeDir = Get-NodeDir

    New-Item -ItemType Directory -Force $nodeDir | Out-Null

    # Kill stale tunnel for this port
    if (Test-Path $pidFile) {
        [string]$oldPid = (Get-Content $pidFile -ErrorAction SilentlyContinue).Trim()
        if ($oldPid -match '^\d+$') {
            Stop-Process -Id ([int]$oldPid) -Force -ErrorAction SilentlyContinue
        }
        Remove-Item $pidFile -ErrorAction SilentlyContinue
        Remove-Item $urlFile -ErrorAction SilentlyContinue
    }

    # Cloudflare quick tunnels are occasionally refused (rate limit). Retry with backoff.
    $maxAttempts = 4
    for ($attempt = 1; $attempt -le $maxAttempts; $attempt++) {
        Set-Content $logFile "" -NoNewline

        $proc = Start-Process cloudflared `
            -ArgumentList "tunnel --no-autoupdate --url http://localhost:$Port" `
            -RedirectStandardOutput $logFile `
            -RedirectStandardError  $logFile `
            -NoNewWindow -PassThru

        Set-Content $pidFile $proc.Id

        # Wait up to 50 seconds for the URL to appear in the log.
        $url = ""
        for ($i = 0; $i -lt 50; $i++) {
            Start-Sleep 1
            if (Test-Path $logFile) {
                [string]$content = Get-Content $logFile -Raw -ErrorAction SilentlyContinue
                $m = [regex]::Match($content, 'https://[a-z0-9]+(-[a-z0-9]+)+\.trycloudflare\.com')
                if ($m.Success) { $url = $m.Value; break }
            }
        }

        if ($url) {
            Set-Content $urlFile $url
            return $url
        }

        # Tunnel failed — stop the process before retrying.
        Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
        Remove-Item $pidFile -ErrorAction SilentlyContinue

        [string]$logContent = Get-Content $logFile -Raw -ErrorAction SilentlyContinue
        if ($logContent -match "failed to request quick Tunnel") {
            Write-Warn "Cloudflare rate-limited quick tunnel (attempt $attempt/$maxAttempts) — retrying in $($attempt * 10)s..."
        } else {
            Write-Warn "Tunnel produced no URL (attempt $attempt/$maxAttempts) — retrying in $($attempt * 10)s..."
        }
        if ($attempt -lt $maxAttempts) { Start-Sleep ($attempt * 10) }
    }

    Write-Die "cloudflared did not produce a URL after $maxAttempts attempts. Check: $logFile"
}

# ── Commands ──────────────────────────────────────────────────────────────────

function Invoke-Build {
    Assert-Tool podman "Install from https://podman.io/"
    Write-Head "Building container image: $IMAGE_NAME"
    podman build --network=host -t $IMAGE_NAME .
    if ($LASTEXITCODE -ne 0) { Write-Die "Container build failed." }
    Write-Ok "Image ready: $IMAGE_NAME"
}

function Invoke-Setup {
    $envFile  = Get-EnvFile
    $nodeDir  = Get-NodeDir
    $dataDir  = Get-DataDir
    $stateDir = Get-StateDir

    if ((Test-Path $envFile) -and (Read-EnvKey "RED_ADMIN_TOKEN" $envFile)) {
        Write-Warn "Node on port $Port is already configured ($envFile)."
        $ow = Read-Host "  Re-run setup wizard and overwrite? [y/N]"
        if ($ow -notmatch '^[yY]$') { Invoke-Status; exit 0 }
    }

    Clear-Host
    Write-Host @"

  ██████╗ ███████╗██████╗     ███████╗███╗   ██╗ ██████╗ ██╗███╗   ██╗███████╗
  ██╔══██╗██╔════╝██╔══██╗    ██╔════╝████╗  ██║██╔════╝ ██║████╗  ██║██╔════╝
  ██████╔╝█████╗  ██║  ██║    █████╗  ██╔██╗ ██║██║  ███╗██║██╔██╗ ██║█████╗
  ██╔══██╗██╔══╝  ██║  ██║    ██╔══╝  ██║╚██╗██║██║   ██║██║██║╚██╗██║██╔══╝
  ██║  ██║███████╗██████╔╝    ███████╗██║ ╚████║╚██████╔╝██║██║ ╚████║███████╗
  ╚═╝  ╚═╝╚══════╝╚═════╝     ╚══════╝╚═╝  ╚═══╝ ╚═════╝ ╚═╝╚═╝  ╚═══╝╚══════╝

  Node Setup  (port $Port)
"@ -ForegroundColor Cyan

    # ── Node name ──────────────────────────────────────────────────────────────
    Write-Host ""
    Write-Host "  Node Identity" -ForegroundColor Cyan
    Write-Host "  ──────────────────────────────────────────────────────────" -ForegroundColor DarkGray
    Write-Host "  The node name is your permanent identity in the RED network." -ForegroundColor Yellow
    Write-Host "  Changing it after peers have connected will break those connections." -ForegroundColor Yellow
    Write-Host ""
    $defaultName = if ($NodeName) { $NodeName } else {
        try { $env:COMPUTERNAME ?? (hostname 2>$null) ?? "red-node-$Port" } catch { "red-node-$Port" }
    }
    $chosenName = Read-Host "  Node name [default: $defaultName]"
    if (!$chosenName) { $chosenName = $defaultName }
    $ack = Read-Host "  Type 'I understand' to confirm the node name is permanent"
    if ($ack -ne "I understand") { Write-Die "Acknowledgement required. Aborting." }

    # ── Admin token ────────────────────────────────────────────────────────────
    Write-Host ""
    Write-Host "  Admin Token" -ForegroundColor Cyan
    Write-Host "  ──────────────────────────────────────────────────────────" -ForegroundColor DarkGray
    $generated = New-SecureToken
    Write-Host "  Generated: " -NoNewline; Write-Host $generated -ForegroundColor Yellow
    $customToken = Read-Host "  Use this token? Press Enter to accept, or type a custom token"
    $adminToken  = if ($customToken) { $customToken } else { $generated }

    # ── Optional ───────────────────────────────────────────────────────────────
    Write-Host ""
    Write-Host "  Optional" -ForegroundColor Cyan
    Write-Host "  ──────────────────────────────────────────────────────────" -ForegroundColor DarkGray
    $description = Read-Host "  Node description [optional, shown publicly]"
    $webhook     = Read-Host "  Webhook secret   [optional, for GitHub push sync]"

    # ── Write .env ─────────────────────────────────────────────────────────────
    New-Item -ItemType Directory -Force $nodeDir  | Out-Null
    New-Item -ItemType Directory -Force $dataDir  | Out-Null
    New-Item -ItemType Directory -Force $stateDir | Out-Null

    $stamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
    Set-Content $envFile @"
# RED Engine node configuration — port $Port
# Generated: $stamp
RED_NODE_NAME=$chosenName
RED_ADMIN_TOKEN=$adminToken
RED_NODE_DESCRIPTION=$description
RED_WEBHOOK_SECRET=$webhook
"@ -NoNewline

    # Keep .env and .red-nodes/ out of git for the default node
    if ($Port -eq 8080) {
        $gi = Join-Path $ROOT ".gitignore"
        if (Test-Path $gi) {
            if (!(Select-String -Path $gi -Pattern "^\.env$"      -Quiet)) { Add-Content $gi "`n.env" }
            if (!(Select-String -Path $gi -Pattern "^\.red-nodes" -Quiet)) { Add-Content $gi ".red-nodes/" }
        }
    }

    Write-Host ""
    Write-Host "+===============================================================+" -ForegroundColor Green
    Write-Host "|  Setup complete — save these credentials now                  |" -ForegroundColor Green
    Write-Host "+===============================================================+" -ForegroundColor Green
    Write-Host "  Port        : $Port"
    Write-Host "  Node name   : " -NoNewline; Write-Host $chosenName  -ForegroundColor Yellow
    Write-Host "  Admin token : " -NoNewline; Write-Host $adminToken  -ForegroundColor Yellow
    if ($webhook) { Write-Host "  Webhook sec : $webhook" -ForegroundColor Yellow }
    Write-Host "+===============================================================+" -ForegroundColor Green
    Write-Host ""
    Write-Warn "Save your admin token — it will not be shown again."
    Write-Host ""

    $doBuild = Read-Host "  Build container image now? [Y/n]"
    if ($doBuild -notmatch '^[nN]$') { Invoke-Build }

    $started = $false
    $doStart = Read-Host "  Start the node on port $Port? [Y/n]"
    if ($doStart -notmatch '^[nN]$') { Invoke-Start; $started = $true }

    # Final summary — always printed so the token is visible even after build/start output
    Write-Host ""
    Write-Host "+===============================================================+" -ForegroundColor Green
    Write-Host "|  Setup complete                                               |" -ForegroundColor Green
    Write-Host "+===============================================================+" -ForegroundColor Green
    Write-Host "  Admin token : " -NoNewline; Write-Host $adminToken -ForegroundColor Yellow
    Write-Host "  Stored in   : $(Get-EnvFile)" -ForegroundColor DarkGray
    Write-Host "+---------------------------------------------------------------+" -ForegroundColor Green
    if ($started) {
        Write-Host "  Node running  : " -NoNewline; Write-Host "http://localhost:$Port" -ForegroundColor Cyan
        Write-Host "  View status   : .\red-engine.ps1 status" -ForegroundColor DarkGray
        Write-Host "  Add tunnel    : .\red-engine.ps1 tunnel -p $Port" -ForegroundColor DarkGray
    } else {
        Write-Host "  To start your node run:" -ForegroundColor DarkGray
        Write-Host "  .\red-engine.ps1 start -p $Port -d $Dir" -ForegroundColor Cyan
        Write-Host "  .\red-engine.ps1 start -p $Port -d $Dir -Tunnel   (+ cloudflared)" -ForegroundColor Cyan
    }
    Write-Host "+===============================================================+" -ForegroundColor Green
    Write-Host ""
}

function Invoke-Start {
    Assert-Tool podman "Install from https://podman.io/"

    $envFile  = Get-EnvFile
    $dataDir  = Get-DataDir
    $stateDir = Get-StateDir
    $cntr     = Get-ContainerName

    if (!(Test-Path $envFile)) {
        Write-Die "No .env found for port $Port. Run: .\red-engine.ps1 setup -p $Port"
    }

    podman image exists $IMAGE_NAME 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Die "Image $IMAGE_NAME not found. Run first: .\red-engine.ps1 build"
    }

    New-Item -ItemType Directory -Force $dataDir  | Out-Null
    New-Item -ItemType Directory -Force $stateDir | Out-Null

    # Remove stale container (any state)
    $existing = Get-ContainerStatus $cntr
    if ($existing) {
        Write-Info "Removing existing container $cntr (was: $existing)..."
        podman rm -f $cntr 2>$null | Out-Null
    }

    # Read credentials
    $adminToken  = if ($Token)    { $Token }    else { Read-EnvKey "RED_ADMIN_TOKEN"      $envFile }
    $nodeName    = if ($NodeName) { $NodeName } else { Read-EnvKey "RED_NODE_NAME"        $envFile }
    $description = Read-EnvKey "RED_NODE_DESCRIPTION" $envFile
    $webhookSec  = Read-EnvKey "RED_WEBHOOK_SECRET"   $envFile

    if (!$adminToken) { Write-Die "RED_ADMIN_TOKEN not set in $envFile" }

    Write-Info "Starting $cntr..."

    # Build volume mount flags. On Linux, append ',z' to relabel for SELinux.
    $volFlags  = if ($IsLinuxOS) { ":rw,z" } else { ":rw" }
    $dataMount = "${dataDir}:/app/data${volFlags}"
    $stateMnt  = "${stateDir}:/app/state${volFlags}"

    # Network strategy:
    #   Linux   — host networking (container shares host netns; app binds :PORT directly)
    #   Windows / macOS — port mapping (host PORT -> container 8080)
    $runArgs = @(
        "run", "-d",
        "--pull=never",
        "--name", $cntr,
        "--restart", "unless-stopped",
        "--userns=keep-id",
        "-v", $dataMount,
        "-v", $stateMnt,
        "-e", "RED_DATA_DIR=/app/data",
        "-e", "RED_STATE_DIR=/app/state",
        "-e", "RED_ADMIN_TOKEN=$adminToken",
        "-e", "RED_NODE_NAME=$nodeName",
        "-e", "RED_NODE_DESCRIPTION=$description",
        "-e", "RED_WEBHOOK_SECRET=$webhookSec"
    )

    if ($IsLinuxOS) {
        $runArgs += @("--network", "host", "-e", "RED_ADDR=:$Port")
    } else {
        $runArgs += @("-p", "${Port}:8080", "-e", "RED_ADDR=:8080")
    }

    $runArgs += $IMAGE_NAME

    podman @runArgs
    if ($LASTEXITCODE -ne 0) { Write-Die "Failed to start $cntr" }

    Write-Info "Waiting for health check on port $Port..."
    if (!(Wait-NodeHealth $Port 30)) {
        Write-Fail "Node did not become healthy within 30s. Check logs:"
        Write-Host "  .\red-engine.ps1 logs -p $Port [-Follow]"
        exit 1
    }
    Write-Ok "$cntr is UP — http://localhost:$Port"

    if ($Tunnel) { Invoke-Tunnel }
}

function Invoke-Stop {
    $cntr = Get-ContainerName
    $state = Get-ContainerStatus $cntr
    if ($state) {
        Write-Info "Stopping container $cntr..."
        podman rm -f $cntr 2>$null | Out-Null
        Write-Ok "$cntr stopped."
    } else {
        Write-Warn "No container found: $cntr"
    }

    Stop-Tunnel $Port

    if ($WipeData) {
        $dataDir = Get-DataDir
        if (Test-Path $dataDir) {
            Remove-Item -Recurse -Force $dataDir
            Write-Info "Wiped data directory: $dataDir"
        }
    }
}

function Invoke-Restart {
    $envFile = Get-EnvFile
    if (!(Test-Path $envFile)) {
        Write-Die "No .env found for port $Port. Run setup first."
    }
    Invoke-Stop
    Start-Sleep 2
    Invoke-Start
}

function Invoke-Logs {
    $cntr = Get-ContainerName
    if (!(Get-ContainerStatus $cntr)) { Write-Die "Container $cntr is not running." }
    if ($Follow) { podman logs -f $cntr }
    else         { podman logs --tail 100 $cntr }
}

function Invoke-Tunnel {
    Write-Info "Requesting cloudflared quick tunnel for port $Port..."
    $url = Start-Tunnel
    Write-Ok "Tunnel URL: $url"

    # Push public_url into the running node's settings via the admin API.
    $envFile    = Get-EnvFile
    $adminToken = Read-EnvKey "RED_ADMIN_TOKEN" $envFile
    if ($adminToken) {
        try {
            $body = (@{ public_url = $url; tunnel_type = "cloudflare_quick" } | ConvertTo-Json)
            Invoke-RestMethod -Uri "http://localhost:$Port/-/admin/config" `
                -Method POST -Body $body -ContentType "application/json" `
                -Headers @{ "X-Admin-Token" = $adminToken } -ErrorAction Stop | Out-Null
            Write-Info "public_url updated in node settings."
        } catch {
            Write-Warn "Could not push public_url to running node ($($_.Exception.Message))."
            Write-Warn "The URL is saved; restart the node to pick it up."
        }
    }
}

function Invoke-Status {
    Assert-Tool podman "Install from https://podman.io/"
    Write-Head "RED Engine nodes"
    Write-Host ""

    $containers = podman ps -a --format "{{.Names}}" 2>$null |
                  Where-Object { $_ -match "^red_engine_\d+$" }

    if (!$containers) {
        Write-Warn "No red_engine containers found."
        Write-Host "  Run: .\red-engine.ps1 setup [-p PORT] [-d DIR]" -ForegroundColor DarkGray
        return
    }

    $sep = "  " + ("─" * 60)

    foreach ($c in $containers) {
        $p     = [int]($c -replace "red_engine_", "")
        $state = (podman inspect --format '{{.State.Status}}' $c 2>$null | Out-String).Trim()

        $health = "unreachable"
        try {
            $r = Invoke-WebRequest -Uri "http://localhost:${p}/-/health" `
                     -UseBasicParsing -TimeoutSec 2 -ErrorAction Stop
            $health = if ($r.StatusCode -eq 200) { "healthy" } else { "http $($r.StatusCode)" }
        } catch {}

        $urlDir    = if ($p -eq 8080) { $ROOT } else { Join-Path $NODES_DIR "$p" }
        $urlFile   = Join-Path $urlDir "tunnel-url"
        $tunnelUrl = if (Test-Path $urlFile) {
            (Get-Content $urlFile -ErrorAction SilentlyContinue | Out-String).Trim()
        } else { "" }

        $stateColor  = switch ($state) { "running" {"Green"} "exited" {"Red"} default {"Yellow"} }
        $healthColor = if ($health -eq "healthy") { "Green" } else { "Red" }

        Write-Host $sep -ForegroundColor DarkGray
        Write-Host ("  ") -NoNewline
        Write-Host ("{0,-22}" -f $c) -NoNewline -ForegroundColor Cyan
        Write-Host ("  port ") -NoNewline
        Write-Host ("{0,-6}" -f $p) -NoNewline -ForegroundColor Yellow
        Write-Host ("  ") -NoNewline
        Write-Host ("{0,-10}" -f $state) -NoNewline -ForegroundColor $stateColor
        Write-Host ("  ") -NoNewline
        Write-Host $health -ForegroundColor $healthColor
        Write-Host ("  Local  : ") -NoNewline
        Write-Host "http://localhost:$p" -ForegroundColor Cyan
        if ($tunnelUrl) {
            Write-Host ("  Tunnel : ") -NoNewline
            Write-Host $tunnelUrl -ForegroundColor Magenta
        } else {
            Write-Host ("  Tunnel : none") -ForegroundColor DarkGray
        }
    }
    Write-Host $sep -ForegroundColor DarkGray
    Write-Host ""
}

function Invoke-Token {
    $envFile = Get-EnvFile
    if (!(Test-Path $envFile)) { Write-Die "No .env found for port $Port." }

    Write-Head "Rotate admin token (port $Port)"
    $current = Read-EnvKey "RED_ADMIN_TOKEN" $envFile
    if ($current) { Write-Host "  Current: " -NoNewline; Write-Host $current -ForegroundColor Yellow }

    $choice = Read-Host "`n  Generate and apply a new secure token? [y/N]"
    if ($choice -notmatch '^[yY]$') { Write-Info "Token unchanged."; return }

    $newToken = New-SecureToken
    Write-EnvKey "RED_ADMIN_TOKEN" $newToken $envFile
    Write-Host ""
    Write-Ok "New admin token: $newToken"
    Write-Warn "Restart the node for the change to take effect:"
    Write-Host "  .\red-engine.ps1 restart -p $Port"
}

function Invoke-Backup {
    $dataDir = Get-DataDir
    if (!(Test-Path $dataDir)) { Write-Die "Data directory not found: $dataDir" }

    $stamp   = Get-Date -Format "yyyyMMdd_HHmmss"
    $backDir = Join-Path $ROOT "backups"
    $dest    = Join-Path $backDir "data-${Port}_${stamp}.zip"
    New-Item -ItemType Directory -Force $backDir | Out-Null
    Compress-Archive -Path "$dataDir\*" -DestinationPath $dest -Force
    Write-Ok "Backup written: $dest"
}

function Invoke-Update {
    Write-Head "Updating RED Engine (port $Port)"

    Write-Info "Pulling latest source..."
    git pull
    if ($LASTEXITCODE -ne 0) { Write-Warn "git pull failed — continuing with current code." }

    Assert-Tool go "Install from https://golang.org/"
    Write-Info "Running tests before rebuild..."
    go test ./...
    if ($LASTEXITCODE -ne 0) { Write-Die "Tests failed. Aborting update to protect the running node." }

    Invoke-Build

    $state = Get-ContainerStatus (Get-ContainerName)
    if ($state -eq "running") {
        Write-Info "Restarting node..."
        Invoke-Restart
    } else {
        Write-Info "Node was not running. Start with:"
        Write-Host "  .\red-engine.ps1 start -p $Port"
    }
    Write-Ok "Update complete."
}

function Invoke-Test {
    Assert-Tool go "Install from https://golang.org/"
    Write-Head "Running test suite"
    go test ./...
    if ($LASTEXITCODE -eq 0) { Write-Ok "All tests passed." }
    else                     { Write-Die "Tests failed." }
}

function Invoke-Dev {
    Assert-Tool go  "Install from https://golang.org/"
    Assert-Tool npm "Install from https://nodejs.org/"
    Write-Head "Starting development environment"

    if (!(Get-Command air -ErrorAction SilentlyContinue)) {
        Write-Warn "air not found — installing..."
        go install github.com/air-verse/air@latest
        $goBin = Join-Path (go env GOPATH) "bin"
        if ($env:PATH -notlike "*$goBin*") {
            $env:PATH += [IO.Path]::PathSeparator + $goBin
        }
    }

    if (!(Test-Path (Join-Path $ROOT "node_modules"))) {
        Write-Info "Installing npm dependencies..."
        npm install --legacy-peer-deps
    }

    $env:NODE_OPTIONS = "--disable-warning=DEP0205"
    Write-Info "Starting Vite dev server on :5173..."
    $vite = Start-Process npx -ArgumentList "vite" -NoNewWindow -PassThru

    Write-Warn "Open http://localhost:5173 in your browser."
    $env:DEV_MODE = "true"
    try {
        if (Test-Path (Join-Path $ROOT ".air.dev.toml")) {
            air -c .air.dev.toml -- "-config=config.json"
        } else {
            go run ./cmd/red/main.go "-config=config.json"
        }
    } finally {
        if ($vite -and !$vite.HasExited) {
            Stop-Process -Id $vite.Id -Force -ErrorAction SilentlyContinue
        }
        Write-Ok "Development environment stopped."
    }
}

function Invoke-Help {
    Write-Host @"

  RED Engine — Podman node management
  =====================================

  .\red-engine.ps1 <command> [-p PORT] [options]

  SETUP & BUILD
    setup               First-time wizard for the node on -p PORT (default: 8080)
    build               Build (or rebuild) the red-engine:latest container image

  NODE LIFECYCLE
    start [-Tunnel]     Start the node; -Tunnel also opens a cloudflared tunnel
    stop  [-WipeData]   Stop the node (container + tunnel); -WipeData removes data/
    restart             Restart without rebuilding the image
    logs  [-Follow]     Show last 100 lines; -Follow to stream live
    status              Table of all active red_engine_* containers + health

  TUNNEL
    tunnel              Start a cloudflared quick tunnel for this node's port
                        (auto-pushes public_url to the node's settings API)

  MAINTENANCE
    token               Rotate the admin token for this node
    backup              Zip this node's data/ directory into backups/
    update              git pull -> go test -> rebuild image -> restart

  DEVELOPMENT
    test                Run Go unit tests (go test ./...)
    dev                 Vite dev server + Go live reload (no container)
    help                Show this message

  FLAGS
    -p, -Port <n>       Port of the node to operate on  [default: 8080]
    -NodeName <s>       Override node name  (setup / start)
    -Token <s>          Override admin token  (start)
    -Tunnel             Also start a cloudflared tunnel  (start)
    -Follow             Stream logs  (logs)
    -WipeData           Also delete the data directory  (stop)

  MULTI-NODE EXAMPLE — 3 independent nodes for federation testing
    .\red-engine.ps1 setup   -p 8080          # wizard for node 1
    .\red-engine.ps1 setup   -p 8081          # wizard for node 2
    .\red-engine.ps1 setup   -p 8082          # wizard for node 3
    .\red-engine.ps1 build                    # build image once
    .\red-engine.ps1 start   -p 8080 -Tunnel  # start node 1 + tunnel
    .\red-engine.ps1 start   -p 8081 -Tunnel  # start node 2 + tunnel
    .\red-engine.ps1 start   -p 8082 -Tunnel  # start node 3 + tunnel
    .\red-engine.ps1 status                   # live view of all 3

  Node state layout:
    Port 8080  → ./data/  ./state/  ./.env   (repo root, backwards-compatible)
    Port 8081  → ./.red-nodes/8081/data/  state/  .env
    Port 8082  → ./.red-nodes/8082/data/  state/  .env

"@
}

# ── Dispatch ──────────────────────────────────────────────────────────────────

switch ($Command.ToLower()) {
    "setup"   { Invoke-Setup   }
    "build"   { Invoke-Build   }
    "start"   { Invoke-Start   }
    "stop"    { Invoke-Stop    }
    "restart" { Invoke-Restart }
    "logs"    { Invoke-Logs    }
    "tunnel"  { Invoke-Tunnel  }
    "status"  { Invoke-Status  }
    "token"   { Invoke-Token   }
    "backup"  { Invoke-Backup  }
    "update"  { Invoke-Update  }
    "test"    { Invoke-Test    }
    "dev"     { Invoke-Dev     }
    "help"    { Invoke-Help    }
    "" {
        $envFile = Get-EnvFile
        if ((Test-Path $envFile) -and (Read-EnvKey "RED_ADMIN_TOKEN" $envFile)) { Invoke-Status }
        else { Invoke-Help }
    }
    default { Write-Die "Unknown command: '$Command'. Run: .\red-engine.ps1 help" }
}
