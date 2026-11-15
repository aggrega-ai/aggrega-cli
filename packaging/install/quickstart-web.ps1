# Run one step of quickstart-web, the example in the Aggrega documentation that
# builds a web app with PostgreSQL up in four steps, from PowerShell. For
# step 1:
#
#   $env:QUICKSTART_DB_PASSWORD = 'choose-a-password'
#   irm https://aggrega.ai/quickstart-web.ps1 | iex
#
# Another step, 2, 3 or 4, is set in QUICKSTART_STEP first:
#
#   $env:QUICKSTART_STEP=2; irm https://aggrega.ai/quickstart-web.ps1 | iex
#
# It first says what it will do and asks to continue, reading the answer from
# the console; where nobody can answer - its input redirected, or a host that
# reads none - it refuses. It checks that aggrega can be found, on PATH or in
# the folder the Aggrega installer puts it in, and, for a step that runs, that Docker answers and has Compose, and that
# QUICKSTART_DB_PASSWORD is set. It writes the example into .\quickstart-web
# with `aggrega draft --example quickstart-web`, or uses that folder as it
# is when it already exists, goes into the step's folder, quickstart-web\<step>,
# and runs the commands the example's page gives for that step, then returns
# to the folder it started in. Steps 1 and 2 build and start the example with
# Docker Compose; steps 3 and 4 build its Terraform files for AWS, and plan and
# apply nothing. It ends by saying what to open next; for steps 1 and 2 the
# last line is the app's address. It runs in Windows PowerShell 5.1 and in
# PowerShell 7.
#
# Settings, read from the environment:
#
#   AGGREGA_INSTALL_DIR     the folder to look for aggrega.exe in when PATH
#                           does not have it, as the installer reads it;
#                           %LOCALAPPDATA%\Programs\aggrega when unset.
#   QUICKSTART_DB_PASSWORD  for steps 1 and 2, the database's password, which
#                           Docker Compose reads; required, and yours to
#                           choose. The database keeps the password it was
#                           first started with, so step 2 takes the same one.
#                           The script never prints it.
#   QUICKSTART_STEP         the step to run, 1, 2, 3 or 4; 1 when unset. A step
#                           given to the script as an argument is run instead.
#
# Exit status. The script never closes the window it runs in, so it does not
# exit with a number: it leaves the number in $LASTEXITCODE, and every outcome
# but 0 ends in an error, which a run through powershell -Command or
# pwsh -Command reports as exit status 1.
#
#   0  the step was built, and started where it runs
#   1  a check or a command failed, or the answer was not yes
#   2  the step is not 1, 2, 3 or 4, or an argument is not a step
#
# How the steps are written. Each step's commands are in the function
# step_<N>, one command per line, each line the word `run` followed by the
# command exactly as the example's page writes it in that step's block, less
# the block's `cd` lines, which the script replaces by its own. A variable a
# step needs set is a line of its own, `require_env <NAME> <value>`, which
# stands for the page's `export <NAME>=<value>` line where the page has one;
# the value is the page's example, printed when the variable is not set. Step
# 2's block has no such line, as the page asks for the password set at step 1
# to be still set. The commands run from these lines and from nowhere else:
# each step function runs twice, first to check what require_env asks for,
# then to run the commands.
#
# Everything runs inside one script block, so none of its variables or
# preferences outlive it in the session that ran it. The script is kept to
# ASCII: Windows PowerShell 5.1 reads a file without a byte-order mark in the
# system's legacy code page.

# The script declares no [CmdletBinding()], so PowerShell hands it every
# argument its one switch does not take in $args rather than refusing it: the
# step, and anything else, which the script refuses itself, naming it.
param([switch]$Yes)

