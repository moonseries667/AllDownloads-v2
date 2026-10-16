#Requires -Version 5.1
[CmdletBinding()]
param([string] $GoExecutable = 'go.exe')
. (Join-Path $PSScriptRoot '../Common.ps1')
$repository = Get-FullPath (Join-Path $PSScriptRoot '../../..')
$testRoot = Join-Path $repository ('artifacts/release-tests-' + [Guid]::NewGuid().ToString('N'))
[void][IO.Directory]::CreateDirectory($testRoot)
$script:passed = 0

function Assert-True($Value, [string] $Message) {
    if (-not $Value) { throw "Assertion failed: $Message" }
}

function Assert-Throws([scriptblock] $Action, [string] $Pattern) {
    $caught = $null
    try { & $Action | Out-Null } catch { $caught = $_.Exception.Message }
    Assert-True ($null -ne $caught -and $caught -match $Pattern) "Expected failure /$Pattern/, got: $caught"
}

function Test-Case([string] $Name, [scriptblock] $Action) {
    & $Action
    $script:passed++
    Write-Output "PASS $Name"
}

function Get-FreePort {
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    try { return $listener.LocalEndpoint.Port } finally { $listener.Stop() }
}

function New-TestDeployment([string] $Name) {
    $root = Join-Path $testRoot $Name
    & (Join-Path $PSScriptRoot '../Install-WindowsBundle.ps1') -BundleRoot $script:bundle -Root $root -Port (Get-FreePort) -JavaExecutable (Join-Path $testRoot 'tool-bin/java.exe') | Out-Null
    return $root
}

