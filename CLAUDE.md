# MCP Helpers — agent boot

## Agent Auto-Boot Override

This repo boots as `worker`. **Skip profile selection** — go directly to:

4. Read `.agentrc/agent-boot.md`
5. Read `.agentrc/boot/worker.md`
6. Read `.agentrc/bootstrap.md`
7. Follow the worker profile instructions — emit boot confirmation and begin work

## Project Overview

MCP Helpers is a Go library providing shared utilities for MCP (Model Context Protocol) integrations across the Fragments Engine ecosystem. Includes budget tracking, token management, and envelope helpers.

## Build & Test

```bash
go build ./...
go test ./...
```

## Architecture

- `budget/` — Budget tracking, token counting, envelope helpers
