# red-dev.ps1 – RED Engine development environment (Windows)
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
    go install github.com/air-verse/air@latest
    # Refresh PATH (optional, but air might be in user's GOPATH/bin)
    $env:Path = [System.Environment]::GetEnvironmentVariable("Path","Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path","User")
}

# --- Install dependencies ---
Write-Host "📦 Installing Go dependencies..." -ForegroundColor Green
go mod download

if (-not (Test-Path "node_modules")) {
    Write-Host "📦 Installing npm dependencies..." -ForegroundColor Green
    npm install
}

# --- Start background jobs ---
Write-Host "🎨 Starting Tailwind CSS watcher..." -ForegroundColor Green
$tailwindJob = Start-Job -ScriptBlock {
    Set-Location $using:PWD
    npm run watch:tailwind
}

Write-Host "🏃 Starting Go server with live reload (DEV_MODE=true)..." -ForegroundColor Green
$env:DEV_MODE = "true"
$airJob = Start-Job -ScriptBlock {
    Set-Location $using:PWD
    air
}

# --- Wait for server to become ready ---
Write-Host "Waiting for server to start..." -NoNewline
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
        # still starting
    }
    Write-Host "." -NoNewline
}
if ($ready) { Write-Host " ✓" -ForegroundColor Green } else { Write-Host " timed out" -ForegroundColor Red }

# --- Display URLs ---
$ip = Get-LocalIP
Write-Host "`n✅ RED Engine is running!" -ForegroundColor Green
Write-Host "   📡 Local:    http://localhost:8080" -ForegroundColor Yellow
if ($ip) {
    Write-Host "   🌐 Network:  http://$($ip):8080" -ForegroundColor Yellow
} else {
    Write-Host "   ⚠️  Could not detect local IP. Use 'ipconfig' to find your LAN address."
}
Write-Host "`n   Press Ctrl+C to stop." -ForegroundColor Green

# --- Wait for Ctrl+C ---
try {
    Wait-Event -Timeout ([System.Threading.Timeout]::Infinite)
}
finally {
    Write-Host "`n🛑 Shutting down processes..." -ForegroundColor Yellow
    Stop-Job $tailwindJob
    Stop-Job $airJob
    Remove-Job $tailwindJob
    Remove-Job $airJob
    Write-Host "✅ Development environment stopped." -ForegroundColor Green
}