# Install the aggrega command for the current user on Windows:
#
#   irm https://aggrega.ai/install.ps1 | iex
#
# It downloads the default aggrega binary for Windows on 64-bit x86 from a
# release, checks it against the release's SHA256SUMS list, and installs it as
# %LOCALAPPDATA%\Programs\aggrega\aggrega.exe, replacing an earlier copy there
# in one step. It adds that folder to the user PATH when it is not there yet,
# and to the PATH of the window it runs in, then runs the installed binary to
# print its version. It runs in Windows PowerShell 5.1 and in PowerShell 7, and
# needs no administrator rights.
#
# Settings, read from the environment:
#
#   AGGREGA_VERSION        the release to install, such as 0.8.7 or v0.8.7.
#                          The latest release when unset.
#   AGGREGA_INSTALL_DIR    the folder to install into.
#                          %LOCALAPPDATA%\Programs\aggrega when unset.
#   AGGREGA_MIRROR         the address of a mirror of the Aggrega releases,
#                          laid out as GitHub lays them out:
#                          <mirror>/latest/download/<file> for the latest
#                          release and <mirror>/download/v<version>/<file> for
#                          a given one. The releases on GitHub,
#                          https://github.com/aggrega-ai/aggrega-cli/releases,
#                          when unset.
#
# Run through Invoke-Expression, a script that calls exit closes the window it
# runs in, so a refusal throws instead: the error is shown and the window stays
# open. Everything runs inside one script block, so none of its variables or
# preferences outlive it in the session that ran it; what it leaves in that
# session is the folder added to its PATH and, in Windows PowerShell 5.1, TLS
# 1.2 added to the protocols the session's web requests may use.
#
# The script is kept to ASCII: Windows PowerShell 5.1 reads a file without a
# byte-order mark in the system's legacy code page.

