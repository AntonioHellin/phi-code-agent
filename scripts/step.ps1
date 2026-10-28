<#
.SYNOPSIS
    Automated workflow manager for Phi Agent Code milestone steps.

.DESCRIPTION
    Enforces architectural discipline, runs pre-flight validation checks,
    creates isolated milestone branches, and safely merges completed steps into the base branch.

.EXAMPLE
    .\scripts\step.ps1 start 2
    .\scripts\step.ps1 2 start
    .\scripts\step.ps1 step 2 start
    .\scripts\step.ps1 finish
    .\scripts\step.ps1 status
#>

param(
    [Parameter(Position = 0, Mandatory = $false)]
    [string]$Arg1,

    [Parameter(Position = 1, Mandatory = $false)]
    [string]$Arg2,

    [Parameter(Position = 2, Mandatory = $false)]
    [string]$Arg3
)

# Normalize arguments across combinations:
# "step 1 start", "1 start", "start 1", "finish", "1 finish", "status", "1 status", "1"
$rawArgs = @($Arg1, $Arg2, $Arg3) | Where-Object { $_ -and $_.Trim() -ne "" }
$tokens = $rawArgs | Where-Object { $_ -notmatch '^(step|milestone)$' }

$Action = $null
$Step = $null

foreach ($token in $tokens) {
    if ($token -match '^\d+$') {
        $Step = $token
    } elseif ($token -match '^(start|finish|status)$') {
        $Action = $token.ToLower()
    }
}

# Defaults
if (-not $Action -and $Step) {
    $Action = "start"
} elseif (-not $Action -and -not $Step) {
    $Action = "status"
}

if ($Action -notin @("start", "finish", "status")) {
    Write-Host "[-] Error: Could not determine action ('start', 'finish', or 'status')." -ForegroundColor Red
    Write-Host "    Supported formats:" -ForegroundColor Yellow
    Write-Host "      .\scripts\step.ps1 start 1   (or .\scripts\step.ps1 1 start / .\scripts\step.ps1 step 1 start)" -ForegroundColor DarkGray
    Write-Host "      .\scripts\step.ps1 finish    (or .\scripts\step.ps1 1 finish / .\scripts\step.ps1 step 1 finish)" -ForegroundColor DarkGray
    Write-Host "      .\scripts\step.ps1 status    (or .\scripts\step.ps1 1 status / .\scripts\step.ps1 step 1 status)" -ForegroundColor DarkGray
    exit 1
}

$Steps = @{
    1 = @{ Branch = "step/01-minimal-loop"; Guide = "docs/steps/01-minimal-loop.md"; Title = "The Minimal Loop" }
    2 = @{ Branch = "step/02-permission-gate"; Guide = "docs/steps/02-permission-gate.md"; Title = "The Permission Gate" }
    3 = @{ Branch = "step/03-permission-policies"; Guide = "docs/steps/03-permission-policies.md"; Title = "Permission Policies" }
    4 = @{ Branch = "step/04-tool-retry"; Guide = "docs/steps/04-tool-retry.md"; Title = "Tool Error Retry" }
    5 = @{ Branch = "step/05-provider-interface"; Guide = "docs/steps/05-provider-interface.md"; Title = "Provider Abstraction" }
    6 = @{ Branch = "step/06-providers-and-mocks"; Guide = "docs/steps/06-providers-and-mocks.md"; Title = "Alternative Providers and Mocks" }
    7 = @{ Branch = "step/07-streaming-responses"; Guide = "docs/steps/07-streaming-responses.md"; Title = "Streaming Responses" }
    8 = @{ Branch = "step/08-image-inputs"; Guide = "docs/steps/08-image-inputs.md"; Title = "Multimodal and Image Inputs" }
    9 = @{ Branch = "step/09-terminal-polish"; Guide = "docs/steps/09-terminal-polish.md"; Title = "Terminal Polish and UX" }
    10 = @{ Branch = "step/10-slash-commands"; Guide = "docs/steps/10-slash-commands.md"; Title = "Slash Commands" }
    11 = @{ Branch = "step/11-advanced-input"; Guide = "docs/steps/11-advanced-input.md"; Title = "Advanced Input Handling" }
    12 = @{ Branch = "step/12-conversation-state"; Guide = "docs/steps/12-conversation-state.md"; Title = "Conversation State" }
    13 = @{ Branch = "step/13-transcript-renderer"; Guide = "docs/steps/13-transcript-renderer.md"; Title = "Transcript Renderer" }
    14 = @{ Branch = "step/14-compaction-strategies"; Guide = "docs/steps/14-compaction-strategies.md"; Title = "Compaction Strategies" }
    15 = @{ Branch = "step/15-token-budget-and-cost"; Guide = "docs/steps/15-token-budget-and-cost.md"; Title = "Token Budgeting and Cost Tracking" }
    16 = @{ Branch = "step/16-prompt-caching"; Guide = "docs/steps/16-prompt-caching.md"; Title = "Prompt Caching" }
    17 = @{ Branch = "step/17-tool-registry"; Guide = "docs/steps/17-tool-registry.md"; Title = "Plug-and-Play Tool Registry" }
    18 = @{ Branch = "step/18-custom-tools"; Guide = "docs/steps/18-custom-tools.md"; Title = "Custom Tools Suite" }
    19 = @{ Branch = "step/19-diff-approval"; Guide = "docs/steps/19-diff-approval.md"; Title = "Diff Approval for File Writes" }
    20 = @{ Branch = "step/20-mcp-support"; Guide = "docs/steps/20-mcp-support.md"; Title = "Model Context Protocol (MCP)" }
    21 = @{ Branch = "step/21-internal-architecture"; Guide = "docs/steps/21-internal-architecture.md"; Title = "Internal Package Architecture" }
    22 = @{ Branch = "step/22-agents-md-context"; Guide = "docs/steps/22-agents-md-context.md"; Title = "Project Context via AGENTS.md" }
    23 = @{ Branch = "step/23-subagents-delegation"; Guide = "docs/steps/23-subagents-delegation.md"; Title = "Subagents and Task Delegation" }
    24 = @{ Branch = "step/24-markdown-subagents"; Guide = "docs/steps/24-markdown-subagents.md"; Title = "Markdown-Configured Subagents" }
    25 = @{ Branch = "step/25-persistent-memory"; Guide = "docs/steps/25-persistent-memory.md"; Title = "Persistent Agent Memory" }
    26 = @{ Branch = "step/26-full-tui"; Guide = "docs/steps/26-full-tui.md"; Title = "Full Terminal UI (TUI)" }
    27 = @{ Branch = "step/27-harness-testing"; Guide = "docs/steps/27-harness-testing.md"; Title = "Harness End-to-End Testing" }
}

