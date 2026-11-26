# Tests for website/install.ps1: runs the script against a fake release server
# and checks what it installs and what it refuses.
#
#   pwsh -NoProfile -File scripts/test-install-ps1.ps1
#   powershell -NoProfile -File scripts/test-install-ps1.ps1   (Windows PowerShell 5.1)
#
# The script under test runs in a child process, so its exit code is what a user
# sees. It runs in test mode (NITPICK_INSTALL_TEST), which skips the operating
# system check and does not run the file it installs: the file is a stand-in.

param(
    [string]$Script = (Join-Path $PSScriptRoot '..\website\install.ps1')
)

$ErrorActionPreference = 'Stop'
$Script = (Resolve-Path $Script).Path
$isWin = ($PSVersionTable.PSEdition -ne 'Core') -or $IsWindows
$shell = (Get-Process -Id $PID).Path
$version = 'v9.9.9'
$asset = "nitpick_${version}_windows_amd64.exe"
$failures = New-Object System.Collections.ArrayList

function Check([bool]$Condition, [string]$Name, [string]$Detail = '') {
    if ($Condition) {
        Write-Host "ok      $Name"
    } else {
        Write-Host "FAILED  $Name"
        if ($Detail) { Write-Host ($Detail -replace '(?m)^', '        ') }
        [void]$failures.Add($Name)
    }
}

function Get-Sha([byte[]]$Bytes) {
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try { return ([System.BitConverter]::ToString($sha.ComputeHash($Bytes)) -replace '-', '').ToLowerInvariant() } finally { $sha.Dispose() }
}

