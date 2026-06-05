$ErrorActionPreference = 'SilentlyContinue'

foreach ($port in 8080, 5176) {
  $listeners = Get-NetTCPConnection -LocalPort $port -State Listen
  foreach ($listener in $listeners) {
    Stop-Process -Id $listener.OwningProcess -Force
  }
}

Write-Host 'FlowSpace local backend/frontend processes stopped. Tailscale Funnel config was left unchanged.'
