package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ---- structured-output schemas ---------------------------------------------

var archSchema = Schema{
	Name:        "report_architecture",
	Description: "Report the project's architecture blueprint and Design OT.",
	Object: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary":          map[string]any{"type": "string"},
			"architecture_map": map[string]any{"type": "string"},
			"design_ot":        map[string]any{"type": "integer", "description": "OpusTokens to invent the architecture"},
			"design_reasoning": map[string]any{"type": "string"},
		},
		"required":             []string{"summary", "architecture_map", "design_ot", "design_reasoning"},
		"additionalProperties": false,
	},
}

var batchSchema = Schema{
	Name:        "report_files",
	Description: "Report the effort multiplier for every file in the batch, given the blueprint.",
	Object: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"results": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "file path exactly as given"},
						"category": map[string]any{
							"type": "string",
							"enum": []string{"generated", "boilerplate", "glue", "domain_logic", "novel_algorithm"},
						},
						"multiplier": map[string]any{"type": "number", "description": "effort multiplier m, 1..200"},
						"reasoning":  map[string]any{"type": "string"},
						"evidence":   map[string]any{"type": "string"},
					},
					"required":             []string{"path", "category", "multiplier", "reasoning", "evidence"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"results"},
		"additionalProperties": false,
	},
}

// ---- parsed results ---------------------------------------------------------

type ArchReport struct {
	Summary         string `json:"summary"`
	ArchitectureMap string `json:"architecture_map"`
	DesignOT        int64  `json:"design_ot"`
	DesignReasoning string `json:"design_reasoning"`
}

type chunkResult struct {
	Path       string  `json:"path"`
	Category   string  `json:"category"`
	Multiplier float64 `json:"multiplier"`
	Reasoning  string  `json:"reasoning"`
	Evidence   string  `json:"evidence"`
}

type batchResult struct {
	Results []chunkResult `json:"results"`
}

// ---- judge (backend-agnostic) ----------------------------------------------

type Judge struct {
	be   Backend
	cost Cost
}

func NewJudge(be Backend) *Judge { return &Judge{be: be} }

// Pass1 runs one architecture read; the repo sits in the (cached) prefix so the
// median-of-N calls reuse it cheaply.
func (j *Judge) Pass1(ctx context.Context, repo string) (ArchReport, error) {
	system := rubricSystem + "\n\nFULL PROJECT SOURCE:\n\n" + repo
	raw, u, err := j.be.Score(ctx, system, pass1Instruction, archSchema, 16000,
		ScoreOpts{Effort: "high", LongCache: true})
	j.cost.add(u)
	if err != nil {
		return ArchReport{}, err
	}
	var r ArchReport
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return ArchReport{}, fmt.Errorf("parse arch report: %w (raw: %s)", err, raw)
	}
	return r, nil
}

// Pass2Batch scores a batch of files in one call against the cached blueprint,
// writing results (or per-file errors) onto the chunks. Batching amortizes the
// per-call thinking/output overhead — the dominant cost of a run.
func (j *Judge) Pass2Batch(ctx context.Context, blueprint string, batch []*FileChunk) Usage {
	system := rubricSystem + "\n\nARCHITECTURE BLUEPRINT:\n" + blueprint
	var sb strings.Builder
	sb.WriteString(pass2Instruction)
	for _, c := range batch {
		sb.WriteString("\n\n=== FILE: ")
		sb.WriteString(c.Path)
		sb.WriteString(" ===\n")
		sb.WriteString(c.Content)
	}
	maxTokens := int64(4000 + 1000*len(batch))
	if maxTokens > 32000 {
		maxTokens = 32000
	}
	raw, u, err := j.be.Score(ctx, system, sb.String(), batchSchema, maxTokens,
		ScoreOpts{Effort: "medium"})
	j.cost.add(u)
	if err != nil {
		for _, c := range batch {
			c.Err = err
		}
		return u
	}
	var r batchResult
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		for _, c := range batch {
			c.Err = fmt.Errorf("parse batch report: %w (raw: %s)", err, raw)
		}
		return u
	}
	byPath := make(map[string]chunkResult, len(r.Results))
	for _, res := range r.Results {
		byPath[res.Path] = res
	}
	for _, c := range batch {
		res, ok := byPath[c.Path]
		if !ok {
			c.Err = fmt.Errorf("no result returned for %s", c.Path)
			continue
		}
		c.Err = nil
		c.Category = res.Category
		c.Multiplier = clamp(res.Multiplier, 1, 200)
		c.Reasoning = res.Reasoning
		c.Evidence = res.Evidence
	}
	return u
}

// VerifyTop re-scores the top-K token-heaviest files twice more and takes the
// per-file median multiplier. Σ tokens×m is dominated by the largest files, so
// a single noisy sample there can flip an A-vs-B comparison; two extra calls
// buy medians exactly where the variance matters.
func (j *Judge) VerifyTop(ctx context.Context, blueprint string, chunks []*FileChunk, k int) {
	scored := make([]*FileChunk, 0, len(chunks))
	for _, c := range chunks {
		if c.Err == nil {
			scored = append(scored, c)
		}
	}
	sort.SliceStable(scored, func(a, b int) bool { return scored[a].Tokens > scored[b].Tokens })
	if k > len(scored) {
		k = len(scored)
	}
	if k == 0 {
		return
	}
	top := scored[:k]

	samples := make(map[string][]float64, k)
	for _, c := range top {
		samples[c.Path] = []float64{c.Multiplier}
	}
	for s := 0; s < 2; s++ {
		probes := make([]*FileChunk, k)
		for i, c := range top {
			probes[i] = &FileChunk{Path: c.Path, Content: c.Content}
		}
		j.Pass2Batch(ctx, blueprint, probes)
		for _, p := range probes {
			if p.Err == nil {
				samples[p.Path] = append(samples[p.Path], p.Multiplier)
			}
		}
	}
	for _, c := range top {
		c.Multiplier = median(samples[c.Path])
	}
}

func median(vals []float64) float64 {
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