# Automatically configure repository hooks path for commit prefixing
$currentHooks = (git config --get core.hooksPath)
if ($currentHooks -ne ".githooks") {
    git config core.hooksPath .githooks
}

function Branch-Exists($branchName) {
    & git show-ref --verify --quiet "refs/heads/$branchName"
    return ($LASTEXITCODE -eq 0)
}

function Get-BaseBranch {
    if (Branch-Exists "main") { return "main" }
    if (Branch-Exists "master") { return "master" }
    return "main"
}

function Branch-IsMerged($branchName) {
    $base = Get-BaseBranch
    & git merge-base --is-ancestor $branchName $base
    return ($LASTEXITCODE -eq 0)
}

function Assert-CleanWorkTree {
    $status = git status --porcelain
    if ($status) {
        Write-Host "[-] Error: Working tree has uncommitted changes. Commit or stash them first." -ForegroundColor Red
        git status -s
        exit 1
    }
}

function Run-HealthChecks {
    Write-Host "[*] Running Go code health checks (vet and build)..." -ForegroundColor Cyan
    & go vet ./...
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[-] Error: 'go vet ./...' failed." -ForegroundColor Red
        exit 1
    }
    & go build ./...
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[-] Error: 'go build ./...' failed." -ForegroundColor Red
        exit 1
    }
    if (Test-Path phi-agent-code.exe) { Remove-Item phi-agent-code.exe }
    Write-Host "[+] Health checks passed cleanly." -ForegroundColor Green
}

