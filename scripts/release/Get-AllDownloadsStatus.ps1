#Requires -Version 5.1
[CmdletBinding()]
param([string] $Root = 'D:\Code\Other\AllDownloads-v2')
. (Join-Path $PSScriptRoot 'Common.ps1')
$Root = Assert-DeploymentRoot $Root
$state = Get-ProcessState $Root
if ($null -eq $state) { Write-Output 'NOT_STARTED'; return }
$processes = @($state.processes | ForEach-Object { [pscustomobject]@{ name = $_.name; pid = $_.pid; matchesRecordedProcess = ($null -ne (Get-RecordedProcess $_)) } })
$healthy = $false
$failure = $null
$mountRequired = $state.PSObject.Properties.Name -contains 'mountRequired' -and $state.mountRequired
$mount = [pscustomobject]@{ state = 'UNKNOWN'; reasonCode = 'MOUNT_OBSERVATION_UNAVAILABLE' }
try {
    $health = Read-LocalHealth $state.port
    $healthy = Test-HealthPhase $health -Runner
    $mount = Get-MountObservation $health
} catch { $failure = 'HTTP health unavailable' }
$baseHealthy = $healthy -and $processes.Count -eq 2 -and @($processes | Where-Object { -not $_.matchesRecordedProcess }).Count -eq 0
$healthy = $baseHealthy -and (-not $mountRequired -or $mount.state -eq 'MOUNTED')
$status = 'UNHEALTHY'
if ($baseHealthy) { $status = $(if ($healthy) { 'RUNNING' } else { 'RUNNING_WITH_MOUNT_PENDING' }) }
[pscustomobject]@{ status = $status; recordedStatus = $state.status; version = $state.version; url = "http://127.0.0.1:$($state.port)"; healthy = $healthy; healthError = $failure; mountRequired = [bool]$mountRequired; mount = $mount; processes = $processes; logs = (Join-Path $Root 'runtime/logs') } | ConvertTo-Json -Depth 5
if (-not $healthy) { exit 1 }
exit 0
