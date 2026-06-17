$ErrorActionPreference = 'SilentlyContinue'

foreach ($port in 4200, 4100, 5176) {
  $listeners = Get-NetTCPConnection -LocalPort $port -State Listen
  foreach ($listener in $listeners) {
    Stop-Process -Id $listener.OwningProcess -Force
  }
}

Write-Host 'FlowSpace public frontend processes stopped. Local backends and Tailscale Funnel config were left unchanged.'
