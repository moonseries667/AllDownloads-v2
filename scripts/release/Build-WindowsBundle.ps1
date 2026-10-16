#Requires -Version 5.1
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string] $Version,
    [Parameter(Mandatory)] [string] $FFmpegDirectory,
    [Parameter(Mandatory)] [string] $TdlExecutable,
    [Parameter(Mandatory)] [string] $RcloneExecutable,
    [string] $OutputDirectory,
    [string] $JavaHome = $env:JAVA_HOME,
    [string] $GoExecutable = 'go.exe',
    [string] $NpmExecutable = 'npm.cmd',
    [switch] $UsePrebuilt,
    [string] $JavaJar,
    [string] $RunnerExecutable,
    [string] $SessionSetupExecutable,
    [string] $FrontendDirectory,
    [switch] $NoArchive
)
. (Join-Path $PSScriptRoot 'Common.ps1')
Assert-Version $Version
$repository = Get-FullPath (Join-Path $PSScriptRoot '../..')
if (-not $OutputDirectory) { $OutputDirectory = Join-Path $repository 'dist' }
if (-not $JavaJar) { $JavaJar = Join-Path $repository 'backend/java/target/alldownloads-backend-0.1.0-SNAPSHOT.jar' }
if (-not $RunnerExecutable) { $RunnerExecutable = Join-Path $repository 'backend/go/bin/runner.exe' }
if (-not $SessionSetupExecutable) { $SessionSetupExecutable = Join-Path $repository 'backend/go/bin/session-setup.exe' }
if (-not $FrontendDirectory) { $FrontendDirectory = Join-Path $repository 'frontend/dist' }
$FFmpegDirectory = Get-FullPath $FFmpegDirectory
$TdlExecutable = Get-FullPath $TdlExecutable
$RcloneExecutable = Get-FullPath $RcloneExecutable
$go = Resolve-Executable $GoExecutable
$ffmpeg = Join-Path $FFmpegDirectory 'ffmpeg.exe'
$ffprobe = Join-Path $FFmpegDirectory 'ffprobe.exe'
foreach ($path in @($ffmpeg, $ffprobe, $TdlExecutable, $RcloneExecutable)) { Assert-PlainTree $path }
[void](Get-ToolVersion $ffmpeg ffmpeg)
[void](Get-ToolVersion $ffprobe ffprobe)
[void](Get-ToolVersion $TdlExecutable tdl)
[void](Get-ToolVersion $RcloneExecutable rclone)
$commit = (& git -c "safe.directory=$repository" -C $repository rev-parse HEAD | Out-String).Trim()
if ($LASTEXITCODE -ne 0) { throw 'Build from a Git checkout with an identifiable source commit.' }
$dirty = & git -c "safe.directory=$repository" -C $repository status --porcelain -- backend/java backend/go frontend
if ($LASTEXITCODE -ne 0 -or $dirty) { throw 'Commit module source changes before producing a release.' }
if (-not $UsePrebuilt) {
    if (-not (Test-Path -LiteralPath (Join-Path $repository 'backend/go/cmd/session-setup') -PathType Container)) { throw 'Integrate cmd/session-setup before building the deployable release.' }
    $nodeVersion = (& (Resolve-Executable 'node.exe') --version | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or $nodeVersion -notmatch '^v24\.') { throw 'Build the frontend with Node 24.' }
    if (-not $JavaHome) { throw 'Set JAVA_HOME or pass -JavaHome for Java 21.' }
    [void](Get-ToolVersion (Join-Path $JavaHome 'bin/java.exe') java)
    $oldJava = $env:JAVA_HOME
    $oldMode = $env:VITE_API_MODE
    $oldBase = $env:VITE_API_BASE
    $oldGoos = $env:GOOS
    $oldGoarch = $env:GOARCH
    try {
        $env:JAVA_HOME = $JavaHome
        Push-Location (Join-Path $repository 'backend/java')
        try { Invoke-Checked '.\mvnw.cmd' @('--batch-mode', '--no-transfer-progress', '-DskipTests', 'package') } finally { Pop-Location }
        $env:GOOS = 'windows'; $env:GOARCH = 'amd64'
        [void][IO.Directory]::CreateDirectory((Split-Path -Parent $RunnerExecutable))
        Push-Location (Join-Path $repository 'backend/go')
        try {
            Invoke-Checked $go @('build', '-trimpath', '-ldflags=-s -w', '-o', $RunnerExecutable, './cmd/runner')
            Invoke-Checked $go @('build', '-trimpath', '-ldflags=-s -w', '-o', $SessionSetupExecutable, './cmd/session-setup')
        } finally { Pop-Location }
        $env:VITE_API_MODE = 'http'; $env:VITE_API_BASE = '/api/v1'
        Push-Location (Join-Path $repository 'frontend')
        try {
            try { Invoke-Checked $NpmExecutable @('ci', '--registry=https://registry.npmmirror.com') }
            catch {
                Write-Warning 'The mirror install failed. Retrying once with the official npm registry and the same lockfile.'
                Invoke-Checked $NpmExecutable @('ci', '--registry=https://registry.npmjs.org')
            }
            Invoke-Checked $NpmExecutable @('run', 'build')
        } finally { Pop-Location }
    } finally {
        $env:JAVA_HOME = $oldJava; $env:VITE_API_MODE = $oldMode; $env:VITE_API_BASE = $oldBase
        $env:GOOS = $oldGoos; $env:GOARCH = $oldGoarch
    }
}
# A prebuilt input must still be the production command, built without Carbon source paths.
foreach ($path in @($JavaJar, $RunnerExecutable, $SessionSetupExecutable, $FrontendDirectory)) { Assert-PlainTree $path }
foreach ($command in @(@('runner', $RunnerExecutable), @('session-setup', $SessionSetupExecutable))) {
    $buildInfo = (& $go version -m $command[1] 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $buildInfo -notmatch ('path\s+github.com/moonseries667/AllDownloads-v2/backend/go/cmd/' + $command[0] + '\s') -or
        $buildInfo -notmatch '-trimpath=true' -or $buildInfo -notmatch 'GOOS=windows' -or $buildInfo -notmatch 'GOARCH=amd64') { throw "$($command[0]) must be the windows/amd64 production command built with -trimpath." }
}
Add-Type -AssemblyName System.IO.Compression.FileSystem
$jar = [IO.Compression.ZipFile]::OpenRead((Get-FullPath $JavaJar))
try {
    if (-not $jar.GetEntry('BOOT-INF/classes/dev/alldownloads/BackendApplication.class')) { throw 'Expected the Spring Boot production jar, not a plain/test jar.' }
    foreach ($entry in $jar.Entries) {
        if ($entry.FullName -match '^BOOT-INF/classes/' -and $entry.FullName -match '(?i)(\.local[./]|\.db(?:-wal|-shm)?$|\.sqlite[0-9]?(?:-wal|-shm)?$|\.session(?:-journal)?$|/\.env(?:\.|$))') { throw 'A private runtime file was found in the jar.' }
    }
} finally { $jar.Dispose() }
$indexPath = Join-Path $FrontendDirectory 'index.html'
if (-not (Test-Path -LiteralPath $indexPath -PathType Leaf)) { throw 'Frontend dist/index.html is missing.' }
$textExtensions = @('.html', '.js', '.css', '.svg', '.json', '.txt', '.webmanifest')
$allowedExtensions = $textExtensions + @('.png', '.jpg', '.jpeg', '.gif', '.webp', '.ico', '.woff', '.woff2', '.ttf', '.avif')
foreach ($file in Get-ChildItem -LiteralPath $FrontendDirectory -File -Recurse -Force) {
    if ($file.Extension -notin $allowedExtensions -or $file.Name -match '(?i)(\.local\.|\.local$|cookie|session|credential|^\.env)') { throw "Unexpected frontend release file: $($file.Name)" }
    if ($file.Extension -in $textExtensions) {
        $content = Get-Content -LiteralPath $file.FullName -Raw -Encoding UTF8
        if ($content -match '(?i)[A-Z]:[\\/](?:Users[\\/]ThinkPad|Code[\\/](?:Other|Env))[\\/]') { throw "Carbon absolute path found in frontend output: $($file.Name)" }
    }
}
# Build-mode evidence is explicit and local to the dist being packaged.
$modePath = Join-Path $FrontendDirectory 'release-build.json'
if (-not (Test-Path -LiteralPath $modePath -PathType Leaf)) {
    if ($UsePrebuilt) { throw 'Prebuilt frontend requires release-build.json with apiMode=http and apiBase=/api/v1; create it only after that exact build.' }
    Write-NewText $modePath (@{ apiMode = 'http'; apiBase = '/api/v1' } | ConvertTo-Json)
}
$mode = Read-JsonFile $modePath
if ($mode.apiMode -ne 'http' -or $mode.apiBase -ne '/api/v1') { throw 'Refusing to package a demo or cross-origin frontend.' }
$OutputDirectory = Get-FullPath $OutputDirectory
[void][IO.Directory]::CreateDirectory($OutputDirectory)
$bundle = Join-Path $OutputDirectory "AllDownloads-v2-$Version-windows-x64"
$archive = $bundle + '.zip'
if ((Test-Path -LiteralPath $bundle) -or (Test-Path -LiteralPath $archive)) { throw 'Output version already exists. Use a new version; old outputs are preserved.' }
$release = Join-Path $bundle 'release'
foreach ($directory in @($release, (Join-Path $release 'frontend'), (Join-Path $release 'config-templates'), (Join-Path $bundle 'scripts/release'), (Join-Path $bundle 'tools/ffmpeg-9.0.2'), (Join-Path $bundle 'tools/tdl-0.20.4'), (Join-Path $bundle 'tools/rclone-1.74.4'))) { [void][IO.Directory]::CreateDirectory($directory) }
Copy-Item -LiteralPath $JavaJar -Destination (Join-Path $release 'backend.jar')
Copy-Item -LiteralPath $RunnerExecutable -Destination (Join-Path $release 'runner.exe')
Copy-Item -LiteralPath $SessionSetupExecutable -Destination (Join-Path $release 'session-setup.exe')
Get-ChildItem -LiteralPath $FrontendDirectory -Force | Copy-Item -Destination (Join-Path $release 'frontend') -Recurse
Copy-Item -LiteralPath $ffmpeg, $ffprobe -Destination (Join-Path $bundle 'tools/ffmpeg-9.0.2')
Copy-Item -LiteralPath $TdlExecutable -Destination (Join-Path $bundle 'tools/tdl-0.20.4/tdl.exe')
Copy-Item -LiteralPath $RcloneExecutable -Destination (Join-Path $bundle 'tools/rclone-1.74.4/rclone.exe')
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'licenses/rclone-1.74.4/COPYING') -Destination (Join-Path $bundle 'tools/rclone-1.74.4/COPYING')
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'licenses/rclone-1.74.4/NOTICE.md') -Destination (Join-Path $bundle 'tools/rclone-1.74.4/NOTICE.md')
# Validate the copied executables too, so a source tool's adjacent DLLs cannot hide an incomplete bundle.
foreach ($name in @('ffmpeg', 'ffprobe')) { [void](Get-ToolVersion (Join-Path $bundle "tools/ffmpeg-9.0.2/$name.exe") $name) }
[void](Get-ToolVersion (Join-Path $bundle 'tools/tdl-0.20.4/tdl.exe') tdl)
[void](Get-ToolVersion (Join-Path $bundle 'tools/rclone-1.74.4/rclone.exe') rclone)
foreach ($name in @('deployment.example.json', 'runner.example.json')) {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot ('templates/' + $name)) -Destination (Join-Path $release 'config-templates')
}
foreach ($name in @('Common.ps1', 'Install-WindowsBundle.ps1', 'Start-AllDownloads.ps1', 'Stop-AllDownloads.ps1', 'Get-AllDownloadsStatus.ps1')) {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot $name) -Destination (Join-Path $bundle 'scripts/release')
}
$guide = Get-Content -LiteralPath (Join-Path $repository 'docs/Y7000P.md') -Raw -Encoding UTF8
$guide = $guide.Replace('(../backend/go/README.md)', "(https://github.com/moonseries667/AllDownloads-v2/blob/$commit/backend/go/README.md)").Replace('(ACCEPTANCE.md)', "(https://github.com/moonseries667/AllDownloads-v2/blob/$commit/docs/ACCEPTANCE.md)")
Write-NewText (Join-Path $bundle 'Y7000P.md') $guide
$manifest = [ordered]@{ schemaVersion = 1; version = $Version; platform = 'windows-x64'; sourceCommit = $commit; createdAt = [DateTime]::UtcNow.ToString('o'); java = '21'; ffmpeg = '9.0.2'; ffprobe = '9.0.2'; tdl = '0.20.4'; rclone = '1.74.4'; sessionSetup = $true; frontend = @{ apiMode = 'http'; apiBase = '/api/v1' } }
Write-NewText (Join-Path $release 'release.json') ($manifest | ConvertTo-Json -Depth 5)
if (-not $NoArchive) { Compress-Archive -LiteralPath $bundle -DestinationPath $archive -CompressionLevel Optimal }
[pscustomobject]@{ bundle = $bundle; archive = $(if ($NoArchive) { $null } else { $archive }); version = $Version; sourceCommit = $commit } | Format-List