& {
    param([bool]$GoAhead, [object[]]$Rest)

    $ErrorActionPreference = 'Stop'
    $example = 'quickstart-web'

    function step_1 {
        run aggrega build --release release.yaml --auto-pull
        require_env QUICKSTART_DB_PASSWORD choose-a-password
        run docker compose -p quickstart-web -f dist/compose/docker-compose.yaml up -d --wait
    }

    function step_2 {
        require_env QUICKSTART_DB_PASSWORD choose-a-password
        run aggrega build --release release.yaml --auto-pull
        run docker compose -p quickstart-web -f dist/compose/docker-compose.yaml up -d --wait
    }

    function step_3 {
        run aggrega build --release release.yaml --auto-pull
    }

    function step_4 {
        run aggrega build --release release.yaml --auto-pull
    }

    # step_runs returns whether a step starts the example with Docker Compose,
    # rather than only building it.
    function step_runs([string]$Step) {
        return (@('1', '2') -contains $Step)
    }

    # step_notes prints what the reader should know before the step runs;
    # this example has nothing to add.
    function step_notes([string]$Step) {
    }

    # step_end prints what to open next.
    function step_end([string]$Step) {
        if (@('3', '4') -contains $Step) {
            Write-Say "the Terraform files are in $example\$Step\dist\terraform\; the script plans and applies nothing, and $example\README.md says which placeholders to replace first."
            return
        }
        Write-Say "step $Step is up."
        Write-Say 'open http://127.0.0.1:8080/ in your browser to see the app.'
    }

    # The machinery that runs a step from the functions above.

    function Write-Say([string]$Message) {
        Write-Host "${example}: $Message"
    }

    function Stop-Quickstart([string]$Message, [int]$Status = 1) {
        $global:LASTEXITCODE = $Status
        throw "${example}: $Message"
    }

    # Test-Drafts reports whether an aggrega reporting $Version has the draft
    # command, the rule the editor follows: an unreleased build, "(devel)", or a
    # version that cannot be read, has it; otherwise the first
    # MAJOR[.MINOR[.PATCH]] number run in the string decides, a missing part
    # counting as 0 and a pre-release or build suffix ignored, and 0.9.2 and
    # later have it.
    function Test-Drafts([string]$Version) {
        if (-not $Version -or $Version -eq '(devel)') { return $true }
        if ($Version -notmatch '(?<!\d)(\d+)(?:\.(\d+))?(?:\.(\d+))?') { return $true }
        $major = [int]$Matches[1]
        $minor = if ($Matches[2]) { [int]$Matches[2] } else { 0 }
        $patch = if ($Matches[3]) { [int]$Matches[3] } else { 0 }
        if ($major -gt 0) { return $true }
        if ($minor -gt 9) { return $true }
        return ($minor -eq 9 -and $patch -ge 2)
    }

    # Get-DraftingVerb returns the command that writes an example for the
    # aggrega found: draft where it has it, else scaffold, which every aggrega
    # has had since the first release.
    function Get-DraftingVerb([string]$Aggrega) {
        $reported = & {
            $ErrorActionPreference = 'Continue'
            try {
                $json = (& $Aggrega version -o json 2>$null | Out-String) | ConvertFrom-Json
                [string]$json.version
            } catch {
                ''
            }
        }
        if (Test-Drafts $reported) { return 'draft' }
        return 'scaffold'
    }

    function Stop-Usage([string]$Message) {
        Write-Host "usage: irm https://aggrega.ai/$example.ps1 | iex"
        Write-Host "       another step: `$env:QUICKSTART_STEP=2; irm https://aggrega.ai/$example.ps1 | iex"
        $global:LASTEXITCODE = 2
        throw "${example}: $Message"
    }

    # Test-Runs returns whether a command runs and succeeds, discarding what it
    # prints. Inside it, a line the command writes to its error stream is not
    # an error of the script's own: under the Stop preference, Windows
    # PowerShell 5.1 would end the script on the first one it redirects.
    function Test-Runs {
        $ErrorActionPreference = 'Continue'
        $command = $args[0]
        $rest = @($args | Select-Object -Skip 1)
        try {
            & $command @rest 2>&1 | Out-Null
            return ($LASTEXITCODE -eq 0)
        } catch {
            return $false
        }
    }

    # run runs one of a step's commands, aggrega meaning the one found below,
    # when the step function runs to run its commands.
    function run {
        if ($mode -ne 'run') {
            return
        }
        $line = $args -join ' '
        Write-Host ''
        Write-Host ('$ ' + $line)
        $command = $args[0]
        if ($command -eq 'aggrega') {
            $command = $aggrega
        }
        $rest = @($args | Select-Object -Skip 1)
        $failed = $false
        try {
            & $command @rest | Out-Host
            if ($LASTEXITCODE -ne 0) {
                $failed = $true
            }
        } catch {
            $failed = $true
        }
        if ($failed) {
            Stop-Quickstart "the command failed: $line"
        }
    }

    # require_env refuses, when the step function runs to check, unless the
    # variable is set; it prints the line that sets it, with the page's example
    # value, and never the variable's own value.
    function require_env([string]$Name, [string]$Value) {
        if ($mode -ne 'check') {
            return
        }
        if (-not [Environment]::GetEnvironmentVariable($Name)) {
            Write-Say "step $step needs $Name set, and it is not; choose your own value, set it and run the script again:"
            Write-Host ''
            Write-Host ("  `$env:$Name = '" + $Value.Replace("'", "''") + "'")
            Write-Host ''
            Stop-Quickstart "$Name is not set."
        }
    }

    $steps = @()
    foreach ($argument in $Rest) {
        $text = "$argument"
        if (@('1', '2', '3', '4') -contains $text) {
            $steps += $text
        } elseif ($text.StartsWith('-')) {
            Stop-Usage "'$text' is not an option this script takes"
        } else {
            Stop-Usage "'$text' is not a step; the steps are 1, 2, 3 and 4"
        }
    }
    if ($steps.Count -gt 1) {
        Stop-Usage "give one step, not $($steps.Count) ($($steps -join ', '))"
    }
    # A step given as an argument is run; else the one QUICKSTART_STEP names;
    # else step 1.
    if ($steps.Count -eq 1) {
        $step = $steps[0]
    } elseif ($env:QUICKSTART_STEP) {
        $step = "$env:QUICKSTART_STEP".Trim()
        if (@('1', '2', '3', '4') -notcontains $step) {
            Stop-Usage "QUICKSTART_STEP is '$($env:QUICKSTART_STEP)', which is not a step; the steps are 1, 2, 3 and 4"
        }
    } else {
        $step = '1'
    }

    if (step_runs $step) {
        Write-Host 'This script will draft the example, build it and run it with Docker Compose.'
    } else {
        Write-Host 'This script will draft the example and build it; it will not run it.'
    }
    if (-not $GoAhead) {
        $answer = $null
        $asked = $false
        try {
            if (-not [Console]::IsInputRedirected) {
                Write-Host -NoNewline 'Continue? [y/N] '
                $answer = Read-Host
                $asked = $true
            }
        } catch {
            $asked = $false
        }
        if (-not $asked) {
            Stop-Quickstart 'there is nobody to ask whether to continue; run the script again from a PowerShell window you are at.'
        }
        if ("$answer".Trim() -notmatch '^(y|yes)$') {
            Stop-Quickstart 'stopped; nothing was done.'
        }
    }

    $found = Get-Command aggrega -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($found) {
        $aggrega = $found.Path
    } else {
        if ($env:AGGREGA_INSTALL_DIR) {
            $candidate = Join-Path $env:AGGREGA_INSTALL_DIR 'aggrega.exe'
        } elseif ($env:LOCALAPPDATA) {
            $candidate = Join-Path $env:LOCALAPPDATA 'Programs\aggrega\aggrega.exe'
        } else {
            $candidate = $null
        }
        if (-not $candidate -or -not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
            Stop-Quickstart 'aggrega is not on PATH or in the folder the installer puts it in; install it with: irm https://aggrega.ai/install.ps1 | iex'
        }
        $aggrega = $candidate
    }

    if (step_runs $step) {
        if (-not (Get-Command docker -CommandType Application -ErrorAction SilentlyContinue)) {
            Stop-Quickstart "docker is not on PATH, and step $step runs with Docker Compose."
        }
        if (-not (Test-Runs docker info)) {
            Stop-Quickstart "Docker does not answer ('docker info' failed); start it and run the script again."
        }
        if (-not (Test-Runs docker compose version)) {
            Stop-Quickstart "Docker Compose is not available ('docker compose version' failed)."
        }
    }

    $mode = 'check'
    & "step_$step"
    step_notes $step

    if (Test-Path -LiteralPath $example -PathType Container) {
        Write-Say "the folder $example already exists, so the script uses it as it is."
    } elseif (Test-Path -LiteralPath $example) {
        Stop-Quickstart "$example exists and is not a folder; move it away and run the script again."
    } else {
        Write-Say "writing the example into the folder $example."
        $draftingVerb = Get-DraftingVerb $aggrega
        $written = & {
            $ErrorActionPreference = 'Continue'
            try {
                $out = & $aggrega $draftingVerb --example $example $example 2>&1 | Out-String
                [pscustomobject]@{ Ok = ($LASTEXITCODE -eq 0); Text = $out }
            } catch {
                [pscustomobject]@{ Ok = $false; Text = $_.Exception.Message }
            }
        }
        if (-not $written.Ok) {
            Write-Host $written.Text
            Stop-Quickstart "'aggrega $draftingVerb --example $example' failed, as it says above."
        }
    }
    $stepFolder = Join-Path $example $step
    if (-not (Test-Path -LiteralPath $stepFolder -PathType Container)) {
        Stop-Quickstart "the folder $example holds no folder $step; move $example away and run the script again to write a new copy."
    }

    Push-Location -LiteralPath $stepFolder
    try {
        $mode = 'run'
        & "step_$step"
    } finally {
        Pop-Location
    }
    Write-Host ''
    step_end $step
    $global:LASTEXITCODE = 0
} ([bool]$Yes) $args