$fixture = Join-Path $testRoot 'fixture.exe'
$go = Resolve-Executable $GoExecutable
Invoke-Checked $go @('build', '-trimpath', '-o', $fixture, (Join-Path $PSScriptRoot 'fixture.go'))
$toolBin = Join-Path $testRoot 'tool-bin'
[void][IO.Directory]::CreateDirectory($toolBin)
foreach ($name in @('java', 'tdl', 'ffmpeg', 'ffprobe', 'go', 'runner', 'session-setup')) { Copy-Item -LiteralPath $fixture -Destination (Join-Path $toolBin "$name.exe") }
$inputRoot = Join-Path $testRoot 'inputs'
[void][IO.Directory]::CreateDirectory((Join-Path $inputRoot 'frontend/assets'))
Write-NewText (Join-Path $inputRoot 'frontend/index.html') '<!doctype html><div id="app"></div><script src="/assets/app.js"></script>'
Write-NewText (Join-Path $inputRoot 'frontend/assets/app.js') 'console.log("HTTP fixture")'
Write-NewText (Join-Path $inputRoot 'frontend/release-build.json') '{"apiMode":"http","apiBase":"/api/v1"}'
Add-Type -AssemblyName System.IO.Compression.FileSystem
$jarPath = Join-Path $inputRoot 'backend.jar'
$zip = [IO.Compression.ZipFile]::Open($jarPath, [IO.Compression.ZipArchiveMode]::Create)
try { [void]$zip.CreateEntry('BOOT-INF/classes/dev/alldownloads/BackendApplication.class') } finally { $zip.Dispose() }
$output = Join-Path $testRoot 'output'
$script:bundle = Join-Path $output 'AllDownloads-v2-2026.10.16.1-windows-x64'
$buildArguments = @{ Version = '2026.10.16.1'; FFmpegDirectory = $toolBin; TdlExecutable = (Join-Path $toolBin 'tdl.exe'); GoExecutable = (Join-Path $toolBin 'go.exe'); UsePrebuilt = $true; NoArchive = $true; JavaJar = $jarPath; RunnerExecutable = (Join-Path $toolBin 'runner.exe'); SessionSetupExecutable = (Join-Path $toolBin 'session-setup.exe'); FrontendDirectory = (Join-Path $inputRoot 'frontend'); OutputDirectory = $output }
$deployments = [Collections.Generic.List[string]]::new()
try {
    Test-Case 'bundle contains only declared release artifacts and no runtime' {
        & (Join-Path $PSScriptRoot '../Build-WindowsBundle.ps1') @buildArguments | Out-Null
        Assert-True (Test-Path -LiteralPath (Join-Path $script:bundle 'release/config-templates/runner.example.json')) 'configuration template'
        Assert-True (-not (Test-Path -LiteralPath (Join-Path $script:bundle 'runtime'))) 'no runtime copied'
        Assert-True (-not (Test-Path -LiteralPath (Join-Path $script:bundle 'scripts/release/tests'))) 'no test executable or fixture copied'
    }
    Test-Case 'existing bundle version is preserved' {
        Assert-Throws { & (Join-Path $PSScriptRoot '../Build-WindowsBundle.ps1') @buildArguments } 'Output version already exists'
    }
    Test-Case 'demo build marker is refused' {
        [IO.File]::WriteAllText((Join-Path $inputRoot 'frontend/release-build.json'), '{"apiMode":"demo","apiBase":"/api/v1"}')
        Assert-Throws { & (Join-Path $PSScriptRoot '../Build-WindowsBundle.ps1') @buildArguments } 'demo or cross-origin'
        [IO.File]::WriteAllText((Join-Path $inputRoot 'frontend/release-build.json'), '{"apiMode":"http","apiBase":"/api/v1"}')
    }
    Test-Case 'frontend private files are refused before copying' {
        Write-NewText (Join-Path $inputRoot 'frontend/cookie.local.json') '{}'
        Assert-Throws { & (Join-Path $PSScriptRoot '../Build-WindowsBundle.ps1') @buildArguments } 'Unexpected frontend release file'
        Remove-Item -LiteralPath (Join-Path $inputRoot 'frontend/cookie.local.json')
    }
    Test-Case 'legacy path and unsafe versions are refused' {
        Assert-Throws { Assert-DeploymentRoot 'D:\Code\Other\AllDownload' } 'old AllDownload'
        Assert-Throws { Assert-DeploymentRoot 'D:\Code\Other\AllDownload\app' } 'old AllDownload'
        Assert-Throws { Assert-Version '../1' } 'safe, unique'
        Assert-Throws { Assert-Version '1.' } 'safe, unique'
        Assert-Throws { Assert-ChildPath $testRoot (Join-Path $testRoot '../outside') } 'must be below'
    }
    Test-Case 'SSH startup is refused before writing runtime or starting processes' {
        $previousSsh = $env:SSH_CONNECTION
        try {
            $env:SSH_CONNECTION = 'local-test-placeholder'
            $guardRoot = Join-Path $testRoot 'guard-not-started'
            Assert-Throws { & (Join-Path $PSScriptRoot '../Start-AllDownloads.ps1') -Root $guardRoot } 'Admin desktop session'
            Assert-True (-not (Test-Path -LiteralPath $guardRoot)) 'guard precedes all deployment writes'
        } finally { $env:SSH_CONNECTION = $previousSsh }
    }
    Test-Case 'fresh and stale runner observations use UTC across PowerShell date types' {
        $now = [DateTime]::UtcNow
        $health = ('{"data":{"runtime":{"services":[{"id":"java","state":"ACTIVE"},{"id":"go","state":"ACTIVE","lastSeenAt":"' + $now.ToString('o') + '"}]}}}') | ConvertFrom-Json
        Assert-True (Test-HealthPhase $health -Runner -NotBeforeUtcTicks $now.AddSeconds(-1).Ticks) 'fresh runner accepted'
        Assert-True (-not (Test-HealthPhase $health -Runner -NotBeforeUtcTicks $now.AddSeconds(1).Ticks)) 'stale runner rejected'
    }
    $root = New-TestDeployment 'deploy 空格'
    $deployments.Add($root)
    Test-Case 'installation initializes fresh runtime with no account sessions' {
        $runner = Read-JsonFile (Join-Path $root 'runtime/config/runner.local.json')
        Assert-True ($runner.allowedRoots[0] -eq (Join-Path $root 'downloads')) 'download root'
        Assert-True (@($runner.telegram.accounts.PSObject.Properties).Count -eq 0) 'no Telegram login'
        Assert-True ($runner.bilibili.cookieFile -eq (Join-Path $root 'runtime/auth/bilibili.session') -and $runner.bilibili.expectedUserId -eq '') 'one new Bilibili session path; first UID may be empty'
        $drive = $runner.driveAccounts.'drive-main'
        Assert-True ($drive.rootId -eq 'root' -and $drive.tokenFile -eq (Join-Path $root 'runtime/auth/drive.session') -and $drive.clientFile -eq (Join-Path $root 'runtime/auth/google-desktop-client.json')) 'new Drive login paths'
        Assert-True (-not ($drive.PSObject.Properties.Name -contains 'tokenEnv')) 'file authentication does not also set tokenEnv'
        foreach ($path in @($runner.bilibili.cookieFile, $drive.tokenFile, $drive.clientFile)) { Assert-True (-not (Test-Path -LiteralPath $path)) 'no platform credential or client file created by default' }
        Assert-True ((Get-Item -LiteralPath (Join-Path $root 'runtime/auth/runner.token')).Length -ge 32) 'new token exists (contents not read)'
    }
    Test-Case 'private config and internal token survive initialization' {
        $settingsPath = Join-Path $root 'runtime/config/deployment.local.json'
        $before = Get-Content -LiteralPath $settingsPath -Raw
        $runnerPath = Join-Path $root 'runtime/config/runner.local.json'
        $runnerBefore = Get-Content -LiteralPath $runnerPath -Raw
        $tokenTime = (Get-Item -LiteralPath (Join-Path $root 'runtime/auth/runner.token')).LastWriteTimeUtc
        [void](Initialize-Runtime $root 9090 'does-not-exist.exe')
        Assert-True ($before -eq (Get-Content -LiteralPath $settingsPath -Raw)) 'existing config kept'
        Assert-True ($runnerBefore -eq (Get-Content -LiteralPath $runnerPath -Raw)) 'existing runner config kept'
        Assert-True ($tokenTime -eq (Get-Item -LiteralPath (Join-Path $root 'runtime/auth/runner.token')).LastWriteTimeUtc) 'token not rewritten'
    }
    Test-Case 'lifecycle lock prevents simultaneous mutations' {
        $lock = Enter-DeploymentLock $root
        try { Assert-Throws { Enter-DeploymentLock $root } 'holds the deployment lock' } finally { $lock.Dispose() }
    }
    Test-Case 'runtime start reaches Java, Go, frontend and deep-route readiness' {
        $result = Start-Deployment $root 8
        Assert-True ($result.status -eq 'RUNNING') 'running after all checks'
        $state = Get-ProcessState $root
        Assert-True ($state.processes.Count -eq 2) 'only two recorded children'
        $status = & (Join-Path $PSScriptRoot '../Get-AllDownloadsStatus.ps1') -Root $root | ConvertFrom-Json
        Assert-True ($status.healthy -and $status.processes.Count -eq 2) 'status endpoint checks both recorded children'
        Assert-Throws { Start-Deployment $root 5 } 'live recorded process'
        Assert-Throws { & (Join-Path $PSScriptRoot '../Install-WindowsBundle.ps1') -BundleRoot $script:bundle -Root $root } 'already installed'
        $manifestPath = Join-Path $script:bundle 'release/release.json'
        $manifest = Read-JsonFile $manifestPath
        try {
            $manifest.version = '2026.10.16.2'
            Write-JsonFile $manifestPath $manifest
            Assert-Throws { & (Join-Path $PSScriptRoot '../Install-WindowsBundle.ps1') -BundleRoot $script:bundle -Root $root } 'Stop this deployment'
        } finally { $manifest.version = '2026.10.16.1'; Write-JsonFile $manifestPath $manifest }
    }
    Test-Case 'PID reuse mismatch cannot stop an unrelated live process' {
        $record = (Get-ProcessState $root).processes[0]
        $forged = [pscustomobject]@{ name = $record.name; pid = $record.pid; executablePath = $record.executablePath; startTimeTicks = '1' }
        Assert-True ($null -eq (Get-RecordedProcess $forged)) 'mismatched birth time rejected'
        Stop-RecordedProcesses ([pscustomobject]@{ processes = @($forged) })
        Assert-True ($null -ne (Get-RecordedProcess $record)) 'unmatched process remains alive'
    }
    Test-Case 'stop is scoped and repeatable' {
        & (Join-Path $PSScriptRoot '../Stop-AllDownloads.ps1') -Root $root | Out-Null
        & (Join-Path $PSScriptRoot '../Stop-AllDownloads.ps1') -Root $root | Out-Null
        foreach ($record in (Get-ProcessState $root).processes) { Assert-True ($null -eq (Get-RecordedProcess $record)) 'recorded process stopped' }
        Assert-True (Test-Path -LiteralPath (Join-Path $root 'runtime/config/runner.local.json')) 'private config preserved'
    }
    Test-Case 'new release activation preserves private configuration and installed tools' {
        $manifestPath = Join-Path $script:bundle 'release/release.json'
        $manifest = Read-JsonFile $manifestPath
        $configPath = Join-Path $root 'runtime/config/runner.local.json'
        $configBefore = Get-Content -LiteralPath $configPath -Raw
        $toolBefore = (Get-Item -LiteralPath (Join-Path $root 'tools/ffmpeg-9.0.2/ffmpeg.exe')).LastWriteTimeUtc
        try {
            $manifest.version = '2026.10.16.2'
            Write-JsonFile $manifestPath $manifest
            & (Join-Path $PSScriptRoot '../Install-WindowsBundle.ps1') -BundleRoot $script:bundle -Root $root | Out-Null
            Assert-True ((Read-JsonFile (Join-Path $root 'app/current.json')).version -eq '2026.10.16.2') 'new version activated'
            Assert-True (Test-Path -LiteralPath (Join-Path $root 'app/releases/2026.10.16.1/runner.exe')) 'old version retained'
            Assert-True ($configBefore -eq (Get-Content -LiteralPath $configPath -Raw)) 'private runner config retained'
            Assert-True ($toolBefore -eq (Get-Item -LiteralPath (Join-Path $root 'tools/ffmpeg-9.0.2/ffmpeg.exe')).LastWriteTimeUtc) 'existing tool not overwritten'
        } finally { $manifest.version = '2026.10.16.1'; Write-JsonFile $manifestPath $manifest }
    }
    foreach ($failure in @('fail-runner', 'fail-front')) {
        $failureRoot = New-TestDeployment $failure
        $deployments.Add($failureRoot)
        Write-NewText (Join-Path $failureRoot $failure) 'intentional fixture failure'
        Test-Case "$failure leaves diagnostics and stops only newly recorded children" {
            Assert-Throws { Start-Deployment $failureRoot 5 } 'Startup failed'
            $state = Get-ProcessState $failureRoot
            Assert-True ($state.status -eq 'START_FAILED') 'diagnostic state'
            foreach ($record in $state.processes) { Assert-True ($null -eq (Get-RecordedProcess $record)) 'failed children stopped' }
            Assert-True (@(Get-ChildItem -LiteralPath (Join-Path $failureRoot 'runtime/logs') -File).Count -ge 2) 'failure logs retained'
        }
    }
    Test-Case 'state persistence failure still cleans up the child already started' {
        $failureRoot = New-TestDeployment 'fail-state-write'
        $deployments.Add($failureRoot)
        $savedWriter = (Get-Command Write-JsonFile).ScriptBlock
        function Write-JsonFile([string] $Path, $Value) {
            if ($Value.status -eq 'STARTING' -and $Value.processes.Count -gt 0) { throw 'deliberate state write failure' }
            & $savedWriter $Path $Value
        }
        Assert-Throws { Start-Deployment $failureRoot 5 } 'deliberate state write failure'
        $state = Get-ProcessState $failureRoot
        Assert-True ($state.status -eq 'START_FAILED') 'failure state persisted after cleanup'
        Assert-True ($state.processes.Count -eq 1) 'one started child recorded'
        Assert-True ($null -eq (Get-RecordedProcess $state.processes[0])) 'child stopped despite persistence failure'
    }
    Test-Case 'occupied loopback port is not taken over' {
        $occupiedRoot = New-TestDeployment 'occupied'
        $deployments.Add($occupiedRoot)
        $settings = Read-JsonFile (Join-Path $occupiedRoot 'runtime/config/deployment.local.json')
        $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, [int]$settings.port)
        $listener.Start()
        try { Assert-Throws { Start-Deployment $occupiedRoot 5 } 'occupied' } finally { $listener.Stop() }
        Assert-True ($null -eq (Get-ProcessState $occupiedRoot)) 'no child process started'
    }
    Write-Output "$script:passed release checks passed. Synthetic processes only. Evidence: $testRoot"
} finally {
    foreach ($deployment in $deployments) {
        $state = Get-ProcessState $deployment
        if ($null -ne $state) { Stop-RecordedProcesses $state }
    }
}
