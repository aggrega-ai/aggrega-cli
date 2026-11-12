# Install the aggrega command for the current user on Windows:
#
#   irm https://aggrega.ai/install.ps1 | iex
#
# It downloads the default aggrega binary for Windows on 64-bit x86 from a
# release, checks it against the release's SHA256SUMS list, and installs it as
# %LOCALAPPDATA%\Programs\aggrega\aggrega.exe, replacing an earlier copy there
# in one step. It adds that folder to the user PATH when it is not there yet,
# and to the PATH of the window it runs in, then checks, printing nothing, that
# the installed binary runs. What it prints, on success:
#
#   Installing aggrega <version> (windows-amd64)
#     Downloaded and checked against its SHA-256 checksum
#     Installed <folder>\aggrega.exe
#
# then, when it added the folder to the user PATH, a line saying so; then the
# installed binary's own version, printed by running it by its full path; and
# last, under "Next:", one line each for running the AI quickstart, adding the
# Aggrega skill to your AI assistants, and the page that shows how to set up
# shell completion and connect an AI client to the Aggrega MCP server. It runs
# in Windows PowerShell 5.1 and in PowerShell 7, and needs no administrator
# rights.
#
# A refusal ends in an error, and a failed step in a warning, reading:
#
#   aggrega install: error: <what went wrong> - <what to do>
#
# Options. These are switches of the script itself; Invoke-Expression passes
# none, so they apply where the script is run with arguments:
#
#   -Verbose               also print the address of the checksum list, the
#                          name of the release file and the address it is
#                          downloaded from.
#   -Completion            also set up shell completion, for you alone, for the
#                          PowerShell the script runs in, using the installed
#                          aggrega's own `aggrega completion powershell`
#                          script, and say which file it wrote or changed: it
#                          writes the script as
#                          %LOCALAPPDATA%\aggrega\completion.ps1 and adds the
#                          lines that load it to your profile for every host of
#                          that PowerShell, $PROFILE.CurrentUserAllHosts.
#                          Windows PowerShell and PowerShell 7 keep separate
#                          profiles, so run it from each one you use. A profile
#                          runs only where the execution policy a new window
#                          takes lets it (RemoteSigned, Unrestricted or
#                          Bypass); under another policy it writes nothing,
#                          says which policy it found, and the step fails. It
#                          never changes the policy. Run again, it rewrites the
#                          script only when it changed and adds the lines to
#                          the profile only once.
#   -Skills                also install the Aggrega skill, in every project of
#                          yours, for the AI assistants on this machine, by
#                          running the installed aggrega's own
#                          `aggrega skills install --global`. It prints each
#                          assistant it found and what found it. In a window
#                          someone is at it then asks before writing: press
#                          Enter to install for those, or type the identifiers
#                          of others to add them. With nobody to ask it
#                          installs for those it found. It prints each skill
#                          folder it wrote, and the line about the skill is
#                          left out of "Next:".
#   -Skills <list>         install the skill for the assistants the list names
#                          instead, such as -Skills claude-code,cursor, without
#                          looking for others; `aggrega skills list` prints
#                          the identifiers. Run again, either form replaces the
#                          copies an earlier run wrote, which is how a newer
#                          aggrega brings the skill up to its own version.
#
# Exit status. The script never closes the window it runs in, so it does not
# exit with a number: it leaves the number in $LASTEXITCODE, and every outcome
# but 0 ends in an error, which a run through powershell -Command or
# pwsh -Command reports as exit status 1.
#
#   0  aggrega is installed, and every step an option asked for succeeded
#   1  nothing was installed, or the installed aggrega did not run
#   2  an option the script does not take, or a list naming no assistant;
#      nothing was done
#   3  aggrega is installed and runs, but setting up shell completion failed
#   4  aggrega is installed and runs, but installing the skill failed
#   5  aggrega is installed and runs, but both of those failed
#
# A failed step says why in its warning, written when it fails.
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
# session is the folder added to its PATH, $LASTEXITCODE as above, the
# $Verbose, $Completion and $Skills switches its options declare, which
# Invoke-Expression defines in the session it runs in, and, in Windows
# PowerShell 5.1, TLS 1.2 added to the protocols the session's web requests may
# use.
#
# The script is kept to ASCII: Windows PowerShell 5.1 reads a file without a
# byte-order mark in the system's legacy code page.

