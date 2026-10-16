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
try { $healthy = Test-HealthPhase (Read-LocalHealth $state.port) -Runner } catch { $failure = 'HTTP health unavailable' }
[pscustomobject]@{ recordedStatus = $state.status; version = $state.version; url = "http://127.0.0.1:$($state.port)"; healthy = $healthy; healthError = $failure; processes = $processes; logs = (Join-Path $Root 'runtime/logs') } | ConvertTo-Json -Depth 5
if (-not $healthy -or @($processes | Where-Object { -not $_.matchesRecordedProcess }).Count -gt 0 -or $processes.Count -ne 2) { exit 1 }
