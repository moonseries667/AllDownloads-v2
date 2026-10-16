#Requires -Version 5.1
[CmdletBinding()]
param([string] $Root = 'D:\Code\Other\AllDownloads-v2')
. (Join-Path $PSScriptRoot 'Common.ps1')
$Root = Assert-DeploymentRoot $Root
$lock = Enter-DeploymentLock $Root
try {
    $state = Get-ProcessState $Root
    if ($null -eq $state) { Write-Output 'No process record exists for this deployment.'; return }
    Stop-RecordedProcesses $state
    $state.status = 'STOPPED'
    Write-JsonFile (Join-Path $Root 'runtime/processes/active.json') $state
    Write-Output 'Recorded deployment processes stopped; runtime data and logs preserved.'
} finally { $lock.Dispose() }
