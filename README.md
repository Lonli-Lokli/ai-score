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
# Easiest — if you already use Claude Code (no API key):
go run github.com/Lonli-Lokli/ai-score@latest -backend cli -path .

# Or with an Anthropic API key:
ANTHROPIC_API_KEY=sk-ant-... go run github.com/Lonli-Lokli/ai-score@latest -path .
```

`-path .` scores the current folder — point it anywhere.

**What you get:** a score in **OpusTokens**, an effort tier (*AI-trivial → weeks
of work*), and a per-file breakdown. Each run costs **~$1–3** of model usage
(your API key, or drawn from your Claude credits).

Options: `-path` (folder) · `-backend api|cli` · `-n` (accuracy passes) ·
`-max-files` (cost guard).

> **Spike caveats:** file-level chunks (not function-level), sequential scoring,
> placeholder effort-tier thresholds. Lines marked `// VERIFY` confirm
> `anthropic-sdk-go` / `claude -p` shapes against your installed versions.
