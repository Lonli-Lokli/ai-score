package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type fakeBackend struct{ calls int }

func (f *fakeBackend) Name() string      { return "fake" }
func (f *fakeBackend) CanPreCount() bool { return false }
func (f *fakeBackend) CountTokens(ctx context.Context, text string) (int64, error) {
	return 0, fmt.Errorf("no")
}
func (f *fakeBackend) Score(ctx context.Context, system, user string, schema Schema, maxTokens int64, opts ScoreOpts) (string, Usage, error) {
	f.calls++
	if schema.Name != "report_files" {
		return "", Usage{}, fmt.Errorf("unexpected schema %s", schema.Name)
	}
	// Return one result per FILE header in the user turn, multiplier varies per call.
	var results []chunkResult
	for _, p := range []string{"a.go", "b.go"} {
		if strings.Contains(user, "=== FILE: "+p+" ===") {
			results = append(results, chunkResult{
				Path: p, Category: "glue", Multiplier: float64(10 * f.calls),
				Reasoning: "r", Evidence: "e",
			})
		}
	}
	raw, _ := json.Marshal(batchResult{Results: results})
	return string(raw), Usage{Input: 100, Output: 50}, nil
}

func TestPass2BatchAndVerify(t *testing.T) {
	fb := &fakeBackend{}
	j := NewJudge(fb)
	chunks := []*FileChunk{
		{Path: "a.go", Content: "package a", Tokens: 100},
		{Path: "b.go", Content: "package b", Tokens: 50},
		{Path: "missing.go", Content: "package m", Tokens: 10},
	}
	u := j.Pass2Batch(context.Background(), "bp", chunks)
	if u.totalPrompt() != 100 {
		t.Fatalf("usage not propagated")
	}
	if chunks[0].Multiplier != 10 || chunks[0].Category != "glue" {
		t.Fatalf("a.go not scored: %+v", chunks[0])
	}
	if chunks[2].Err == nil {
		t.Fatalf("missing.go should carry an error")
	}
	// Verify pass: call 1 gave m=10; calls 2,3 give 20,30 → median 20.
	j.VerifyTop(context.Background(), "bp", chunks, 2)
	if chunks[0].Multiplier != 20 {
		t.Fatalf("median wrong: got %v want 20", chunks[0].Multiplier)
	}
	if fb.calls != 3 {
		t.Fatalf("expected 3 calls, got %d", fb.calls)
	}
}

func TestMedian(t *testing.T) {
	if median([]float64{30, 10, 20}) != 20 {
		t.Fatal("odd median")
	}
	if median([]float64{10, 30}) != 20 {
		t.Fatal("even median")
	}
	if median([]float64{7}) != 7 {
		t.Fatal("single median")
	}
}

func TestBadgeAndCoverage(t *testing.T) {
	cov := Coverage{TotalFiles: 780, ScoredFiles: 120, TotalLOC: 100000, ScoredLOC: 52000}
	if cov.Label() != "52% scanned" {
		t.Fatalf("label: %s", cov.Label())
	}
	full := Coverage{TotalFiles: 5, ScoredFiles: 5, TotalLOC: 10, ScoredLOC: 10}
	if full.Label() != "full scan" {
		t.Fatalf("full label: %s", full.Label())
	}
	u := badgeURL(8093820, "weeks+", cov)
	want := "https://img.shields.io/badge/aiscore-8.1M_OT_%C2%B7_weeks%2B_%C2%B7_52%25_scanned-8a2be2"
	if u != want {
		t.Fatalf("badge url:\n got %s\nwant %s", u, want)
	}
}
