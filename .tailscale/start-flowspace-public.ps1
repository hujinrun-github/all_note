$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..')
$frontendDir = Join-Path $root 'frontend'
$logDir = $PSScriptRoot
$tailscale = 'C:\Program Files\Tailscale\tailscale.exe'
$publicHost = 'tylerhu-1.king-shiner.ts.net'

$prodFrontendPort = 5198
$prodBackendHost = '[::1]'
$prodBackendPort = 8080
$prodBase = '/all-note/'
$prodServePath = '/all-note'

$testFrontendPort = 15198
$testBackendHost = '127.0.0.1'
$testBackendPort = 18080
$testBase = '/all-note-test/'
$testServePath = '/all-note-test'

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

Stop-PortOwner $prodFrontendPort
Stop-PortOwner $testFrontendPort
Stop-PortOwner 5176
Start-Sleep -Seconds 2

Start-Cmd `
  'flowspace-public-prod-5198' `
  $frontendDir `
  "set VITE_APP_BASE=$prodBase&& set VITE_BACKEND_HOST=$prodBackendHost&& set VITE_BACKEND_PORT=$prodBackendPort&& npm run dev -- --host 127.0.0.1 --port $prodFrontendPort"

Start-Cmd `
  'flowspace-public-test-15198' `
  $frontendDir `
  "set VITE_APP_BASE=$testBase&& set VITE_BACKEND_HOST=$testBackendHost&& set VITE_BACKEND_PORT=$testBackendPort&& npm run dev -- --host 127.0.0.1 --port $testFrontendPort"

Start-Sleep -Seconds 4

if (Test-Path $tailscale) {
  & $tailscale funnel --bg --yes --set-path $prodServePath "http://127.0.0.1:$prodFrontendPort$prodServePath" | Out-Host
  & $tailscale funnel --bg --yes --set-path $testServePath "http://127.0.0.1:$testFrontendPort$testServePath" | Out-Host
} else {
  Write-Warning "Tailscale CLI not found: $tailscale"
}

Write-Host 'FlowSpace public services configured:'
Write-Host "  prod: https://$publicHost/all-note/"
Write-Host "  test: https://$publicHost/all-note-test/"
Write-Host 'Local backends must be running on 8080 (prod) and 18080 (test).'
