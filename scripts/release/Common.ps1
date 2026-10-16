# Shared by the Windows release entry points. Requires Windows PowerShell 5.1+.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Get-FullPath([string] $Path) {
    return [IO.Path]::GetFullPath($Path).TrimEnd('\', '/')
}

function Assert-ChildPath([string] $Parent, [string] $Path) {
    $parentPath = Get-FullPath $Parent
    $childPath = Get-FullPath $Path
    if (-not $childPath.StartsWith($parentPath + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Path must be below $parentPath : $childPath"
    }
    return $childPath
}

function Assert-DeploymentRoot([string] $Root) {
    $path = Get-FullPath $Root
    if ($path -eq [IO.Path]::GetPathRoot($path).TrimEnd('\', '/')) { throw 'A drive root cannot be the deployment root.' }
    $legacy = 'D:\Code\Other\AllDownload'
    if ($path.Equals($legacy, [StringComparison]::OrdinalIgnoreCase) -or $path.StartsWith($legacy + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw 'The old AllDownload directory is outside this deployment.'
    }
    return $path
}

function Assert-Version([string] $Version) {
    if ($Version -notmatch '^[0-9](?:[A-Za-z0-9._-]{0,62}[A-Za-z0-9_-])?$' -or $Version.Contains('..')) { throw 'Version must be a safe, unique directory name beginning with a digit.' }
}

function Read-JsonFile([string] $Path) {
    return Get-Content -LiteralPath $Path -Raw -Encoding UTF8 | ConvertFrom-Json
}

function Write-NewText([string] $Path, [string] $Text) {
    $bytes = [Text.UTF8Encoding]::new($false).GetBytes($Text)
    $stream = [IO.File]::Open($Path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try { $stream.Write($bytes, 0, $bytes.Length) } finally { $stream.Dispose() }
}

function Write-JsonFile([string] $Path, $Value) {
    # Serialize to a sibling, then replace the small pointer/state file atomically.
    $temporary = $Path + '.' + [Guid]::NewGuid().ToString('N') + '.tmp'
    Write-NewText $temporary ($Value | ConvertTo-Json -Depth 30)
    if (Test-Path -LiteralPath $Path) { [IO.File]::Replace($temporary, $Path, [NullString]::Value) }
    else { [IO.File]::Move($temporary, $Path) }
}

function Assert-PlainTree([string] $Path) {
    $item = Get-Item -LiteralPath $Path
    $items = @($item)
    if ($item.PSIsContainer) { $items += @(Get-ChildItem -LiteralPath $Path -Recurse -Force) }
    foreach ($entry in $items) {
        if ($entry.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Links are not accepted in release inputs: $($entry.FullName)" }
    }
}

function Resolve-Executable([string] $Executable) {
    $command = Get-Command -Name $Executable -CommandType Application -ErrorAction Stop | Select-Object -First 1
    return (Get-Item -LiteralPath $command.Source).FullName
}

function Invoke-Checked([string] $Executable, [string[]] $Arguments) {
    & $Executable @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Command failed ($LASTEXITCODE): $Executable" }
}

function Get-ToolVersion([string] $Executable, [ValidateSet('ffmpeg', 'ffprobe', 'tdl', 'java')] [string] $Tool) {
    $arguments = @('-version')
    if ($Tool -eq 'tdl') { $arguments = @('version') }
    $process = [Diagnostics.Process]::new()
    $process.StartInfo.FileName = $Executable
    $process.StartInfo.Arguments = ($arguments | ForEach-Object { ConvertTo-ProcessArgument $_ }) -join ' '
    $process.StartInfo.UseShellExecute = $false
    $process.StartInfo.CreateNoWindow = $true
    $process.StartInfo.RedirectStandardOutput = $true
    $process.StartInfo.RedirectStandardError = $true
    if ($Tool -eq 'tdl') {
        # tdl initializes its home even for `version`. Never let that touch a user's default .tdl.
        $probeHome = Get-FullPath (Join-Path ([IO.Path]::GetTempPath()) 'AllDownloads-v2/tool-probes/tdl-0.20.4')
        [void][IO.Directory]::CreateDirectory($probeHome)
        $process.StartInfo.EnvironmentVariables['USERPROFILE'] = $probeHome
        $process.StartInfo.EnvironmentVariables['HOME'] = $probeHome
        $process.StartInfo.EnvironmentVariables['TDL_DEBUG'] = 'false'
    }
    try {
        [void]$process.Start()
        $stdout = $process.StandardOutput.ReadToEndAsync()
        $stderr = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(15000)) { $process.Kill(); throw "$Tool version query timed out." }
        $output = $stdout.GetAwaiter().GetResult() + $stderr.GetAwaiter().GetResult()
        if ($process.ExitCode -ne 0) { throw "Cannot query $Tool version." }
    } finally { $process.Dispose() }
    switch ($Tool) {
        'java' { if ($output -notmatch 'version\s+"21(?:\.|["+])') { throw 'Java 21 is required.' }; return '21' }
        'tdl' { if ($output -notmatch '(?im)^\s*Version:\s*v?0\.20\.4\s*$') { throw 'tdl 0.20.4 is required.' }; return '0.20.4' }
        default { if ($output -notmatch ('(?m)^' + $Tool + ' version 9\.0\.2(?:[-\s]|$)')) { throw "$Tool 9.0.2 is required; system FFmpeg is not substituted." }; return '9.0.2' }
    }
}

function Get-Release([string] $Root) {
    $pointer = Read-JsonFile (Join-Path $Root 'app/current.json')
    Assert-Version $pointer.version
    $releasePath = Assert-ChildPath (Join-Path $Root 'app/releases') (Join-Path $Root ('app/releases/' + $pointer.version))
    $manifest = Read-JsonFile (Join-Path $releasePath 'release.json')
    if ($manifest.schemaVersion -ne 1 -or $manifest.version -ne $pointer.version -or $manifest.platform -ne 'windows-x64') { throw 'Unsupported or inconsistent release manifest.' }
    foreach ($file in @('backend.jar', 'runner.exe', 'session-setup.exe', 'frontend/index.html')) {
        if (-not (Test-Path -LiteralPath (Join-Path $releasePath $file) -PathType Leaf)) { throw "Incomplete release: $file" }
    }
    return [pscustomobject]@{ Path = $releasePath; Manifest = $manifest }
}

function Initialize-Runtime([string] $Root, [int] $Port = 8080, [string] $JavaExecutable = 'java.exe') {
    if ($Port -lt 1024 -or $Port -gt 65535) { throw 'Port must be between 1024 and 65535.' }
    foreach ($directory in @('runtime/config', 'runtime/auth', 'runtime/control', 'runtime/runner', 'runtime/logs', 'runtime/processes', 'downloads')) {
        [void][IO.Directory]::CreateDirectory((Join-Path $Root $directory))
    }
    $settingsPath = Join-Path $Root 'runtime/config/deployment.local.json'
    if (-not (Test-Path -LiteralPath $settingsPath)) {
        Write-NewText $settingsPath ([ordered]@{ schemaVersion = 1; port = $Port; javaExecutable = $JavaExecutable } | ConvertTo-Json)
    }
    $settings = Read-JsonFile $settingsPath
    if ($settings.schemaVersion -ne 1 -or $settings.port -lt 1024 -or $settings.port -gt 65535) { throw 'Invalid deployment.local.json schemaVersion or port.' }
    $runnerPath = Join-Path $Root 'runtime/config/runner.local.json'
    if (-not (Test-Path -LiteralPath $runnerPath)) {
        $configuration = [ordered]@{
            runnerId = 'runner-y7000p'; controlUrl = "http://127.0.0.1:$($settings.port)"
            tokenFile = (Join-Path $Root 'runtime/auth/runner.token'); stateDir = (Join-Path $Root 'runtime/runner')
            allowedRoots = @((Join-Path $Root 'downloads')); roots = @{ 'download-root' = (Join-Path $Root 'downloads') }
            ffmpeg = (Join-Path $Root 'tools/ffmpeg-9.0.2/ffmpeg.exe'); ffprobe = (Join-Path $Root 'tools/ffmpeg-9.0.2/ffprobe.exe')
            bilibili = @{ cookieFile = (Join-Path $Root 'runtime/auth/bilibili.session'); expectedUserId = '' }
            telegram = @{ executable = (Join-Path $Root 'tools/tdl-0.20.4/tdl.exe'); accounts = @{} }
            quarkAccounts = @{}
            driveAccounts = @{ 'drive-main' = @{ rootId = 'root'; tokenFile = (Join-Path $Root 'runtime/auth/drive.session'); clientFile = (Join-Path $Root 'runtime/auth/google-desktop-client.json') } }
        }
        Write-NewText $runnerPath ($configuration | ConvertTo-Json -Depth 10)
    }
    # The only secret created here is a new v2 internal token. No existing file is read or printed.
    $tokenPath = Join-Path $Root 'runtime/auth/runner.token'
    if (-not (Test-Path -LiteralPath $tokenPath)) {
        $random = New-Object byte[] 32
        $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
        try { $rng.GetBytes($random) } finally { $rng.Dispose() }
        Write-NewText $tokenPath ([Convert]::ToBase64String($random).TrimEnd('=').Replace('+', '-').Replace('/', '_'))
        [Array]::Clear($random, 0, $random.Length)
    }
    return $settings
}

function Assert-InteractiveSession {
    $process = Get-Process -Id $PID
    $user = [Security.Principal.WindowsIdentity]::GetCurrent().Name.Split('\')[-1]
    if ($user -ne 'Admin' -or $process.SessionId -eq 0 -or -not [Environment]::UserInteractive -or $env:SSH_CONNECTION) {
        throw 'Start explicitly in the signed-in Admin desktop session on Y7000P. SSH and Session 0 cannot provide the native folder picker.'
    }
}

function Enter-DeploymentLock([string] $Root) {
    [void][IO.Directory]::CreateDirectory((Join-Path $Root 'runtime/processes'))
    try { return [IO.File]::Open((Join-Path $Root 'runtime/processes/lifecycle.lock'), [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None) }
    catch { throw 'Another install/start/stop operation holds the deployment lock.' }
}

function Get-RecordedProcess($Record) {
    if ($Record.name -notin @('java', 'runner')) { throw 'Invalid process record name.' }
    $process = Get-Process -Id ([int]$Record.pid) -ErrorAction SilentlyContinue
    if ($null -eq $process) { return $null }
    try {
        if ($process.StartTime.Ticks.ToString() -ne [string]$Record.startTimeTicks -or
            -not $process.Path.Equals([string]$Record.executablePath, [StringComparison]::OrdinalIgnoreCase)) { return $null }
    } catch { throw 'Cannot verify a recorded process; no process was stopped.' }
    return $process
}

function Get-ProcessState([string] $Root) {
    $path = Join-Path $Root 'runtime/processes/active.json'
    if (-not (Test-Path -LiteralPath $path)) { return $null }
    $state = Read-JsonFile $path
    if ($state.schemaVersion -ne 1 -or -not $state.root.Equals($Root, [StringComparison]::OrdinalIgnoreCase)) { throw 'Process state belongs to another deployment.' }
    Assert-Version $state.version
    $names = @{}
    foreach ($record in $state.processes) {
        if ($names.ContainsKey($record.name)) { throw 'Duplicate process record.' }
        $names[$record.name] = $true
        [void](Get-RecordedProcess $record)
    }
    return $state
}

function Stop-RecordedProcesses($State) {
    # Stop the runner first, then Java. Never search by java/node image name or kill a tree.
    foreach ($name in @('runner', 'java')) {
        foreach ($record in @($State.processes | Where-Object { $_.name -eq $name })) {
            $process = Get-RecordedProcess $record
            if ($null -ne $process) {
                Stop-Process -InputObject $process -ErrorAction Stop
                if (-not $process.WaitForExit(10000)) { throw "Timed out stopping deployment $name PID $($record.pid)." }
            }
        }
    }
}

function ConvertTo-ProcessArgument([string] $Value) {
    # Windows command-line quoting, including trailing backslashes before the closing quote.
    return '"' + [regex]::Replace([regex]::Replace($Value, '(\\*)"', '$1$1\"'), '(\\+)$', '$1$1') + '"'
}

function Start-ManagedProcess([string] $Name, [string] $Executable, [string[]] $Arguments, [string] $WorkingDirectory, [string] $LogPrefix) {
    $argumentLine = ($Arguments | ForEach-Object { ConvertTo-ProcessArgument $_ }) -join ' '
    $healthNotBefore = [DateTime]::UtcNow.Ticks.ToString()
    $process = Start-Process -FilePath $Executable -ArgumentList $argumentLine -WorkingDirectory $WorkingDirectory -WindowStyle Hidden -PassThru -RedirectStandardOutput ($LogPrefix + '.stdout.log') -RedirectStandardError ($LogPrefix + '.stderr.log')
    try {
        $started = $process.StartTime.Ticks.ToString()
        if ($process.HasExited) { throw "$Name exited during startup; inspect $LogPrefix.*.log" }
        return [pscustomobject]@{ name = $Name; pid = $process.Id; startTimeTicks = $started; healthNotBeforeUtcTicks = $healthNotBefore; executablePath = $Executable }
    } catch {
        if (-not $process.HasExited) { Stop-Process -InputObject $process }
        throw
    }
}

function Read-LocalHealth([int] $Port) {
    return Invoke-RestMethod -Uri "http://127.0.0.1:$Port/api/v1/health" -Method Get -TimeoutSec 2 -ErrorAction Stop
}

function Test-HealthPhase($Health, [switch] $Runner, [long] $NotBeforeUtcTicks = 0) {
    try {
        if (-not $Health -or -not $Health.data -or -not $Health.data.runtime) { return $false }
        $services = @($Health.data.runtime.services)
        $java = @($services | Where-Object { $_.id -eq 'java' -and $_.state -eq 'ACTIVE' })
        if ($java.Count -ne 1) { return $false }
        if ($Runner) {
            $go = @($services | Where-Object { $_.id -eq 'go' -and $_.state -eq 'ACTIVE' })
            if ($go.Count -ne 1) { return $false }
            if ($NotBeforeUtcTicks -gt 0) {
                # PS 7 may deserialize ISO dates to DateTime; PS 5.1 leaves strings.
                # Parsing a DateTime via its timezone-free string would subtract the local offset twice.
                $seen = $go[0].lastSeenAt
                if ($seen -is [DateTime]) { $seenTicks = $seen.ToUniversalTime().Ticks }
                elseif ($seen -is [DateTimeOffset]) { $seenTicks = $seen.UtcDateTime.Ticks }
                else { $seenTicks = [DateTimeOffset]::Parse([string]$seen, [Globalization.CultureInfo]::InvariantCulture).UtcDateTime.Ticks }
                if ($seenTicks -lt $NotBeforeUtcTicks) { return $false }
            }
        }
        return $true
    } catch { return $false }
}

function Wait-DeploymentHealth([int] $Port, $Record, [int] $TimeoutSeconds, [switch] $Runner) {
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        if ($null -eq (Get-RecordedProcess $Record)) { throw "$($Record.name) exited before becoming healthy." }
        try { if (Test-HealthPhase (Read-LocalHealth $Port) -Runner:$Runner -NotBeforeUtcTicks ([long]$Record.healthNotBeforeUtcTicks)) { return } } catch { }
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Health check timed out for $($Record.name) on port $Port."
}

function Start-Deployment([string] $Root, [int] $TimeoutSeconds = 60) {
    $release = Get-Release $Root
    $settings = Initialize-Runtime $Root
    $existing = Get-ProcessState $Root
    if ($null -ne $existing) {
        foreach ($record in $existing.processes) {
            if ($null -ne (Get-RecordedProcess $record)) { throw 'This deployment has a live recorded process. Run Stop-AllDownloads.ps1 first.' }
        }
    }
    $java = Resolve-Executable $settings.javaExecutable
    [void](Get-ToolVersion $java java)
    $runnerConfigurationPath = Join-Path $Root 'runtime/config/runner.local.json'
    $runner = Read-JsonFile $runnerConfigurationPath
    # Inspect structural settings, never token/cookie/session contents.
    if ($runner.controlUrl -ne "http://127.0.0.1:$($settings.port)" -or
        $runner.tokenFile -ne (Join-Path $Root 'runtime/auth/runner.token') -or
        $runner.stateDir -ne (Join-Path $Root 'runtime/runner')) { throw 'Runner controlUrl/tokenFile/stateDir must match this deployment.' }
    if ($runner.PSObject.Properties.Name -contains 'isolatedTestMode' -and $runner.isolatedTestMode) { throw 'A release cannot use isolatedTestMode.' }
    if ($runner.PSObject.Properties.Name -contains 'fixtureCatalog' -and $runner.fixtureCatalog) { throw 'A release cannot use fixtureCatalog.' }
    if ($runner.ffmpeg -ne (Join-Path $Root 'tools/ffmpeg-9.0.2/ffmpeg.exe') -or
        $runner.ffprobe -ne (Join-Path $Root 'tools/ffmpeg-9.0.2/ffprobe.exe') -or
        $runner.telegram.executable -ne (Join-Path $Root 'tools/tdl-0.20.4/tdl.exe')) { throw 'Use the versioned tools installed in this deployment.' }
    [void](Get-ToolVersion $runner.ffmpeg ffmpeg)
    [void](Get-ToolVersion $runner.ffprobe ffprobe)
    [void](Get-ToolVersion $runner.telegram.executable tdl)
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, [int]$settings.port)
    try { $listener.Start() } catch { throw "Port $($settings.port) is occupied. No existing process will be stopped." } finally { $listener.Stop() }
    $stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfffZ')
    $statePath = Join-Path $Root 'runtime/processes/active.json'
    $state = [ordered]@{ schemaVersion = 1; root = $Root; version = $release.Manifest.version; port = $settings.port; status = 'STARTING'; startedAt = [DateTime]::UtcNow.ToString('o'); logPrefix = $stamp; processes = @() }
    Write-JsonFile $statePath $state
    try {
        # Generated launch settings contain paths only; personal runner configuration stays untouched.
        $properties = @(
            'server.address=127.0.0.1', "server.port=$($settings.port)",
            ('alldownloads.database=' + (Join-Path $Root 'runtime/control/business.db').Replace('\', '/')),
            ('alldownloads.runner-token-file=' + (Join-Path $Root 'runtime/auth/runner.token').Replace('\', '/')),
            ('alldownloads.download-root=' + [string]$runner.roots.'download-root').Replace('\', '/'),
            ('alldownloads.frontend-root=' + (Join-Path $release.Path 'frontend').Replace('\', '/')),
            'alldownloads.allowed-origins='
        )
        $launchPath = Join-Path $Root 'runtime/config/launch.properties'
        # Java properties requires escaping non-ASCII as well as backslash/special leading characters.
        $properties = $properties | ForEach-Object {
            $builder = [Text.StringBuilder]::new()
            foreach ($character in $_.ToCharArray()) {
                if ([int]$character -gt 127) { [void]$builder.Append(('\u{0:x4}' -f [int]$character)) }
                else { [void]$builder.Append($character) }
            }
            $builder.ToString()
        }
        [IO.File]::WriteAllLines($launchPath, [string[]]$properties, [Text.UTF8Encoding]::new($false))
        $javaArguments = @('-jar', (Join-Path $release.Path 'backend.jar'), ('--spring.config.additional-location=' + ([Uri]$launchPath).AbsoluteUri), '--server.address=127.0.0.1', "--server.port=$($settings.port)")
        $javaRecord = Start-ManagedProcess java $java $javaArguments $Root (Join-Path $Root "runtime/logs/$stamp-java")
        $state.processes += $javaRecord
        Write-JsonFile $statePath $state
        Wait-DeploymentHealth $settings.port $javaRecord $TimeoutSeconds
        $runnerRecord = Start-ManagedProcess runner (Join-Path $release.Path 'runner.exe') @('-config', $runnerConfigurationPath) $Root (Join-Path $Root "runtime/logs/$stamp-runner")
        $state.processes += $runnerRecord
        Write-JsonFile $statePath $state
        Wait-DeploymentHealth $settings.port $runnerRecord $TimeoutSeconds -Runner
        if ($null -eq (Get-RecordedProcess $javaRecord)) { throw 'Java exited while the runner was starting.' }
        $page = Invoke-WebRequest -Uri "http://127.0.0.1:$($settings.port)/" -UseBasicParsing -TimeoutSec 3
        if ($page.StatusCode -ne 200 -or $page.Content -notmatch '<div\s+id="app"') { throw 'Java is not serving the production frontend.' }
        $deep = Invoke-WebRequest -Uri "http://127.0.0.1:$($settings.port)/settings" -UseBasicParsing -TimeoutSec 3
        if ($deep.StatusCode -ne 200 -or $deep.Content -notmatch '<div\s+id="app"') { throw 'Frontend deep-route fallback is unavailable.' }
        if ($null -eq (Get-RecordedProcess $runnerRecord)) { throw 'Runner exited while checking the frontend.' }
        $state.status = 'RUNNING'
        Write-JsonFile $statePath $state
        return [pscustomobject]@{ status = 'RUNNING'; version = $state.version; url = "http://127.0.0.1:$($settings.port)"; logs = (Join-Path $Root 'runtime/logs') }
    } catch {
        $failure = $_
        $state.status = 'START_FAILED'
        try { Stop-RecordedProcesses ([pscustomobject]$state) } catch { Write-Warning 'Startup cleanup could not confirm a recorded process stopped. Run the stop entry point and inspect state.' }
        try { Write-JsonFile $statePath $state } catch { Write-Warning 'Failed to persist startup diagnostics; inspect the per-launch logs.' }
        throw "Startup failed: $($failure.Exception.Message) Logs: $(Join-Path $Root 'runtime/logs'); state: $statePath"
    }
}