& {
    $ErrorActionPreference = 'Stop'
    # Windows PowerShell 5.1 redraws a progress bar for every block it
    # downloads, which slows a large download many times over.
    $ProgressPreference = 'SilentlyContinue'

    function Stop-Install([string]$Message) {
        throw "aggrega install: $Message"
    }

    function Write-Step([string]$Message) {
        Write-Host "aggrega install: $Message"
    }

    function Get-ReleaseFile([string]$Url, [string]$Path) {
        try {
            # -UseBasicParsing: Windows PowerShell 5.1 otherwise needs the
            # Internet Explorer engine to parse the response; PowerShell 7
            # accepts the switch and ignores it.
            Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Path
        } catch {
            return $_.Exception.Message
        }
        return $null
    }

    $base = $env:AGGREGA_MIRROR
    if (-not $base) {
        $base = 'https://github.com/aggrega-ai/aggrega-cli/releases'
    }
    $base = $base.TrimEnd('/')

    if ($PSVersionTable.PSEdition -eq 'Core' -and -not $IsWindows) {
        Stop-Install "install.ps1 installs on Windows; on Linux and macOS, run: curl -fsSL $base/latest/download/install.sh | sh"
    }
    $machine = $env:PROCESSOR_ARCHITEW6432
    if (-not $machine) {
        $machine = $env:PROCESSOR_ARCHITECTURE
    }
    if ($machine -ne 'AMD64') {
        Stop-Install "the processor is $machine, for which no release carries a Windows binary; Windows releases carry a binary for 64-bit x86 (AMD64)"
    }

    $requested = ''
    if ($env:AGGREGA_VERSION) {
        $requested = $env:AGGREGA_VERSION -replace '^v', ''
        if ($requested -notmatch '^[A-Za-z0-9._+-]+$') {
            Stop-Install "AGGREGA_VERSION is '$($env:AGGREGA_VERSION)', which is not a release version such as 0.8.7 or v0.8.7"
        }
    }

    if ($env:AGGREGA_INSTALL_DIR) {
        $dir = $env:AGGREGA_INSTALL_DIR
    } elseif ($env:LOCALAPPDATA) {
        $dir = Join-Path $env:LOCALAPPDATA 'Programs\aggrega'
    } else {
        Stop-Install 'LOCALAPPDATA is not set, so there is no default folder to install into; set AGGREGA_INSTALL_DIR to one'
    }
    $dir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($dir)
    if ($dir.Length -gt 3) {
        $dir = $dir.TrimEnd('\')
    }
    $dest = Join-Path $dir 'aggrega.exe'
    if (Test-Path -LiteralPath $dest -PathType Container) {
        Stop-Install "$dest is a folder, so aggrega cannot be installed under that name"
    }

    # GitHub accepts TLS 1.2 or later, which Windows PowerShell 5.1 does not
    # always offer by default. PowerShell 7 negotiates it on its own.
    if ($PSVersionTable.PSEdition -ne 'Core') {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    }

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('aggrega-install-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    $staged = $null
    try {
        if ($requested) {
            $sumsUrl = "$base/download/v$requested/SHA256SUMS"
        } else {
            $sumsUrl = "$base/latest/download/SHA256SUMS"
        }
        Write-Step "reading the checksum list $sumsUrl"
        $sumsPath = Join-Path $tmp 'SHA256SUMS'
        $failure = Get-ReleaseFile $sumsUrl $sumsPath
        if ($failure) {
            if ($requested) {
                Stop-Install "could not download ${sumsUrl}: $failure; check that $requested is a published release"
            }
            Stop-Install "could not download ${sumsUrl}: $failure"
        }

        # The default binary is aggrega-<version>-windows-amd64.exe, where the
        # version starts with a digit; that is what sets it apart from the
        # other variants a release carries, aggrega-debug-<version>-... and
        # aggrega-light-<version>-....
        $suffix = '-windows-amd64.exe'
        $found = @()
        foreach ($line in Get-Content -LiteralPath $sumsPath) {
            if ($line -notmatch '^([0-9A-Fa-f]{64}) [ *](.+?)\s*$') {
                continue
            }
            $lineSum = $Matches[1]
            $lineName = $Matches[2]
            if ($lineName -cnotmatch '^aggrega-[0-9][^\\/]*-windows-amd64\.exe$') {
                continue
            }
            if ($requested -and $lineName -cne "aggrega-$requested$suffix") {
                continue
            }
            $found += [pscustomobject]@{ Name = $lineName; Sum = $lineSum.ToLowerInvariant() }
        }
        if ($found.Count -eq 0) {
            Stop-Install "the checksum list at $sumsUrl names no aggrega binary for windows/amd64"
        }
        if ($found.Count -gt 1) {
            Stop-Install "the checksum list at $sumsUrl names $($found.Count) aggrega binaries for windows/amd64, where it should name one"
        }
        $asset = $found[0].Name
        $assetSum = $found[0].Sum
        $version = $asset.Substring('aggrega-'.Length, $asset.Length - 'aggrega-'.Length - $suffix.Length)

        $url = "$base/download/v$version/$asset"
        Write-Step "downloading $url"
        $download = Join-Path $tmp $asset
        $failure = Get-ReleaseFile $url $download
        if ($failure) {
            Stop-Install "could not download ${url}: $failure"
        }
        $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $download).Hash.ToLowerInvariant()
        if ($actual -ne $assetSum) {
            Remove-Item -LiteralPath $download -Force
            Stop-Install "the SHA-256 checksum of the downloaded $asset is $actual, but SHA256SUMS lists $assetSum; the download was deleted and nothing was installed"
        }
        Write-Step "$asset matches its SHA-256 checksum in SHA256SUMS"

        # The new binary is written beside the old one under a temporary name
        # and then swapped in by one file-system call, so the folder holds
        # either the earlier aggrega.exe or the new one at every moment.
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        $staged = Join-Path $dir ('.aggrega.install.' + [guid]::NewGuid().ToString('N') + '.exe')
        Copy-Item -LiteralPath $download -Destination $staged
        try {
            if (Test-Path -LiteralPath $dest) {
                # [NullString]::Value, not $null: PowerShell passes $null to a
                # string parameter of a .NET method as an empty string, which
                # File.Replace refuses as a backup file name.
                [IO.File]::Replace($staged, $dest, [NullString]::Value)
            } else {
                [IO.File]::Move($staged, $dest)
            }
        } catch {
            Stop-Install "could not put the new aggrega.exe in place at ${dest}: $($_.Exception.Message) If aggrega is running, started by an editor or an AI client, close that and run the installer again."
        }
        $staged = $null
        # A downloaded file can carry the mark Windows gives files from the
        # internet; removing it keeps the first run from being questioned.
        try {
            Unblock-File -LiteralPath $dest
        } catch {
        }
    } finally {
        if ($staged) {
            Remove-Item -LiteralPath $staged -Force -ErrorAction SilentlyContinue
        }
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }

    Write-Step "installed $dest"

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $onUserPath = $false
    if ($userPath) {
        foreach ($entry in $userPath.Split(';')) {
            if ($entry -and $entry.TrimEnd('\') -eq $dir) {
                $onUserPath = $true
            }
        }
    }
    if (-not $onUserPath) {
        if ($userPath) {
            $newPath = $userPath.TrimEnd(';') + ';' + $dir
        } else {
            $newPath = $dir
        }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        Write-Step "added $dir to your user PATH; a terminal opened from now on finds aggrega"
    }
    $onSessionPath = $false
    foreach ($entry in "$env:Path".Split(';')) {
        if ($entry -and $entry.TrimEnd('\') -eq $dir) {
            $onSessionPath = $true
        }
    }
    if (-not $onSessionPath) {
        $env:Path = "$env:Path".TrimEnd(';') + ';' + $dir
    }

    & $dest version
    if ($LASTEXITCODE -ne 0) {
        Stop-Install "installed $dest, but running '$dest version' failed with exit code $LASTEXITCODE"
    }

    $first = Get-Command aggrega -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($first -and $first.Path -ne $dest) {
        Write-Step "note: the aggrega that runs by name is $($first.Path), which comes before $dir on your PATH"
    }
}
