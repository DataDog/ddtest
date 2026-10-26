# Installs ddtest from its GitHub releases, as install.sh does for Unix shells:
#
#   irm https://github.com/DataDog/ddtest/releases/latest/download/install.ps1 | iex
#   & ([scriptblock]::Create((irm .../install.ps1))) -Version v1.2.3 -Dir C:\tools
#
# It downloads the release executable for this host and checks it against the
# release's SHA256SUMS; a checksum mismatch stops it, and so does a host
# without a release executable. An installed executable of the same version
# stays: a requested version needs no download, and latest only needs
# SHA256SUMS. It adds the directory to this session's PATH and, in GitHub
# Actions, to GITHUB_PATH for later steps. Errors are thrown rather than
# exiting, so that iex leaves the caller's session open.
#
# Settings, as parameters or environment variables:
#   -Version, DDTEST_INSTALL_VERSION   release tag, or latest (the default)
#   -Dir, DDTEST_INSTALL_DIR           destination directory ($HOME\.local\bin)
#   DDTEST_INSTALL_DOWNLOAD_URL        base URL of a mirror of the release's files
param(
    [string]$Version = $(if ($env:DDTEST_INSTALL_VERSION) { $env:DDTEST_INSTALL_VERSION } else { 'latest' }),
    [string]$Dir = $(if ($env:DDTEST_INSTALL_DIR) { $env:DDTEST_INSTALL_DIR } else { Join-Path (Join-Path $HOME '.local') 'bin' })
)

