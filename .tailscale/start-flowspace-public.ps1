$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..')
$backendDir = Join-Path $root 'backend'
$frontendDir = Join-Path $root 'frontend'
$logDir = $PSScriptRoot
$tailscale = 'C:\Program Files\Tailscale\tailscale.exe'

function Stop-PortOwner($port) {
  $listeners = Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue
  foreach ($listener in $listeners) {
    Stop-Process -Id $listener.OwningProcess -Force -ErrorAction SilentlyContinue
  }
}

function Start-Cmd($name, $workingDirectory, $command) {
  $out = Join-Path $logDir "$name.out.log"
  $err = Join-Path $logDir "$name.err.log"
  Remove-Item $out, $err -Force -ErrorAction SilentlyContinue
  Start-Process `
    -FilePath 'cmd.exe' `
    -ArgumentList "/d /s /c `"$command`"" `
    -WorkingDirectory $workingDirectory `
    -RedirectStandardOutput $out `
    -RedirectStandardError $err `
    -WindowStyle Hidden
}

Stop-PortOwner 8080
Stop-PortOwner 5176
Start-Sleep -Seconds 2

Start-Cmd 'flowspace-backend-8080' $backendDir 'set PORT=8080&& set GIN_MODE=release&& server-flowspace.exe'
Start-Cmd 'flowspace-preview-5176' $frontendDir 'set VITE_APP_BASE=/all-note/&& set VITE_BACKEND_PORT=8080&& npm run preview -- --host 127.0.0.1 --port 5176'

Start-Sleep -Seconds 4

if (Test-Path $tailscale) {
  & $tailscale funnel --bg --yes --set-path /all-note http://127.0.0.1:5176/all-note | Out-Host
}

Write-Host 'FlowSpace public service started: https://tylerhu-1.tail5cec87.ts.net/all-note'
