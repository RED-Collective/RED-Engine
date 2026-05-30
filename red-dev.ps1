red-dev.ps1
# red-dev.ps1 – RED Engine development environment (Windows)
Write-Host "🚀 Starting RED Engine development environment..." -ForegroundColor Green

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

Write-Host "✅ Both processes running. Press Ctrl+C to stop." -ForegroundColor Green

# Wait for Ctrl+C
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