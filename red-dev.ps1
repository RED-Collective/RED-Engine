# red-dev.ps1 – RED Engine development environment (Windows)
$ErrorActionPreference = "Stop"
Write-Host "🚀 Starting RED Engine development environment..." -ForegroundColor Green

# --- Helper: Get local IP ---
function Get-LocalIP {
    (Get-NetIPAddress -AddressFamily IPv4 | Where-Object {
        $_.InterfaceAlias -notlike "*Loopback*" -and $_.IPAddress -notmatch "^169\." -and $_.PrefixOrigin -ne "WellKnown"
    }).IPAddress | Select-Object -First 1
}

# --- Dependency checks ---
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host "❌ Go not found. Please install Go." -ForegroundColor Red
    exit 1
}
if (-not (Get-Command npm -ErrorAction SilentlyContinue)) {
    Write-Host "❌ npm not found. Please install Node.js." -ForegroundColor Red
    exit 1
}

if (-not (Get-Command air -ErrorAction SilentlyContinue)) {
    Write-Host "⚠️  air not found. Installing..." -ForegroundColor Yellow
    try {
        go install github.com/air-verse/air@latest
        # Safely append the default Go bin location to the local session PATH
        $env:PATH += ";$env:USERPROFILE\go\bin"
    } catch {
        Write-Host "❌ Failed to install air automatically." -ForegroundColor Red
        exit 1
    }
}

# --- Install dependencies ---
Write-Host "📦 Installing Go dependencies..." -ForegroundColor Green
go mod download

if (-not (Test-Path "node_modules")) {
    Write-Host "📦 Installing npm dependencies..." -ForegroundColor Green
    npm install
}

# --- Environment Setup ---
$env:DEV_MODE = "true"

# --- Start background processes ---
Write-Host "🎨 Starting Tailwind CSS watcher..." -ForegroundColor Green
$tailwindProc = Start-Process npx.cmd -ArgumentList "tailwindcss -i ./internal/router/static/tailwind-input.css -o ./internal/router/static/tailwind.css --watch" -NoNewWindow -PassThru

Write-Host "🏃 Starting Go server with live reload (DEV_MODE=true)..." -ForegroundColor Green
# Pointing to our cross-platform isolated Windows configuration file
$airProc = Start-Process air -ArgumentList "-c .air.windows.toml" -NoNewWindow -PassThru

# --- Wait for server to become ready ---
Write-Host "⏳ Waiting for server to start..." -NoNewline
$ready = $false
for ($i = 0; $i -lt 20; $i++) {
    Start-Sleep -Milliseconds 500
    try {
        $response = Invoke-WebRequest -Uri "http://localhost:8080/" -Method Head -TimeoutSec 1 -ErrorAction Stop
        if ($response.StatusCode -eq 200) {
            $ready = $true
            break
        }
    } catch {
        # Server is still booting
    }
    Write-Host "." -NoNewline
}

if ($ready) { 
    Write-Host " ✓" -ForegroundColor Green 
} else { 
    Write-Host " timed out (Check air output above for compilation issues)" -ForegroundColor Red 
}

# --- Display URLs ---
$ip = Get-LocalIP
Write-Host "`n✅ RED Engine is running!" -ForegroundColor Green
Write-Host "   📡 Local:    http://localhost:8080" -ForegroundColor Yellow
if ($ip) {
    Write-Host "   🌐 Network:  http://$($ip):8080" -ForegroundColor Yellow
} else {
    Write-Host "   ⚠️  Could not detect local IP."
}
Write-Host "`n   Press Ctrl+C to stop." -ForegroundColor Green

# --- Reliable Event Loop Intercept ---
try {
    while ($true) {
        Start-Sleep -Seconds 1
        # Stop everything if one of our background worker engines crashes early
        if ($tailwindProc.HasExited -or $airProc.HasExited) {
            Write-Host "`n⚠️ One of the background processes stopped unexpectedly." -ForegroundColor Yellow
            break
        }
    }
}
finally {
    Write-Host "`n🛑 Shutting down processes..." -ForegroundColor Yellow
    foreach ($proc in @($tailwindProc, $airProc)) {
        if ($proc -and -not $proc.HasExited) {
            Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
        }
    }
    Write-Host "✅ Development environment stopped cleanly." -ForegroundColor Green
}
