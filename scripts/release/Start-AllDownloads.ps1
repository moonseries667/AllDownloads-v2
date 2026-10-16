#Requires -Version 5.1
[CmdletBinding()]
param(
    [string] $Root = 'D:\Code\Other\AllDownloads-v2',
    [ValidateRange(5, 180)] [int] $TimeoutSeconds = 60
)
. (Join-Path $PSScriptRoot 'Common.ps1')
$Root = Assert-DeploymentRoot $Root
Assert-InteractiveSession
$lock = Enter-DeploymentLock $Root
try { Start-Deployment $Root $TimeoutSeconds | Format-List } finally { $lock.Dispose() }
