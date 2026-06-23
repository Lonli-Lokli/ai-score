package main

import (
	"context"
	"encoding/json"
	"fmt"
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

var chunkSchema = Schema{
	Name:        "report_chunk",
	Description: "Report the effort multiplier for one file, given the blueprint.",
	Object: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"category": map[string]any{
				"type": "string",
				"enum": []string{"generated", "boilerplate", "glue", "domain_logic", "novel_algorithm"},
			},
			"multiplier": map[string]any{"type": "number", "description": "effort multiplier m, 1..200"},
			"reasoning":  map[string]any{"type": "string"},
			"evidence":   map[string]any{"type": "string"},
		},
		"required":             []string{"category", "multiplier", "reasoning", "evidence"},
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
	Category   string  `json:"category"`
	Multiplier float64 `json:"multiplier"`
	Reasoning  string  `json:"reasoning"`
	Evidence   string  `json:"evidence"`
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
	raw, u, err := j.be.Score(ctx, system, pass1Instruction, archSchema, 16000)
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

// Pass2 scores one file against the cached blueprint, writing results onto c and
// returning the call's usage (used to derive c.Tokens on backends that can't
// pre-count).
func (j *Judge) Pass2(ctx context.Context, blueprint string, c *FileChunk) Usage {
	system := rubricSystem + "\n\nARCHITECTURE BLUEPRINT:\n" + blueprint
	user := fmt.Sprintf("%s\n\n=== FILE: %s ===\n%s", pass2Instruction, c.Path, c.Content)
	raw, u, err := j.be.Score(ctx, system, user, chunkSchema, 6000)
	j.cost.add(u)
	if err != nil {
		c.Err = err
		return u
	}
	var r chunkResult
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		c.Err = fmt.Errorf("parse chunk report: %w (raw: %s)", err, raw)
		return u
	}
	c.Category = r.Category
	c.Multiplier = clamp(r.Multiplier, 1, 200) // json_schema can't enforce numeric range; clamp here
	c.Reasoning = r.Reasoning
	c.Evidence = r.Evidence
	return u
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
