# Project Context for AI Coding Agents

This file provides architectural guidelines and operational instructions for AI coding assistants working in this repository.

## What This Repository Is

`phi-agent-code` is a hands-on learning workshop and production-ready reference implementation of an **AI coding agent harness in Go**. It traces the evolutionary design of tools like Claude Code, Aider, and OpenCode across 27 incremental milestones.

## Core Conventions & Rules

1. **Evolutionary Architecture (Concepts > Code)**:
   - Do NOT introduce future abstractions prematurely.
   - For example, do not create `internal/` packages or separate command registries until the designated steps (`step/21-internal-architecture` and `step/10-slash-commands`). Keep code scoped to the active milestone.
2. **Errors as Tool Results (Never Panic)**:
   - When a tool execution fails (file not found, syntax error, permission denied), return `(errorMessage, true)`.
   - Never terminate the agent loop or trigger unhandled Go panics. Failures travel back to the LLM so it can observe and attempt self-correction.
3. **Cross-Platform Compatibility**:
   - The harness must work seamlessly on both Windows (PowerShell/cmd.exe/Git bash) and POSIX systems (Linux/macOS).
4. **Git & Commit Conventions**:
   - Use strict **Conventional Commits** (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`).
   - NEVER add AI co-author attributions (such as "Co-Authored-By: Assistant").

## Build & Test Commands

```bash
# Run the active harness
go run .

# Sanity build check
go build ./...

# Static analysis
go vet ./...
```

## Milestone Documentation

Detailed step-by-step guides with architecture diagrams and verification scenarios live under `docs/steps/` (`01-minimal-loop.md` to `27-harness-testing.md`).