# A server for the fake release, running in its own runspace.
$tcp = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$tcp.Start(); $port = $tcp.LocalEndpoint.Port; $tcp.Stop()
$base = "http://127.0.0.1:$port"
$state = [hashtable]::Synchronized(@{
    files  = [hashtable]::Synchronized(@{})
    log    = [System.Collections.ArrayList]::Synchronized((New-Object System.Collections.ArrayList))
    latest = ''
})
$listener = [System.Net.HttpListener]::new()
$listener.Prefixes.Add("$base/")
$listener.Start()
$server = [powershell]::Create()
[void]$server.AddScript({
    param($listener, $state)
    while ($listener.IsListening) {
        try { $ctx = $listener.GetContext() } catch { break }
        $path = $ctx.Request.Url.AbsolutePath
        [void]$state.log.Add($path)
        $body = $null; $status = 200
        if ($path -eq '/latest') {
            if ($state.latest) { $body = [System.Text.Encoding]::UTF8.GetBytes("{`"tag_name`": `"$($state.latest)`"}") } else { $status = 404 }
        } elseif ($path.StartsWith('/dl/') -and $state.files.ContainsKey($path.Substring(4))) {
            $body = $state.files[$path.Substring(4)]
        } else { $status = 404 }
        $ctx.Response.StatusCode = $status
        if ($body -ne $null) { $ctx.Response.ContentLength64 = $body.Length; $ctx.Response.OutputStream.Write($body, 0, $body.Length) }
        $ctx.Response.Close()
    }
}).AddArgument($listener).AddArgument($state)
$handle = $server.BeginInvoke()

function Publish([string]$Tag) {
    $bin = [System.Text.Encoding]::UTF8.GetBytes("stand-in for nitpick $Tag")
    $name = "nitpick_${Tag}_windows_amd64.exe"
    $state.files["$Tag/$name"] = $bin
    $state.files["$Tag/$name.sigstore.json"] = [System.Text.Encoding]::UTF8.GetBytes('{"bundle":"x"}')
    $state.files["$Tag/checksums.txt"] = [System.Text.Encoding]::UTF8.GetBytes("$(Get-Sha $bin)  $name`n")
    return $bin
}

function Reset-Server { $state.files.Clear(); $state.log.Clear(); $state.latest = '' }

# Runs the script and returns its exit code, its output, and the install directory.
function Invoke-Install([hashtable]$Env = @{}, [string]$Path = $env:PATH) {
    $dir = Join-Path ([System.IO.Path]::GetTempPath()) ('nitpick-test-' + [guid]::NewGuid().ToString('N'))
    $vars = @{
        INSTALL_DIR = $dir; NITPICK_INSTALL_TEST = '1'; NITPICK_RELEASE_BASE = "$base/dl"
        NITPICK_LATEST_URL = "$base/latest"; VERSION = ''; NITPICK_TEST_ARCH = ''; PATH = $Path
    }
    foreach ($k in $Env.Keys) { $vars[$k] = $Env[$k] }
    $saved = @{}
    foreach ($k in $vars.Keys) { $saved[$k] = [Environment]::GetEnvironmentVariable($k); [Environment]::SetEnvironmentVariable($k, $vars[$k]) }
    try {
        $out = & $shell -NoProfile -NonInteractive -File $Script 2>&1 | Out-String
        $code = $LASTEXITCODE
    } finally {
        foreach ($k in $saved.Keys) { [Environment]::SetEnvironmentVariable($k, $saved[$k]) }
    }
    return [pscustomobject]@{ Code = $code; Out = $out; Dir = $dir; Exe = (Join-Path $dir 'nitpick.exe') }
}

function Remaining([string]$Dir) {
    if (-not (Test-Path $Dir)) { return @() }
    return @(Get-ChildItem -Force -Path $Dir | ForEach-Object { $_.Name })
}

try {
    # 1. the latest release installs
    Reset-Server; $bin = Publish $version; $state.latest = $version
    $r = Invoke-Install
    Check ($r.Code -eq 0) 'installs the latest release' $r.Out
    Check ((Test-Path $r.Exe) -and ((Get-Sha ([System.IO.File]::ReadAllBytes($r.Exe))) -eq (Get-Sha $bin))) 'the installed file is the release asset'
    Check ($r.Out -match "Installing nitpick $version \(windows/amd64\)") 'says what it is about to install'
    Check ((@(Remaining $r.Dir)).Count -eq 1) 'leaves only nitpick.exe in the directory' ((Remaining $r.Dir) -join ', ')

    # 2. a pinned version skips the lookup, with or without the v
    Reset-Server; [void](Publish 'v9.8.0'); [void](Publish $version); $state.latest = $version
    foreach ($pin in 'v9.8.0', '9.8.0') {
        $r = Invoke-Install @{ VERSION = $pin }
        Check ($r.Code -eq 0 -and $r.Out -match 'v9\.8\.0') "VERSION=$pin installs that version" $r.Out
    }
    Check (-not ($state.log -contains '/latest')) 'a pinned install never asks for the latest'

    # 3. a tampered asset is refused and nothing is installed
    Reset-Server; [void](Publish $version); $state.latest = $version
    $state.files["$version/$asset"] = [System.Text.Encoding]::UTF8.GetBytes('owned')
    $r = Invoke-Install
    Check ($r.Code -ne 0 -and $r.Out -match 'checksum mismatch') 'a tampered asset is refused' $r.Out
    Check ((@(Remaining $r.Dir)).Count -eq 0) 'a refused install leaves nothing behind' ((Remaining $r.Dir) -join ', ')

    # 4. a tampered checksums.txt
    Reset-Server; [void](Publish $version); $state.latest = $version
    $state.files["$version/checksums.txt"] = [System.Text.Encoding]::UTF8.GetBytes(("0" * 64) + "  $asset`n")
    $r = Invoke-Install
    Check ($r.Code -ne 0 -and $r.Out -match 'checksum mismatch') 'a tampered checksums.txt is refused' $r.Out
    Check ((@(Remaining $r.Dir)).Count -eq 0) 'nothing is installed after a refused checksums.txt'

    # 5. an asset with no checksum of its own: other files, similar names, wrong case
    Reset-Server; $bin = Publish $version; $state.latest = $version
    $good = Get-Sha $bin
    $state.files["$version/checksums.txt"] = [System.Text.Encoding]::UTF8.GetBytes("$good  $asset.sigstore.json`n$good  x$asset`n$good  $($asset.ToUpper())`n")
    $r = Invoke-Install
    Check ($r.Code -ne 0 -and $r.Out -match 'does not list') 'a checksum under a similar name does not count' $r.Out

    # 6. a refused install leaves an older binary as it was
    Reset-Server; [void](Publish $version); $state.latest = $version
    $state.files["$version/$asset"] = [System.Text.Encoding]::UTF8.GetBytes('tampered')
    $dir = Join-Path ([System.IO.Path]::GetTempPath()) ('nitpick-test-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $dir | Out-Null
    [System.IO.File]::WriteAllText((Join-Path $dir 'nitpick.exe'), 'the old binary')
    $r = Invoke-Install @{ INSTALL_DIR = $dir }
    Check ($r.Code -ne 0 -and ([System.IO.File]::ReadAllText((Join-Path $dir 'nitpick.exe')) -eq 'the old binary')) 'a refused install keeps the old binary exactly' $r.Out

    # 7. unsupported architectures are refused before any request
    foreach ($a in 'Arm64', 'X86', 'Arm') {
        Reset-Server; [void](Publish $version); $state.latest = $version
        $r = Invoke-Install @{ NITPICK_TEST_ARCH = $a }
        Check ($r.Code -ne 0 -and $r.Out -match 'unsupported architecture' -and $state.log.Count -eq 0 -and (@(Remaining $r.Dir)).Count -eq 0) "architecture $a is refused before any request" "$($r.Out) requests: $($state.log -join ',')"
    }

    # 8. versions that are not versions
    foreach ($bad in 'latest', '../../x', 'v1.2.3/../../x', 'v1.2.3;id', 'v1 2', 'vv') {
        Reset-Server; [void](Publish $version); $state.latest = $version
        $r = Invoke-Install @{ VERSION = $bad }
        Check ($r.Code -ne 0 -and $r.Out -match 'not a release version' -and $state.log.Count -eq 0) "VERSION='$bad' is refused before any request" $r.Out
    }

    # 9. a latest lookup that fails says how to pin a version
    Reset-Server
    $r = Invoke-Install
    Check ($r.Code -ne 0 -and $r.Out -match 'VERSION') 'a failed latest lookup explains how to pin a version' $r.Out

    # 10. the PATH hint appears only for a directory that is not on the PATH
    Reset-Server; [void](Publish $version); $state.latest = $version
    $r = Invoke-Install
    Check ($r.Out -match 'to your PATH') 'a directory off the PATH gets a hint'
    $dir = Join-Path ([System.IO.Path]::GetTempPath()) ('nitpick-test-' + [guid]::NewGuid().ToString('N'))
    $r = Invoke-Install @{ INSTALL_DIR = $dir } ($dir + [System.IO.Path]::PathSeparator + $env:PATH)
    Check ($r.Code -eq 0 -and $r.Out -notmatch 'to your PATH') 'a directory already on the PATH gets none' $r.Out

    # 11. cosign: present and failing stops the install; present and passing says so; absent says checksum only
    $fakeDir = Join-Path ([System.IO.Path]::GetTempPath()) ('nitpick-cosign-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $fakeDir | Out-Null
    function Set-Cosign([int]$ExitCode) {
        if ($isWin) {
            [System.IO.File]::WriteAllText((Join-Path $fakeDir 'cosign.cmd'), "@echo off`r`necho %* > `"$fakeDir\args.txt`"`r`nexit /b $ExitCode`r`n")
        } else {
            $p = Join-Path $fakeDir 'cosign'
            [System.IO.File]::WriteAllText($p, "#!/bin/sh`necho `"`$@`" > '$fakeDir/args.txt'`nexit $ExitCode`n")
            & chmod 755 $p
        }
    }
    $withFake = $fakeDir + [System.IO.Path]::PathSeparator + $env:PATH
    Reset-Server; [void](Publish $version); $state.latest = $version
    $r = Invoke-Install @{} $env:PATH
    Check ($r.Code -eq 0 -and $r.Out -match 'Verified the checksum only') 'without cosign the output says only the checksum was checked' $r.Out
    Set-Cosign 1
    $r = Invoke-Install @{} $withFake
    Check ($r.Code -ne 0 -and $r.Out -match 'signature verification failed' -and (@(Remaining $r.Dir)).Count -eq 0) 'a bad signature stops the install' $r.Out
    Set-Cosign 0
    $r = Invoke-Install @{} $withFake
    $cargs = if (Test-Path (Join-Path $fakeDir 'args.txt')) { Get-Content -Raw (Join-Path $fakeDir 'args.txt') } else { '' }
    Check ($r.Code -eq 0 -and $r.Out -match 'Sigstore signature' -and $r.Out -notmatch 'checksum only') 'a good signature is reported' $r.Out
    Check ($cargs -match 'verify-blob' -and $cargs -match '--bundle' -and $cargs -match 'nitpick-oss/nitpick' -and $cargs -match 'token\.actions\.githubusercontent\.com') 'cosign is called with the release identity' $cargs
    $state.files.Remove("$version/$asset.sigstore.json")
    $r = Invoke-Install @{} $withFake
    Check ($r.Code -ne 0 -and $r.Out -match 'signature bundle') 'a missing bundle stops the install when cosign is present' $r.Out

    # 12. a script cut off partway runs nothing
    $lines = Get-Content -Path $Script
    foreach ($keep in 3, [int]($lines.Count / 2), ($lines.Count - 2)) {
        Reset-Server; [void](Publish $version); $state.latest = $version
        $cut = Join-Path ([System.IO.Path]::GetTempPath()) ('cut-' + [guid]::NewGuid().ToString('N') + '.ps1')
        [System.IO.File]::WriteAllText($cut, (($lines | Select-Object -First $keep) -join "`n"))
        $saved = $Script; $Script = $cut
        $r = Invoke-Install
        $Script = $saved
        Check ($state.log.Count -eq 0 -and (@(Remaining $r.Dir)).Count -eq 0) "a script cut after $keep lines installs nothing" "$($r.Out) requests: $($state.log -join ',')"
    }
} finally {
    $listener.Stop(); $listener.Close()
    $server.Dispose()
}

if ($failures.Count -gt 0) {
    Write-Host "`n$($failures.Count) check(s) failed"
    exit 1
}
Write-Host "`nall checks passed"
