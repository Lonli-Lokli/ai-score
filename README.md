# aiscore (spike)

Score a codebase by how much genuine engineering effort it represents —
*"is this AI-trivial / vibe-coded, or did someone actually spend hours on it?"*

## The idea

Judge a project by **how hard the result is to reproduce**, not by how clean the
code looks. `aiscore` measures that in **OpusTokens (OT)** — the estimated effort,
in Opus tokens, to recreate the project from scratch. Clean boilerplate scores
low; a clunky-but-hard algorithm scores high. It is **not** a quality grader.

Opus is the judge (LLM-primary); the only deterministic input is token counts.
Substance splits into two parts that can't double-count:

```
Substance OT = Implementation OT + Design OT
  Implementation OT = Σ tokens(file) × m(file)   # cost to WRITE each file, given the blueprint
  Design OT                                       # cost to INVENT the architecture itself
```

`m` is an Opus-judged effort multiplier (boilerplate ≈1 … novel algorithm ≈200).
A cached "architecture read" produces the blueprint + Design OT; each file is then
scored against that blueprint — so spreading a hard idea across clean files no
longer loses points.

This directory is a **spike** validating that core before the full tool is built.

## Quick start

You need **Go** (one-time) and **Opus access** — either Claude Code *or* an API key.

**1. Install Go** (skip if you have it):
`winget install GoLang.Go` (Windows) · `brew install go` (macOS) · or [go.dev/dl](https://go.dev/dl)

**2. Run it** — no clone, no build. Pick whichever you have:

```sh
# Recommended — with an Anthropic API key (exact token counts, reliable caching):
ANTHROPIC_API_KEY=sk-ant-... go run github.com/Lonli-Lokli/ai-score@latest -path .

# Convenience/demo mode — if you already use Claude Code (no API key):
go run github.com/Lonli-Lokli/ai-score@latest -backend cli -path .
```

`-path .` scores the current folder — point it anywhere.

**What you get:** a score in **OpusTokens**, an effort tier (*AI-trivial → weeks
of work*), and a per-file breakdown. Each run costs **~$1–3** of model usage.

**Comparing two projects:** run once per project with `-backend api` and
`-max-files 0`, same flags both times, and compare the Substance OT totals.
The `cli` backend is demo-only for comparisons — its per-file token counts are
approximate, caching across invocations is unreliable, and it draws from your
Claude subscription's usage window.

Options: `-path` (folder) · `-backend api|cli` · `-n` (Design-OT accuracy
passes) · `-max-files` (cost guard; keeps the largest files and warns) ·
`-batch` (files per Pass-2 call; batching amortizes thinking overhead, the
dominant cost) · `-verify-top` (median-of-3 multipliers for the K
token-heaviest files, which dominate the score).

> **Spike caveats:** file-level chunks (not function-level), sequential scoring,
> placeholder effort-tier thresholds. SDK shapes verified against
> `anthropic-sdk-go v1.58.0`; `claude -p` envelope fields still carry
> `// VERIFY` markers.
