---
name: analyze-agent-usage
description: >
  Queries and analyzes GitHub Copilot agent usage metrics stored locally by arok (Agent Resource
  Observation Kit). Retrieves token consumption, session history, per-repo and per-host breakdowns,
  model usage, and cost-relevant data from a local SQLite database. Enables agents to reason
  about AI resource consumption for tasks, features, sprints, or individual sessions — supporting
  workflows like JIRA time/cost tracking, team usage reporting, multi-host container
  observability, and agentic budget analysis. Reads data only; never modifies the arok database.
  WHEN: query agent usage, analyze token consumption, how many tokens did the agent use, check
  copilot usage, arok query, arok analyze, usage by repo, usage by branch, usage by host,
  session metrics, show me token usage, AI resource consumption, agent cost tracking,
  how much did this task cost, usage report, Copilot session data, analyze agent sessions,
  agentic resource usage, token budget.
license: "MIT"
metadata:
  author: "srbouffard"
  version: "1.0.0"
  summary: "Query and analyze local Copilot agent usage metrics via the arok CLI."
  tags:
    - arok
    - observability
    - copilot
    - agent-usage
    - tokens
    - analytics
---

# Analyzing Agent Usage with arok

## Overview

`arok` is a local-first CLI that captures GitHub Copilot (CLI and VS Code) usage into a SQLite database. Use this skill to retrieve and reason about token consumption, session histories, and AI resource costs across repos, branches, hosts, and models.

Before running any command, verify arok is available:

```bash
arok doctor
```

If `arok` is not installed or `state_dir` shows an error, stop and instruct the user to install: `curl -fsSL https://raw.githubusercontent.com/srbouffard/arok/main/install.sh | bash`.

---

## Workflow Decision Tree

Determine what the user needs:

1. **Overall usage summary / recent activity?** → §1 Overview & Recent Sessions
2. **Usage for a specific repo, branch, or host?** → §2 Filtered Queries
3. **Token breakdown by model?** → §3 Model Analysis
4. **Usage for a specific task or session?** → §4 Session Deep Dive
5. **Multi-host or container usage?** → §5 Multi-Host Analysis

---

## §1 Overview & Recent Sessions

Get an aggregate picture of all captured sessions:

```bash
arok analyze overview
```

For a time-bounded window (e.g. last week):

```bash
arok analyze overview --since 168h --limit 5
```

Returns: total sessions, token totals (input/output/cache/reasoning), and top-N breakdowns by host, repo, branch, and model.

List recent sessions:

```bash
arok query sessions --latest 20
```

---

## §2 Filtered Queries

Query sessions scoped to a specific dimension. Flags can be combined. Use `--since` to keep results focused.

**By repository:**
```bash
arok query repos --since 168h
arok query sessions --repo https://github.com/org/repo --since 168h
```

**By branch:**
```bash
arok query branches --since 168h
arok query sessions --branch main --since 168h
```

**By host:**
```bash
arok query hosts
arok query sessions --host <hostname> --since 168h
```

**By harness (copilot-cli vs copilot-vscode):**
```bash
arok query harnesses --since 168h
```

Filtered `query sessions` appends a totals line — use it when reporting resource consumption for a task, feature, or sprint:

```
totals  sessions=7  input=16950294  output=166860  cache_read=15615328  reasoning=45729
```

---

## §3 Model Analysis

Show token usage broken down by AI model:

```bash
arok query models --since 168h --limit 10
```

Returns: model name, sessions, assistant messages, requests, input/output/cache/reasoning tokens.

---

## §4 Session Deep Dive

Inspect a single session's full JSON record (all token fields, git context, hostname, harness, timestamps, per-model breakdown):

```bash
arok query sessions --session-id <session-id>
```

---

## §5 Multi-Host Analysis

When arok runs across multiple machines or containers sharing a state directory:

```bash
arok query hosts
arok query sessions --host agent-1 --since 168h
```

Combine with `--branch` or `--repo` to scope to a specific workload across all machines.

---

## Reporting Guidelines

- **Show the totals line** from filtered `query sessions` when answering "how much did X cost"
- **Round large token counts** for readability (e.g. "~16.9M input tokens")
- **Convert to cost estimates** only if the user asks and provides pricing; never invent costs
- **Flag data gaps**: sessions with `capture_state=provisional` or `best_effort` may have incomplete counts
- **Default time window**: use `--since 168h` (7 days) unless the user specifies otherwise

For complete flag reference and output format details, see `references/commands.md`.