# The script declares no [CmdletBinding()], so -Verbose is a switch of its own
# rather than PowerShell's common parameter, and PowerShell hands it every
# argument its three switches do not take in $args rather than refusing it:
# the assistants a list after -Skills names, and anything else, which the
# script refuses itself, naming it.
param([switch]$Verbose, [switch]$Completion, [switch]$Skills)

& {
    param([bool]$WantVerbose, [bool]$WantCompletion, [bool]$WantSkills, [object[]]$Rest)

    $ErrorActionPreference = 'Stop'
    # Windows PowerShell 5.1 redraws a progress bar for every block it
    # downloads, which slows a large download many times over.
    $ProgressPreference = 'SilentlyContinue'

    # Stop-Install ends the install with an error saying what went wrong and
    # what to do.
    function Stop-Install([string]$Message, [string]$Todo, [int]$Status = 1) {
        $global:LASTEXITCODE = $Status
        throw "aggrega install: error: $Message - $Todo"
    }

    # Write-Item prints one line of what the install did, indented under its
    # first line.
    function Write-Item([string]$Message) {
        Write-Host "  $Message"
    }

    # The assistants a list after -Skills names. A value of -Skills a,b reaches
    # the script as one argument holding both, and a quoted 'a,b' as one string,
    # so every argument is flattened and split on commas and spaces.
    $skillsList = @()
    $listGiven = $false
    $options = 'the options are -Verbose, -Completion and -Skills [<list>]'
    foreach ($argument in $Rest) {
        foreach ($part in @($argument)) {
            $listGiven = $true
            $text = "$part"
            if ($text.StartsWith('-')) {
                Stop-Install "'$text' is not an option install.ps1 takes" $options 2
            }
            if (-not $WantSkills) {
                Stop-Install "'$text' is not an option install.ps1 takes" $options 2
            }
            foreach ($id in ($text -split '[,\s]+')) {
                if (-not $id) {
                    continue
                }
                if ($id -cnotmatch '^[a-z0-9-]+$') {
                    Stop-Install "-Skills names '$id', which is not an assistant identifier such as claude-code" $options 2
                }
                $skillsList += $id
            }
        }
    }
    if ($listGiven -and $skillsList.Count -eq 0) {
        Stop-Install '-Skills names no assistant' $options 2
    }

    if ($PSVersionTable.PSEdition -eq 'Core') {
        $shellName = 'PowerShell'
    } else {
        $shellName = 'Windows PowerShell'
    }

    # Get-NewWindowPolicy returns the execution policy a PowerShell window
    # opened from now on takes, which decides whether it runs a profile. The
    # policy set for this process alone is left out, since it ends with the
    # process; where it is set, the scopes that outlive it are read in the
    # order PowerShell reads them, falling back to each edition's default.
    function Get-NewWindowPolicy {
        if ([string](Get-ExecutionPolicy -Scope Process) -eq 'Undefined') {
            return [string](Get-ExecutionPolicy)
        }
        foreach ($scope in 'MachinePolicy', 'UserPolicy', 'CurrentUser', 'LocalMachine') {
            $policy = [string](Get-ExecutionPolicy -Scope $scope)
            if ($policy -ne 'Undefined') {
                return $policy
            }
        }
        if ($PSVersionTable.PSEdition -eq 'Core') {
            return 'RemoteSigned'
        }
        return 'Restricted'
    }

    # Get-ProfileEncoding returns the encoding to add lines to a profile in:
    # UTF-16 where the file starts with that encoding's byte-order mark, as one
    # Windows PowerShell 5.1 wrote with > does, and UTF-8 otherwise.
    function Get-ProfileEncoding([string]$Path) {
        if (Test-Path -LiteralPath $Path -PathType Leaf) {
            $head = New-Object byte[] 2
            $stream = [IO.File]::OpenRead($Path)
            try {
                $count = $stream.Read($head, 0, 2)
            } finally {
                $stream.Dispose()
            }
            if ($count -eq 2 -and $head[0] -eq 0xFF -and $head[1] -eq 0xFE) {
                return [Text.Encoding]::Unicode
            }
            if ($count -eq 2 -and $head[0] -eq 0xFE -and $head[1] -eq 0xFF) {
                return [Text.Encoding]::BigEndianUnicode
            }
        }
        return (New-Object Text.UTF8Encoding $false)
    }

    # Set-Completion sets up completion for this PowerShell and returns $null,
    # or returns why it did not.
    function Set-Completion {
        try {
            $policy = Get-NewWindowPolicy
            if (@('RemoteSigned', 'Unrestricted', 'Bypass') -notcontains $policy) {
                return "a new $shellName window takes the execution policy $policy, under which it runs no profile, so nothing was written; Set-ExecutionPolicy -Scope CurrentUser RemoteSigned lets it run yours"
            }
            if (-not $env:LOCALAPPDATA) {
                return 'LOCALAPPDATA is not set, so there is no folder of yours to write it into'
            }
            $profiles = Get-Variable -Name PROFILE -ValueOnly -ErrorAction SilentlyContinue
            $profilePath = $null
            if ($profiles) {
                $profilePath = $profiles.CurrentUserAllHosts
            }
            if (-not $profilePath) {
                return "this $shellName names no profile file of yours"
            }

            $completionPath = Join-Path $env:LOCALAPPDATA 'aggrega\completion.ps1'
            $text = & $dest completion powershell | Out-String
            if ($LASTEXITCODE -ne 0) {
                return "running '$dest completion powershell' failed with exit code $LASTEXITCODE"
            }
            $current = $null
            if (Test-Path -LiteralPath $completionPath -PathType Leaf) {
                $current = [IO.File]::ReadAllText($completionPath)
            }
            if ($current -ceq $text) {
                Write-Item "$completionPath already holds aggrega's PowerShell completion; left it unchanged"
            } else {
                $folder = Split-Path -Parent $completionPath
                New-Item -ItemType Directory -Force -Path $folder | Out-Null
                $stagedScript = Join-Path $folder ('.aggrega.completion.' + [guid]::NewGuid().ToString('N') + '.ps1')
                try {
                    [IO.File]::WriteAllText($stagedScript, $text)
                    if (Test-Path -LiteralPath $completionPath) {
                        [IO.File]::Replace($stagedScript, $completionPath, [NullString]::Value)
                    } else {
                        [IO.File]::Move($stagedScript, $completionPath)
                    }
                } finally {
                    Remove-Item -LiteralPath $stagedScript -Force -ErrorAction SilentlyContinue
                }
                Write-Item "Wrote aggrega's PowerShell completion to $completionPath"
            }

            $marker = '# Shell completion for aggrega, added by the aggrega installer.'
            $content = ''
            if (Test-Path -LiteralPath $profilePath -PathType Leaf) {
                $content = [IO.File]::ReadAllText($profilePath)
            }
            if (($content -split "`r?`n") -ccontains $marker) {
                Write-Item "$profilePath already loads it; left it unchanged"
                return $null
            }
            $quoted = $completionPath.Replace("'", "''")
            $lines = $marker + "`r`n" + "if (Test-Path -LiteralPath '$quoted') { . '$quoted' }" + "`r`n"
            # A file whose last line has no line break gets one first, so the
            # lines start on their own.
            if ($content.Length -gt 0 -and -not $content.EndsWith("`n")) {
                $lines = "`r`n" + $lines
            }
            $encoding = Get-ProfileEncoding $profilePath
            New-Item -ItemType Directory -Force -Path (Split-Path -Parent $profilePath) | Out-Null
            [IO.File]::AppendAllText($profilePath, $lines, $encoding)
            Write-Item "Added the lines that load it to $profilePath; $shellName reads them from a new window on"
            return $null
        } catch {
            return "$($_.Exception.Message)"
        }
    }

    # Install-Skills runs the installed aggrega's own skill installation, for the
    # assistants the list after -Skills names or for the ones it finds on this
    # machine, and returns $null, or returns why it did not succeed. Finding
    # them, asking whoever is at the window and writing are all the command's
    # own; its output is sent to the host rather than returned, so what it
    # prints is shown as it happens.
    function Install-Skills {
        $skillArgs = @('skills', 'install', '--global')
        foreach ($id in $skillsList) {
            $skillArgs += @('--agent', $id)
        }
        & $dest @skillArgs | Out-Host
        if ($LASTEXITCODE -ne 0) {
            return "'aggrega skills install' did not succeed, as it says above"
        }
        return $null
    }

    # Write-NextSteps prints one line for each thing to do next: the AI
    # quickstart, the skill when -Skills did not install it, and the page on
    # shell completion and the MCP server.
    function Write-NextSteps([bool]$SkillsDone) {
        Write-Host ''
        Write-Host 'Next:'
        Write-Host ('  {0,-30}{1}' -f 'Run the AI quickstart:', 'irm https://aggrega.ai/quickstart-ai.ps1 | iex')
        if (-not $SkillsDone) {
            Write-Host ('  {0,-30}{1}' -f 'Add the skill to your AI:', 'aggrega skills install --global')
        }
        Write-Host ('  {0,-30}{1}' -f 'Shell completion and MCP:', 'https://aggrega.ai/docs/install/')
    }

    # Assert-Runs runs the installed aggrega's version command, showing what it
    # prints when $Show is set and discarding it otherwise, and stops the
    # install when it does not run. Inside it, a line the binary writes to its
    # error stream is not an error of the script's own: under the Stop
    # preference, Windows PowerShell 5.1 would end the script on the first one
    # it redirects.
    function Assert-Runs([bool]$Show) {
        $ErrorActionPreference = 'Continue'
        $why = $null
        try {
            if ($Show) {
                & $dest version | Out-Host
            } else {
                & $dest version 2>&1 | Out-Null
            }
            if ($LASTEXITCODE -ne 0) {
                $why = " with exit code $LASTEXITCODE"
            }
        } catch {
            $why = ": $($_.Exception.Message)"
        }
        if ($why) {
            Stop-Install "installed $dest, but running '$dest version' failed$why" "run '$dest version' to see why"
        }
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
        Stop-Install 'install.ps1 installs on Windows' "on Linux and macOS, run: curl -fsSL $base/latest/download/install.sh | sh"
    }
    $machine = $env:PROCESSOR_ARCHITEW6432
    if (-not $machine) {
        $machine = $env:PROCESSOR_ARCHITECTURE
    }
    if ($machine -ne 'AMD64') {
        Stop-Install "the processor is $machine, for which no release carries a Windows binary" 'install on 64-bit x86 (AMD64), which Windows releases carry a binary for'
    }

    $requested = ''
    if ($env:AGGREGA_VERSION) {
        $requested = $env:AGGREGA_VERSION -replace '^v', ''
        if ($requested -notmatch '^[A-Za-z0-9._+-]+$') {
            Stop-Install "AGGREGA_VERSION is '$($env:AGGREGA_VERSION)', which is not a release version such as 0.8.7 or v0.8.7" 'set it to a release version, or remove it to install the latest release'
        }
    }

    if ($env:AGGREGA_INSTALL_DIR) {
        $dir = $env:AGGREGA_INSTALL_DIR
    } elseif ($env:LOCALAPPDATA) {
        $dir = Join-Path $env:LOCALAPPDATA 'Programs\aggrega'
    } else {
        Stop-Install 'LOCALAPPDATA is not set, so there is no default folder to install into' 'set AGGREGA_INSTALL_DIR to the folder to install into'
    }
    $dir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($dir)
    if ($dir.Length -gt 3) {
        $dir = $dir.TrimEnd('\')
    }
    $dest = Join-Path $dir 'aggrega.exe'
    if (Test-Path -LiteralPath $dest -PathType Container) {
        Stop-Install "$dest is a folder, so aggrega cannot be installed under that name" 'move that folder away, or set AGGREGA_INSTALL_DIR to another folder'
    }

    # GitHub accepts TLS 1.2 or later, which Windows PowerShell 5.1 does not
    # always offer by default. PowerShell 7 negotiates it on its own.
    if ($PSVersionTable.PSEdition -ne 'Core') {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    }

    $network = 'check your network connection and run the installer again'
    $otherRelease = 'set AGGREGA_VERSION to another release and run the installer again'
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('aggrega-install-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    $staged = $null
    try {
        if ($requested) {
            $sumsUrl = "$base/download/v$requested/SHA256SUMS"
        } else {
            $sumsUrl = "$base/latest/download/SHA256SUMS"
        }
        $sumsPath = Join-Path $tmp 'SHA256SUMS'
        $failure = Get-ReleaseFile $sumsUrl $sumsPath
        if ($failure) {
            if ($requested) {
                Stop-Install "could not download ${sumsUrl}: $failure" "check that $requested is a published release"
            }
            Stop-Install "could not download ${sumsUrl}: $failure" $network
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
            Stop-Install "the checksum list at $sumsUrl names no aggrega binary for windows/amd64" $otherRelease
        }
        if ($found.Count -gt 1) {
            Stop-Install "the checksum list at $sumsUrl names $($found.Count) aggrega binaries for windows/amd64, where it should name one" $otherRelease
        }
        $asset = $found[0].Name
        $assetSum = $found[0].Sum
        $version = $asset.Substring('aggrega-'.Length, $asset.Length - 'aggrega-'.Length - $suffix.Length)

        $url = "$base/download/v$version/$asset"
        Write-Host "Installing aggrega $version (windows-amd64)"
        if ($WantVerbose) {
            Write-Item "Checksum list:  $sumsUrl"
            Write-Item "Release file:   $asset"
            Write-Item "Downloaded from $url"
        }
        $download = Join-Path $tmp $asset
        $failure = Get-ReleaseFile $url $download
        if ($failure) {
            Stop-Install "could not download ${url}: $failure" $network
        }
        $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $download).Hash.ToLowerInvariant()
        if ($actual -ne $assetSum) {
            Remove-Item -LiteralPath $download -Force
            Stop-Install "the SHA-256 checksum of the downloaded $asset is $actual, but SHA256SUMS lists $assetSum; the download was deleted and nothing was installed" 'run the installer again'
        }
        Write-Item 'Downloaded and checked against its SHA-256 checksum'

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
            Stop-Install "could not put the new aggrega.exe in place at ${dest}: $($_.Exception.Message)" 'if aggrega is running, started by an editor or an AI client, close that and run the installer again'
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

    Write-Item "Installed $dest"

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $onUserPath = $false
    if ($userPath) {
        foreach ($entry in $userPath.Split(';')) {
            if ($entry -and $entry.TrimEnd('\') -eq $dir) {
                $onUserPath = $true
            }
        }
    }
    $pathAdded = $false
    if (-not $onUserPath) {
        if ($userPath) {
            $newPath = $userPath.TrimEnd(';') + ';' + $dir
        } else {
            $newPath = $dir
        }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        $pathAdded = $true
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

    # Checked before completion or the skill is set up, which run it too; the
    # version it prints is shown below.
    Assert-Runs $false

    $status = 0
    if ($WantCompletion) {
        $failure = Set-Completion
        if ($failure) {
            Write-Warning "aggrega install: error: could not set up shell completion: $failure - aggrega is installed and runs; https://aggrega.ai/docs/install/ shows how to set completion up later"
            $status = 3
        }
    }

    if ($pathAdded) {
        Write-Host ''
        Write-Host "Added $dir to your user PATH. Open a new terminal to run aggrega by name."
    }
    $first = Get-Command aggrega -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($first -and $first.Path -ne $dest) {
        Write-Host ''
        Write-Host "Note: the aggrega that runs by name is $($first.Path), which comes before $dir on your PATH."
    }

    $skillsDone = $false
    if ($WantSkills) {
        Write-Host ''
        $failure = Install-Skills
        if ($failure) {
            Write-Warning "aggrega install: error: could not install the Aggrega skill: $failure - aggrega is installed and runs; run 'aggrega skills install --global' again once that is fixed"
            if ($status -eq 3) {
                $status = 5
            } else {
                $status = 4
            }
        } else {
            $skillsDone = $true
        }
    }

    Write-Host ''
    Assert-Runs $true
    Write-NextSteps $skillsDone
    $global:LASTEXITCODE = $status
    if ($status -ne 0) {
        $failed = @{ 3 = 'setting up shell completion'; 4 = 'installing the Aggrega skill'; 5 = 'setting up shell completion and installing the Aggrega skill' }[$status]
        throw "aggrega install: error: aggrega is installed at $dest and runs, but $failed failed - the warning above says why and what to do"
    }
} ([bool]$Verbose) ([bool]$Completion) ([bool]$Skills) $args
