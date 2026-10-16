#Requires -Version 5.1
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string] $BundleRoot,
    [string] $Root = 'D:\Code\Other\AllDownloads-v2',
    [ValidateRange(1024, 65535)] [int] $Port = 8080,
    [string] $JavaExecutable = 'java.exe'
)
. (Join-Path $PSScriptRoot 'Common.ps1')
$Root = Assert-DeploymentRoot $Root
$BundleRoot = Get-FullPath $BundleRoot
if ($Root.Equals($BundleRoot, [StringComparison]::OrdinalIgnoreCase) -or $Root.StartsWith($BundleRoot + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Deployment root must be outside the unpacked bundle.' }
Assert-PlainTree $BundleRoot
$source = Join-Path $BundleRoot 'release'
$manifest = Read-JsonFile (Join-Path $source 'release.json')
Assert-Version $manifest.version
if ($manifest.schemaVersion -ne 1 -or $manifest.platform -ne 'windows-x64' -or $manifest.java -ne '21' -or
    $manifest.ffmpeg -ne '9.0.2' -or $manifest.ffprobe -ne '9.0.2' -or $manifest.tdl -ne '0.20.4' -or $manifest.rclone -ne '1.74.4' -or
    $manifest.frontend.apiMode -ne 'http' -or $manifest.frontend.apiBase -ne '/api/v1' -or -not $manifest.sessionSetup) { throw 'Unsupported release or tool versions.' }
$expectedTop = @('release', 'scripts', 'tools', 'Y7000P.md')
foreach ($item in Get-ChildItem -LiteralPath $BundleRoot -Force) {
    if ($item.Name -notin $expectedTop) { throw "Unexpected bundle input: $($item.Name)" }
}
foreach ($item in Get-ChildItem -LiteralPath $source -Force) {
    if ($item.Name -notin @('backend.jar', 'runner.exe', 'session-setup.exe', 'frontend', 'release.json', 'config-templates')) { throw "Unexpected release input: $($item.Name)" }
}
foreach ($file in @('backend.jar', 'runner.exe', 'session-setup.exe', 'frontend/index.html', 'frontend/release-build.json')) {
    if (-not (Test-Path -LiteralPath (Join-Path $source $file) -PathType Leaf)) { throw "Incomplete package: $file" }
}
$mode = Read-JsonFile (Join-Path $source 'frontend/release-build.json')
if ($mode.apiMode -ne 'http' -or $mode.apiBase -ne '/api/v1') { throw 'Frontend production build marker is invalid.' }
foreach ($tool in @('ffmpeg', 'ffprobe')) { [void](Get-ToolVersion (Join-Path $BundleRoot "tools/ffmpeg-9.0.2/$tool.exe") $tool) }
[void](Get-ToolVersion (Join-Path $BundleRoot 'tools/tdl-0.20.4/tdl.exe') tdl)
[void](Get-ToolVersion (Join-Path $BundleRoot 'tools/rclone-1.74.4/rclone.exe') rclone)
foreach ($name in @('COPYING', 'NOTICE.md')) {
    $noticePath = Join-Path $BundleRoot ('tools/rclone-1.74.4/' + $name)
    if (-not (Test-Path -LiteralPath $noticePath -PathType Leaf) -or (Get-Item -LiteralPath $noticePath).Length -eq 0) { throw 'The rclone copyright and license files are required.' }
}
$target = Assert-ChildPath (Join-Path $Root 'app/releases') (Join-Path $Root ('app/releases/' + $manifest.version))
if (Test-Path -LiteralPath $target) { throw 'Release version already installed. Existing releases are never overwritten.' }
$lock = Enter-DeploymentLock $Root
try {
    if (Test-Path -LiteralPath $target) { throw 'Release version already installed. Existing releases are never overwritten.' }
    $state = Get-ProcessState $Root
    if ($null -ne $state) {
        foreach ($record in $state.processes) { if ($null -ne (Get-RecordedProcess $record)) { throw 'Stop this deployment before activating another release.' } }
    }
    [void][IO.Directory]::CreateDirectory((Join-Path $Root 'app/releases'))
    [void][IO.Directory]::CreateDirectory((Join-Path $Root 'scripts/release'))
    # New immutable version only; pointer is written after every copy and tool check succeeds.
    Copy-Item -LiteralPath $source -Destination $target -Recurse
    foreach ($tool in @(@('ffmpeg-9.0.2', 'ffmpeg.exe', 'ffmpeg'), @('ffmpeg-9.0.2', 'ffprobe.exe', 'ffprobe'), @('tdl-0.20.4', 'tdl.exe', 'tdl'), @('rclone-1.74.4', 'rclone.exe', 'rclone'))) {
        $directory = Join-Path $Root ('tools/' + $tool[0])
        [void][IO.Directory]::CreateDirectory($directory)
        $executable = Join-Path $directory $tool[1]
        if (Test-Path -LiteralPath $executable) { [void](Get-ToolVersion $executable $tool[2]) }
        else { Copy-Item -LiteralPath (Join-Path $BundleRoot ('tools/' + $tool[0] + '/' + $tool[1])) -Destination $executable }
    }
    foreach ($name in @('COPYING', 'NOTICE.md')) {
        $destination = Join-Path $Root ('tools/rclone-1.74.4/' + $name)
        if (-not (Test-Path -LiteralPath $destination)) { Copy-Item -LiteralPath (Join-Path $BundleRoot ('tools/rclone-1.74.4/' + $name)) -Destination $destination }
    }
    foreach ($name in @('Common.ps1', 'Install-WindowsBundle.ps1', 'Start-AllDownloads.ps1', 'Stop-AllDownloads.ps1', 'Get-AllDownloadsStatus.ps1')) {
        Copy-Item -LiteralPath (Join-Path $BundleRoot ('scripts/release/' + $name)) -Destination (Join-Path $Root ('scripts/release/' + $name)) -Force
    }
    [void](Initialize-Runtime $Root $Port $JavaExecutable)
    Write-JsonFile (Join-Path $Root 'app/current.json') @{ version = $manifest.version }
    [pscustomobject]@{ status = 'INSTALLED_NOT_STARTED'; root = $Root; version = $manifest.version; start = (Join-Path $Root 'scripts/release/Start-AllDownloads.ps1') } | Format-List
} finally { $lock.Dispose() }
