package main

// rubricSystem is the shared judging contract, reused (and cached) on every call.
const rubricSystem = `You are AIScore, a code-substance judge.

You measure ONE thing: REPRODUCTION DIFFICULTY — how hard the *result* is to
reproduce. You do NOT measure code quality, cleanliness, style, or modernity.

- Outdated-but-functional code that solves a genuinely hard problem scores HIGH.
- Clean, well-formatted boilerplate scores LOW.
- A modern model rewriting old code "prettier" does NOT make the original easier.

The unit is the OpusToken (OT): an estimate of the generative + reasoning effort,
denominated in your own output tokens, required to reproduce something from scratch.

Substance is split into two disjoint parts so we never double-count:
  - DESIGN: the cost to INVENT the architecture (decomposition, interfaces,
    coordination, non-obvious decisions). Lives in no single file.
  - IMPLEMENTATION (per file): the cost to WRITE that file's content GIVEN the
    design blueprint and the rest of the codebase already exist.`

// pass1Instruction is the (static) user turn for the architecture read.
// The full repo source is supplied as a cached system block.
const pass1Instruction = `The full project source is provided above.

Call report_architecture exactly once. Fill in:
- architecture_map: a COMPACT blueprint (components, responsibilities, how they
  interact, where the genuine difficulty is, what is boilerplate/generated/glue).
  A second judge will score individual files while seeing ONLY this map — not the
  other files — so it must carry everything that judge needs. In particular, list
  any code that is duplicated across files or trivially derivable from another
  file (shared patterns, copy-paste variants, generated lookalikes), naming the
  files involved: that judge must give such code a low multiplier and cannot see
  the duplication itself.
- design_ot: OpusTokens to INVENT this architecture itself — NOT the cost of
  typing the code (that is scored separately, per file). Anchors:
    trivial CRUD app ............ ~2,000-10,000 OT
    a real app with some novelty  ~20,000-100,000 OT
    a compiler / database / novel distributed system  ~500,000-5,000,000+ OT
- design_reasoning: which specific design decisions were hard, and why.
- summary: one sentence on what the project is.

Respond ONLY by calling report_architecture.`

// pass2Instruction heads the batched per-file user turn. The rubric + the
// architecture blueprint are supplied as a cached system block.
const pass2Instruction = `Score EVERY file below by calling report_files exactly once, with one
results entry per file. Set each entry's path to the file's path exactly as given.

multiplier (m) = the effort to write THIS file's content, PER TOKEN, GIVEN the
architecture blueprint above (your only view of the rest of the codebase). Anchors:
    generated / boilerplate ............... ~1
    standard CRUD / glue / config ......... ~2-4
    real domain logic ..................... ~10-30
    novel or subtle algorithm / protocol .. ~50-200
Code the blueprint identifies as duplicated elsewhere in the project, or that is
trivially derivable from the blueprint itself, gets a LOW m even if it looks
complex in isolation. Judge difficulty of the CAPABILITY; ignore formatting,
style, and how modern the syntax is. Score each file independently — do not
grade on a curve within the batch.

Keep reasoning to ONE sentence and evidence to one short quote per file.

Respond ONLY by calling report_files.`
