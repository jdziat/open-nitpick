# Install the latest nitpick release on Windows.
#
#   irm https://nitpick-oss.github.io/nitpick/install.ps1 | iex
#
# It downloads nitpick.exe, checks it against the release's checksums.txt,
# checks the Sigstore signature too when cosign is installed, and puts it in a
# directory you own. It does not change your PATH and does not need
# administrator rights.
#
# Environment:
#   VERSION      the release to install, such as v2.10.0. Default: the latest.
#   INSTALL_DIR  where to put nitpick.exe. Default: %LOCALAPPDATA%\Programs\nitpick
#
# Read by tests and by anyone mirroring releases:
#   NITPICK_RELEASE_BASE  where assets are served, as <base>/<tag>/<asset>
#   NITPICK_LATEST_URL    a URL that answers with JSON holding a "tag_name"
#   NITPICK_INSTALL_TEST  set by the tests only: skips the operating system
#                         check and does not run the file it installed
#   NITPICK_TEST_ARCH     with the line above, stands in for the architecture
#
# Everything is inside Install-Nitpick, which runs on the last line. A download
# cut off partway therefore runs nothing at all.

function Install-Nitpick {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'

    $repo = 'nitpick-oss/nitpick'
    $releaseBase = if ($env:NITPICK_RELEASE_BASE) { $env:NITPICK_RELEASE_BASE } else { "https://github.com/$repo/releases/download" }
    $latestUrl = if ($env:NITPICK_LATEST_URL) { $env:NITPICK_LATEST_URL } else { "https://api.github.com/repos/$repo/releases/latest" }
    $testing = [bool]$env:NITPICK_INSTALL_TEST

    # Windows PowerShell 5.1 can default to protocols GitHub no longer accepts,
    # which fails with "could not create SSL/TLS secure channel".
    if ($PSVersionTable.PSEdition -ne 'Core') {
        try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }
    }

    function Stop-Install([string]$Message) {
        throw "install.ps1: $Message"
    }

    # The platform, before any network request: an unsupported one must fail
    # without having downloaded anything.
    if (-not $testing -and $PSVersionTable.PSEdition -eq 'Core' -and -not $IsWindows) {
        Stop-Install 'this script is for Windows; on linux and darwin run install.sh'
    }
    try {
        $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    } catch {
        # An older .NET Framework has no RuntimeInformation.
        $raw = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
        $arch = switch ($raw) { 'AMD64' { 'X64' } 'ARM64' { 'Arm64' } default { "$raw" } }
    }
    if ($testing -and $env:NITPICK_TEST_ARCH) { $arch = $env:NITPICK_TEST_ARCH }
    if ($arch -ne 'X64') {
        Stop-Install "unsupported architecture ${arch}: Windows releases are built for amd64 only"
    }

    if ($env:INSTALL_DIR) {
        $dir = $env:INSTALL_DIR
    } elseif ($env:LOCALAPPDATA) {
        $dir = Join-Path $env:LOCALAPPDATA 'Programs\nitpick'
    } else {
        Stop-Install 'LOCALAPPDATA is not set; set INSTALL_DIR to where nitpick.exe should go'
    }

    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ('nitpick-install-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        $version = $env:VERSION
        if (-not $version) {
            try {
                $latest = Invoke-RestMethod -UseBasicParsing -Uri $latestUrl
            } catch {
                Stop-Install 'could not find the latest release (GitHub may be rate limiting you); set $env:VERSION = ''vX.Y.Z'' to skip the lookup'
            }
            $version = $latest.tag_name
            if (-not $version) { Stop-Install 'the latest release answer held no tag_name' }
        }
        if (-not $version.StartsWith('v')) { $version = "v$version" }
        # The version goes into a URL and a file name, so it is checked, not trusted.
        if ($version -notmatch '^v[0-9][A-Za-z0-9.+-]*$') { Stop-Install "$version is not a release version" }

        $asset = "nitpick_${version}_windows_amd64.exe"
        Write-Host "Installing nitpick $version (windows/amd64) to $dir"

        $file = Join-Path $tmp $asset
        $sums = Join-Path $tmp 'checksums.txt'
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "$releaseBase/$version/$asset" -OutFile $file
        } catch {
            Stop-Install "could not download $asset for $version"
        }
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "$releaseBase/$version/checksums.txt" -OutFile $sums
        } catch {
            Stop-Install "could not download checksums.txt for $version"
        }

        # Compared as text, case-sensitively: the listed name must be exactly this
        # asset, so a checksum for some other file cannot stand in for it.
        $expected = $null
        foreach ($line in Get-Content -Path $sums) {
            if ($line -match '^([0-9a-fA-F]{64})\s+\*?(\S.*?)\s*$' -and $Matches[2] -ceq $asset) {
                $expected = $Matches[1].ToLowerInvariant()
                break
            }
        }
        if (-not $expected) { Stop-Install "checksums.txt does not list $asset; nothing was installed" }
        $actual = (Get-FileHash -Algorithm SHA256 -Path $file).Hash.ToLowerInvariant()
        if ($expected -ne $actual) {
            Stop-Install "checksum mismatch for $asset (expected $expected, got $actual); nothing was installed"
        }

        if (Get-Command cosign -ErrorAction SilentlyContinue) {
            $bundle = "$file.sigstore.json"
            try {
                Invoke-WebRequest -UseBasicParsing -Uri "$releaseBase/$version/$asset.sigstore.json" -OutFile $bundle
            } catch {
                Stop-Install "could not download the signature bundle for $asset; nothing was installed"
            }
            & cosign verify-blob --bundle $bundle `
                --certificate-identity-regexp "^https://github.com/$repo/" `
                --certificate-oidc-issuer https://token.actions.githubusercontent.com `
                $file | Out-Null
            if ($LASTEXITCODE -ne 0) { Stop-Install "signature verification failed for $asset; nothing was installed" }
            Write-Host 'Verified the checksum and the Sigstore signature.'
        } else {
            Write-Host 'Verified the checksum only. The checksum is published beside the binary, so it'
            Write-Host 'catches a corrupt download, not a compromised release; install cosign to also'
            Write-Host 'check the signature.'
        }

        try {
            New-Item -ItemType Directory -Force -Path $dir | Out-Null
        } catch {
            Stop-Install "cannot create $dir; set `$env:INSTALL_DIR to a directory you own"
        }
        # Written beside the target and renamed into place, so an interrupted install
        # never leaves a half-written nitpick.exe where the old one was.
        $staged = Join-Path $dir ".nitpick.$PID.tmp"
        $target = Join-Path $dir 'nitpick.exe'
        try {
            Copy-Item -Path $file -Destination $staged -Force
            Move-Item -Path $staged -Destination $target -Force
        } catch {
            Remove-Item -Path $staged -Force -ErrorAction SilentlyContinue
            Stop-Install "could not install into $dir; if nitpick is running, close it and try again, or set `$env:INSTALL_DIR to a directory you own"
        }

        if (-not $testing) {
            $ran = & $target version 2>&1
            if ($LASTEXITCODE -ne 0) { Stop-Install "installed, but $target does not run: $ran" }
            Write-Host "Installed: $ran"
        } else {
            Write-Host "Installed: $target"
        }
        $onPath = @($env:PATH -split [System.IO.Path]::PathSeparator | ForEach-Object { $_.TrimEnd('\', '/') }) -contains $dir.TrimEnd('\', '/')
        if (-not $onPath) {
            Write-Host "Add $dir to your PATH, for example:"
            Write-Host "  [Environment]::SetEnvironmentVariable('Path', `"$dir;`" + [Environment]::GetEnvironmentVariable('Path', 'User'), 'User')"
        }
    } finally {
        Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

Install-Nitpick
