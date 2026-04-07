---
type: agent-boot
version: 1
updated_at: 2026-03-08
---

# Agent Boot — MCP Helpers

## Ground truth
- `agentrc.yaml` — system configuration
- `.agentrc/bootstrap.md` — current iteration state
- `.agentrc/tasks/` — task state (read-only cache, Volon is source of truth)

## Core rules
- Always check Volon for your assigned tasks: use `volon_tasks_list` with project_id="mcp-helpers"
- Update task status via `volon_task_transition` when starting (doing), completing (done), or blocked
- Do NOT proceed to next task until current task is transitioned
- Ground truth is in files and Volon, not chat history