if ($Action -eq "status") {
    $currentBranch = (git branch --show-current).Trim()
    Write-Host ""
    Write-Host "=== Phi Agent Code Roadmap Status (Current: $currentBranch) ===" -ForegroundColor Cyan
    Write-Host ""
    for ($i = 1; $i -le 27; $i++) {
        $info = $Steps[$i]
        $branch = $info.Branch
        $exists = Branch-Exists $branch
        $isMerged = $false
        if ($exists) {
            $isMerged = Branch-IsMerged $branch
        }

        $prefix = "[ ] PENDING"
        $color = "DarkGray"
        if ($currentBranch -eq $branch) {
            $prefix = "[*] ACTIVE "
            $color = "Yellow"
        } elseif ($isMerged) {
            $prefix = "[X] MERGED "
            $color = "Green"
        } elseif ($exists) {
            $prefix = "[+] STARTED"
            $color = "Cyan"
        }

        $title = $info.Title
        $numStr = $i.ToString('d2')
        Write-Host "$prefix Step $numStr : $title ($branch)" -ForegroundColor $color
    }
    Write-Host ""
    if ($Step) {
        $sNum = [int]$Step
        if ($Steps.ContainsKey($sNum)) {
            $info = $Steps[$sNum]
            Write-Host "--- Focus: Step $($sNum.ToString('d2')) : $($info.Title) ---" -ForegroundColor Cyan
            Write-Host "Branch: $($info.Branch)" -ForegroundColor DarkGray
            Write-Host "Guide:  $($info.Guide)" -ForegroundColor DarkGray
            Write-Host ""
        }
    }
}
elseif ($Action -eq "start") {
    if (-not $Step) {
        Write-Host "[-] Error: Please provide step number. Example: .\scripts\step.ps1 start 2 (or .\scripts\step.ps1 2)" -ForegroundColor Red
        exit 1
    }
    $stepNum = [int]$Step
    if (-not $Steps.ContainsKey($stepNum)) {
        Write-Host "[-] Error: Invalid step number '$Step'. Must be between 1 and 27." -ForegroundColor Red
        exit 1
    }

    $target = $Steps[$stepNum]
    $stepPadded = $stepNum.ToString('d2')
    $targetTitle = $target.Title
    Write-Host ""
    Write-Host "[*] Preparing to start Step $stepPadded - $targetTitle..." -ForegroundColor Cyan

    Assert-CleanWorkTree

    $baseBranch = Get-BaseBranch
    $current = (git branch --show-current).Trim()
    if ($current -ne $baseBranch) {
        Write-Host "[*] Switching to '$baseBranch' branch..." -ForegroundColor Yellow
        git checkout $baseBranch
    }

    if ($stepNum -gt 1) {
        $prevNum = $stepNum - 1
        $prevInfo = $Steps[$prevNum]
        $prevBranch = $prevInfo.Branch

        if (Branch-Exists $prevBranch) {
            if (-not (Branch-IsMerged $prevBranch)) {
                Write-Host "[-] Pre-flight check failed: Previous step '$prevBranch' is NOT yet merged into '$baseBranch'." -ForegroundColor Red
                Write-Host "Finish and merge Step $($prevNum.ToString('d2')) before proceeding to Step $stepPadded." -ForegroundColor Yellow
                exit 1
            }
        }
    }

    Run-HealthChecks

    $targetBranch = $target.Branch
    if (Branch-Exists $targetBranch) {
        Write-Host "[*] Switching to existing branch '$targetBranch'..." -ForegroundColor Yellow
        git checkout $targetBranch
    } else {
        Write-Host "[+] Creating and switching to milestone branch '$targetBranch'..." -ForegroundColor Green
        git checkout -b $targetBranch $baseBranch
    }

    $guidePath = $target.Guide
    Write-Host ""
    Write-Host "[+] Ready to work on Step $stepPadded - $targetTitle!" -ForegroundColor Green
    Write-Host "    Read guide: $guidePath" -ForegroundColor Cyan
    Write-Host "    When done, run: .\scripts\step.ps1 finish" -ForegroundColor DarkGray
    Write-Host ""
}
elseif ($Action -eq "finish") {
    $currentBranch = (git branch --show-current).Trim()
    if ($currentBranch -notmatch "^step/(\d+)-") {
        Write-Host "[-] Error: Current branch '$currentBranch' is not a step milestone branch (expected 'step/XX-*')." -ForegroundColor Red
        exit 1
    }
    $stepNum = [int]$Matches[1]
    $target = $Steps[$stepNum]
    $stepPadded = $stepNum.ToString('d2')
    $targetTitle = $target.Title

    Write-Host ""
    Write-Host "[*] Completing Step $stepPadded - $targetTitle..." -ForegroundColor Cyan

    Assert-CleanWorkTree
    Run-HealthChecks

    $baseBranch = Get-BaseBranch
    Write-Host "[*] Switching to '$baseBranch'..." -ForegroundColor Yellow
    git checkout $baseBranch

    Write-Host "[+] Merging '$currentBranch' into '$baseBranch'..." -ForegroundColor Green
    git merge $currentBranch --no-ff -m "[$currentBranch] feat: complete step $stepPadded $targetTitle"

    Run-HealthChecks

    Write-Host ""
    Write-Host "[+] Step $stepPadded successfully completed and merged into $baseBranch!" -ForegroundColor Green
    Write-Host "    The branch '$currentBranch' remains preserved as a milestone snapshot." -ForegroundColor Cyan
    if ($stepNum -lt 27) {
        $nextNum = $stepNum + 1
        Write-Host "    Next step: .\scripts\step.ps1 start $nextNum" -ForegroundColor Yellow
    } else {
        Write-Host "    Congratulations! You have completed all 27 milestones of the harness!" -ForegroundColor Magenta
    }
    Write-Host ""
}