& {
    param([string]$Version, [string]$Dir)
    $ErrorActionPreference = 'Stop'
    # The progress bar slows Invoke-WebRequest down considerably.
    $ProgressPreference = 'SilentlyContinue'
    $repo = 'DataDog/ddtest'

    function Say([string]$Message) { [Console]::Error.WriteLine("ddtest install: $Message") }

    function Fetch([string]$Url, [string]$File) {
        try {
            if ($Url.StartsWith('file://')) {
                Copy-Item -LiteralPath ([Uri]$Url).LocalPath -Destination $File
            } else {
                Invoke-WebRequest -Uri $Url -OutFile $File -UseBasicParsing
            }
            return $true
        } catch {
            return $false
        }
    }

    function Sha256([string]$File) { (Get-FileHash -LiteralPath $File -Algorithm SHA256).Hash.ToLowerInvariant() }

    function Publish([string]$File) {
        # Beside the target, then moved over it: a copy that fails, as on a
        # full disk, keeps the installed ddtest.
        $partial = "$target.tmp.$([guid]::NewGuid().ToString('N'))"
        try {
            Copy-Item -LiteralPath $File -Destination $partial -Force
            if ($os -ne 'windows') {
                # A native command's failure does not stop the script.
                & chmod 0755 $partial
                if ($LASTEXITCODE -ne 0) { throw "ddtest install: cannot write $target" }
            }
            Move-Item -LiteralPath $partial -Destination $target -Force
        } finally {
            Remove-Item -LiteralPath $partial -Force -ErrorAction SilentlyContinue
        }
    }

    if ($PSVersionTable.PSEdition -eq 'Desktop') {
        # Windows PowerShell 5.1 can default to protocols that GitHub refuses.
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
        $os = 'windows'
    } elseif ($IsWindows) {
        $os = 'windows'
    } elseif ($IsMacOS) {
        $os = 'darwin'
    } elseif ($IsLinux) {
        $os = 'linux'
    } else {
        $os = ''
    }
    try {
        $machine = [string][System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    } catch {
        $machine = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    }
    $arch = switch ($machine) {
        { $_ -in 'X64', 'AMD64' } { 'amd64' }
        'Arm64' { 'arm64' }
        default { '' }
    }
    $exe = if ($os -eq 'windows') { '.exe' } else { '' }
    $base = if ($Version -eq 'latest') {
        "https://github.com/$repo/releases/latest/download"
    } else {
        "https://github.com/$repo/releases/download/$Version"
    }
    if ($env:DDTEST_INSTALL_DOWNLOAD_URL) { $base = $env:DDTEST_INSTALL_DOWNLOAD_URL }
    # Messages show the URL without the user information or query that a
    # mirror's URL can carry.
    $shown = $base -replace '^([A-Za-z][A-Za-z0-9+.-]*://)[^/@]*@', '$1' -replace '[?#].*$', ''

    $Dir = (New-Item -ItemType Directory -Force -Path $Dir).FullName
    $target = Join-Path $Dir "ddtest$exe"
    $current = ''
    if (Test-Path -LiteralPath $target -PathType Leaf) {
        # ddtest --version prints the version alone.
        try { $current = ((& $target --version 2> $null) -split ' ')[-1] } catch { $current = '' }
    }
    $temp = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
    New-Item -ItemType Directory -Path $temp | Out-Null
    try {
        $installed = ''
        # On a persistent runner, keep an installed executable of the same
        # version: a requested version needs no download, and latest only
        # needs SHA256SUMS.
        # Versions compare case-sensitively, as SemVer prereleases do.
        if ($Version -ne 'latest' -and $current -ceq $Version) {
            $installed = 'already installed'
        } elseif ($os -and $arch) {
            $asset = "ddtest-$os-$arch$exe"
            $want = ''
            if (-not (Fetch "$base/SHA256SUMS" (Join-Path $temp 'SHA256SUMS'))) {
                Say "no SHA256SUMS at $shown"
            } else {
                foreach ($line in Get-Content -LiteralPath (Join-Path $temp 'SHA256SUMS')) {
                    $fields = $line -split '\s+'
                    if ($fields.Count -ge 2 -and ($fields[1] -eq $asset -or $fields[1] -eq "*$asset")) { $want = $fields[0].ToLowerInvariant() }
                }
            }
            if (-not $want) {
                Say "no release executable $asset at $shown"
            } elseif ($current -and (Sha256 $target) -eq $want) {
                $installed = 'already installed'
            } elseif (-not (Fetch "$base/$asset" (Join-Path $temp $asset))) {
                Say "no release executable $asset at $shown"
            } elseif ((Sha256 (Join-Path $temp $asset)) -ne $want) {
                throw "ddtest install: $asset does not match SHA256SUMS"
            } else {
                Publish (Join-Path $temp $asset)
                $installed = "release $Version"
            }
        } else {
            Say "no release executable for $os $machine"
        }
        if (-not $installed) {
            Say "download ddtest from https://github.com/$repo/releases, or build it from a checkout with make install"
            throw 'ddtest install: no release executable installed'
        }
        $reported = & $target --version
        if ($LASTEXITCODE -ne 0) { throw "ddtest install: $target does not run" }
        Say "ddtest $reported in $target ($installed)"
    } finally {
        Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
    }
    $separator = [IO.Path]::PathSeparator
    $entries = $env:PATH -split [regex]::Escape($separator)
    # Windows paths ignore case; Unix paths do not.
    $listed = if ($os -eq 'windows') { $entries -contains $Dir } else { $entries -ccontains $Dir }
    if (-not $listed) {
        $env:PATH = "$Dir$separator$env:PATH"
    }
    # A step's own PATH does not reach the next steps: GitHub Actions adds what
    # GITHUB_PATH lists, even when this step's PATH already has the directory.
    if ($env:GITHUB_PATH) {
        # GitHub reads environment files as UTF-8; Windows PowerShell 5.1
        # would otherwise write the system's code page.
        Add-Content -LiteralPath $env:GITHUB_PATH -Value $Dir -Encoding UTF8
        Say "added $Dir to PATH for this session and the next steps"
    } elseif (-not $listed) {
        Say "added $Dir to PATH for this session; add it to your profile to keep it"
    }
} $Version $Dir
