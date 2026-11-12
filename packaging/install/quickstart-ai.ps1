# Run one step of quickstart-ai, the example in the Aggrega documentation that
# builds an AI agent on a local model up in four steps, from PowerShell:
#
#   & ([scriptblock]::Create((irm https://aggrega.ai/quickstart-ai.ps1))) 1
#
#   usage: quickstart-ai.ps1 [-Yes] 1|2|3|4
#
# It first says what it will do and asks to continue; -Yes goes ahead without
# asking, and where nobody can answer - its input redirected, or a host that
# reads none - it refuses unless -Yes is given. It checks that aggrega can be
# found, on PATH or in the folder the Aggrega installer puts it in, and, for a
# step that runs, that Docker answers and has Compose. It writes the example
# into .\quickstart-ai with `aggrega scaffold --example quickstart-ai`, or uses
# that folder as it is when it already exists, goes into the step's folder,
# quickstart-ai\<step>, and runs the commands the example's page gives for that
# step, then returns to the folder it started in. Steps 1 to 3 build and start
# the example with Docker Compose; step 4 builds its Kubernetes files and
# deploys nothing. It ends by saying what to run or open next; for steps 1 to 3
# the last line is the address of the chat page. It runs in Windows PowerShell
# 5.1 and in PowerShell 7.
#
# Settings, read from the environment:
#
#   AGGREGA_INSTALL_DIR   the folder to look for aggrega.exe in when PATH does
#                         not have it, as the installer reads it;
#                         %LOCALAPPDATA%\Programs\aggrega when unset.
#   OPENAI_API_KEY        for steps 2 and 3, the key of the hosted OpenAI model,
#                         which Docker Compose reads; unset, the local model
#                         answers instead. The script never prints it.
#
# Exit status. The script never closes the window it runs in, so it does not
# exit with a number: it leaves the number in $LASTEXITCODE, and every outcome
# but 0 ends in an error, which a run through powershell -Command or
# pwsh -Command reports as exit status 1.
#
#   0  the step was built, and started where it runs
#   1  a check or a command failed, or the answer was not yes
#   2  the arguments are not one step, 1 to 4, with -Yes or without it
#
# How the steps are written. Each step's commands are in the function
# step_<N>, one command per line, each line the word `run` followed by the
# command exactly as the example's page writes it in that step's block, less
# the block's `cd` lines, which the script replaces by its own. A variable a
# step needs set is a line of its own, `require_env <NAME> <value>`, which
# stands for the page's `export <NAME>=<value>` line where the page has one;
# the value is the page's example, printed when the variable is not set. The
# commands run from these lines and from nowhere else: each step function runs
# twice, first to check what require_env asks for, then to run the commands.
#
# Everything runs inside one script block, so none of its variables or
# preferences outlive it in the session that ran it. The script is kept to
# ASCII: Windows PowerShell 5.1 reads a file without a byte-order mark in the
# system's legacy code page.

# The script declares no [CmdletBinding()], so PowerShell hands it every
# argument -Yes does not take in $args rather than refusing it: the step, and
# anything else, which the script refuses itself, naming it.
param([switch]$Yes)

& {
    param([bool]$GoAhead, [object[]]$Rest)

    $ErrorActionPreference = 'Stop'
    $example = 'quickstart-ai'

    function step_1 {
        run aggrega build --release release.yaml --auto-pull
        run docker compose -p quickstart-ai -f dist/compose/docker-compose.yaml up -d --wait
    }

    function step_2 {
        run aggrega build --release release.yaml --auto-pull
        run docker compose -p quickstart-ai -f dist/compose/docker-compose.yaml up -d --wait
    }

    function step_3 {
        run aggrega build --release release.yaml --auto-pull
        run docker compose -p quickstart-ai -f dist/compose/docker-compose.yaml up -d --wait
    }

    function step_4 {
        run aggrega build --release release.yaml --auto-pull
    }

    # step_runs returns whether a step starts the example with Docker Compose,
    # rather than only building it.
    function step_runs([string]$Step) {
        return (@('1', '2', '3') -contains $Step)
    }

    # step_notes prints what the reader should know before the step runs.
    function step_notes([string]$Step) {
        if ((@('2', '3') -contains $Step) -and -not $env:OPENAI_API_KEY) {
            Write-Say 'OPENAI_API_KEY is not set, so the local model answers in place of the OpenAI model.'
        }
    }

    # step_end prints what to run or open next.
    function step_end([string]$Step) {
        if ($Step -eq '4') {
            Write-Say "the Kubernetes files are in $example\4\dist\helm\; the script deploys nothing, and $example\README.md says what applying them needs."
            return
        }
        Write-Say "step $Step is up. To send the agent a message from a terminal, run this in $example\${Step}:"
        Write-Host ''
        Write-Host '  docker compose -p quickstart-ai -f dist/compose/docker-compose.yaml exec agent quickstart-ask demo "My name is Ada. What is the capital of France?"'
        Write-Host ''
        Write-Say 'open http://127.0.0.1:8090/ in your browser to talk to the agent.'
    }

    # The machinery that runs a step from the functions above.

    function Write-Say([string]$Message) {
        Write-Host "${example}: $Message"
    }

    function Stop-Quickstart([string]$Message, [int]$Status = 1) {
        $global:LASTEXITCODE = $Status
        throw "${example}: $Message"
    }

    function Stop-Usage([string]$Message) {
        Write-Say $Message
        $global:LASTEXITCODE = 2
        throw "usage: $example.ps1 [-Yes] 1|2|3|4"
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
    if ($steps.Count -eq 0) {
        Stop-Usage 'give the step to run, 1, 2, 3 or 4'
    }
    if ($steps.Count -gt 1) {
        Stop-Usage "give one step, not $($steps.Count) ($($steps -join ', '))"
    }
    $step = $steps[0]

    if (step_runs $step) {
        Write-Host 'This script will scaffold the example, build it and run it with Docker Compose.'
    } else {
        Write-Host 'This script will scaffold the example and build it; it will not run it.'
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
            Stop-Quickstart 'there is nobody to ask; run the script again with -Yes to go ahead without asking.'
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
        $written = & {
            $ErrorActionPreference = 'Continue'
            try {
                $out = & $aggrega scaffold --example $example $example 2>&1 | Out-String
                [pscustomobject]@{ Ok = ($LASTEXITCODE -eq 0); Text = $out }
            } catch {
                [pscustomobject]@{ Ok = $false; Text = $_.Exception.Message }
            }
        }
        if (-not $written.Ok) {
            Write-Host $written.Text
            Stop-Quickstart "'aggrega scaffold --example $example' failed, as it says above."
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
