# Windows counterpart of pig-support.sh, with the same commands and messages.
# Keep the two scripts in step.
#
# Ported from herdr-pig (https://github.com/Hai-Fai-Solutions/herdr-pig),
# copyright 2026 Hai-Fai Solutions, Apache-2.0; see ..\NOTICE. Modified to
# install the Go Package with `pig install` instead of copying a file.
#
# usage: pig-support.ps1 <install|uninstall|status>
param([string]$Command = "")

$ErrorActionPreference = "Stop"

$Package = "npm:@pi-in-go/pigpen-herdr"
$LegacyName = "herdr-pig-agent-state.ts"
$LegacyMarker = "HERDR_PIG_EXTENSION_VERSION="

# Mirrors PiG's own lookup: PIG_CODING_AGENT_DIR, then PIG_HOME/agent, then ~/.pig/agent.
function Get-AgentDir {
    if ($env:PIG_CODING_AGENT_DIR) {
        return $env:PIG_CODING_AGENT_DIR
    }
    if ($env:PIG_HOME) {
        return (Join-Path $env:PIG_HOME "agent")
    }
    $userHome = if ($env:USERPROFILE) { $env:USERPROFILE } else { $env:HOME }
    return (Join-Path (Join-Path $userHome ".pig") "agent")
}

# Show the result in herdr when run as a plugin action; stay quiet otherwise.
function Send-Notice([string]$Title, [string]$Body) {
    if ($env:HERDR_ENV -eq "1" -and $env:HERDR_BIN_PATH) {
        try {
            & $env:HERDR_BIN_PATH notification show $Title --body $Body *> $null
        } catch {
        }
    }
}

function Stop-WithError([string]$Message) {
    [Console]::Error.WriteLine("pigpen-herdr: $Message")
    Send-Notice "PiG support" $Message
    exit 1
}

function Assert-Pig {
    if (-not (Get-Command pig -ErrorAction SilentlyContinue)) {
        Stop-WithError "pig is not on PATH; install PiG first"
    }
}

function Test-Installed {
    $listing = (& pig list 2>$null) -join "`n"
    return $listing.Contains($Package)
}

$Legacy = Join-Path (Join-Path (Get-AgentDir) "extensions") $LegacyName

switch ($Command) {
    "install" {
        # With PIG_USE_PI_DIRS=1, PiG shares Pi's settings, so Pi would be asked to
        # load the Package too.
        if ($env:PIG_USE_PI_DIRS -eq "1") {
            Stop-WithError "PIG_USE_PI_DIRS=1 shares Pi's settings; unset it before installing"
        }
        Assert-Pig
        if (Test-Installed) {
            Write-Output "PiG support is already installed ($Package)"
        } else {
            & pig install $Package
            if ($LASTEXITCODE -ne 0) { Stop-WithError "pig install $Package failed" }
            Write-Output "installed PiG support ($Package)"
        }
        # Two reporters in one pane would both claim it, so the herdr-pig copy goes.
        if ((Test-Path -LiteralPath $Legacy -PathType Leaf) -and (Select-String -LiteralPath $Legacy -SimpleMatch -Pattern $LegacyMarker -Quiet)) {
            Remove-Item -LiteralPath $Legacy -Force
            Write-Output "removed the herdr-pig extension it replaces ($Legacy)"
        }
        Write-Output "restart pig, or run /reload in a running session"
        Send-Notice "PiG support installed" "Restart pig or run /reload in a running session."
    }
    "uninstall" {
        Assert-Pig
        if (Test-Installed) {
            & pig remove $Package
            if ($LASTEXITCODE -ne 0) { Stop-WithError "pig remove $Package failed" }
            Write-Output "removed PiG support ($Package)"
            Send-Notice "PiG support removed" "Restart pig or run /reload in a running session."
        } else {
            Write-Output "PiG support is not installed ($Package)"
            Send-Notice "PiG support" "Not installed."
        }
    }
    "status" {
        Assert-Pig
        if (Test-Installed) {
            Write-Output "installed ($Package); pig update $Package updates it"
            Send-Notice "PiG support" "Installed."
        } else {
            Write-Output "not installed ($Package)"
            Send-Notice "PiG support" "Not installed."
        }
        if (Test-Path -LiteralPath $Legacy -PathType Leaf) {
            Write-Output "the herdr-pig extension is also installed ($Legacy); run install to replace it"
        }
    }
    default {
        [Console]::Error.WriteLine("usage: pig-support.ps1 <install|uninstall|status>")
        exit 2
    }
}
