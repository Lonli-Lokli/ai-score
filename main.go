// aiscore-spike — validates the two-pass OpusTokens (OT) scoring core on one repo.
//
//	Substance OT = Implementation OT + Design OT
//	  Implementation OT = Σ tokens(file) × m(file)   (Pass 2, per file)
//	  Design OT         = cost to invent the architecture (Pass 1, median-of-N)
//
// Token counts always come from Anthropic (never estimated here):
//   - backend=api : count_tokens (exact, free)
//   - backend=cli : derived from the real usage Claude Code reports (prefix differencing)
//
// Usage:
//   go build && ./aiscore-spike -path <repo> -backend api   # needs ANTHROPIC_API_KEY
//   go build && ./aiscore-spike -path <repo> -backend cli   # uses local `claude` login
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
)

func main() {
	path := flag.String("path", ".", "repo directory to score")
	n := flag.Int("n", 3, "median-of-N samples for Design OT")
	maxFiles := flag.Int("max-files", 40, "max files to score; 0 = all (cost guard)")
	backendName := flag.String("backend", "api", "judge backend: api | cli")
	flag.Parse()

	ctx := context.Background()

	var be Backend
	switch *backendName {
	case "api":
		if os.Getenv("ANTHROPIC_API_KEY") == "" {
			die("backend=api requires ANTHROPIC_API_KEY (or use -backend cli)")
		}
		be = newAPIBackend()
	case "cli":
		be = newCLIBackend()
	default:
		die("unknown -backend %q (want: api | cli)", *backendName)
	}
	j := NewJudge(be)
	fmt.Printf("Backend: %s\n", be.Name())

	// 1. Ingest -------------------------------------------------------------
	chunks, err := ingest(*path)
	if err != nil {
		die("ingest: %v", err)
	}
	if len(chunks) == 0 {
		die("no scorable source files under %s", *path)
	}
	if *maxFiles > 0 && len(chunks) > *maxFiles {
		fmt.Printf("Found %d source files; scoring the first %d (use -max-files 0 to score all)\n", len(chunks), *maxFiles)
		chunks = chunks[:*maxFiles]
	} else {
		fmt.Printf("Ingested %d files from %s\n", len(chunks), *path)
	}

	// 2. Pre-count tokens when the backend supports it (api) ----------------
	if be.CanPreCount() {
		for _, c := range chunks {
			t, err := be.CountTokens(ctx, c.Content)
			if err != nil {
				die("count_tokens: %v", err)
			}
			c.Tokens = t
		}
	}

	// 3. Pass 1 ×N → median Design OT + its blueprint -----------------------
	repo := repoBlob(chunks)
	reports := make([]ArchReport, 0, *n)
	for i := 0; i < *n; i++ {
		r, err := j.Pass1(ctx, repo)
		if err != nil {
			die("pass1: %v", err)
		}
		fmt.Printf("  Pass1 sample %d/%d: design_ot = %d\n", i+1, *n, r.DesignOT)
		reports = append(reports, r)
	}
	sort.Slice(reports, func(a, b int) bool { return reports[a].DesignOT < reports[b].DesignOT })
	median := reports[len(reports)/2]

	// 4. Pass 2 per file. On no-pre-count backends, derive tokens from usage
	//    via a one-time prefix baseline (real counts, differenced — not estimated).
	var baseline int64
	if !be.CanPreCount() {
		probe := &FileChunk{Path: "(baseline)", Content: ""}
		bu := j.Pass2(ctx, median.ArchitectureMap, probe)
		baseline = bu.totalPrompt()
	}
	for _, c := range chunks {
		u := j.Pass2(ctx, median.ArchitectureMap, c)
		if c.Err == nil && !be.CanPreCount() {
			if t := u.totalPrompt() - baseline; t > 0 {
				c.Tokens = t
			} else {
				c.Tokens = 1
			}
		}
	}

	// 5. Aggregate + report -------------------------------------------------
	report(be.Name(), chunks, median, reports, &j.cost)
}

func report(backend string, chunks []*FileChunk, median ArchReport, samples []ArchReport, cost *Cost) {
	var implOT float64
	fmt.Printf("\n%-44s %8s %5s  %-15s %s\n", "FILE", "TOKENS", "m", "CATEGORY", "IMPL_OT")
	fmt.Println(repeat("-", 92))
	for _, c := range chunks {
		if c.Err != nil {
			fmt.Printf("%-44s  ERROR: %v\n", truncate(c.Path, 44), c.Err)
			continue
		}
		ot := float64(c.Tokens) * c.Multiplier
		implOT += ot
		fmt.Printf("%-44s %8d %5.0f  %-15s %.0f\n", truncate(c.Path, 44), c.Tokens, c.Multiplier, c.Category, ot)
	}

	designOT := float64(median.DesignOT)
	total := implOT + designOT

	fmt.Println(repeat("-", 92))
	fmt.Printf("\nProject: %s\n", median.Summary)
	fmt.Printf("\nDesign OT samples: %v  → median %.0f\n", designOTs(samples), designOT)
	fmt.Printf("Design reasoning: %s\n", median.DesignReasoning)
	fmt.Printf("\n  Implementation OT : %12.0f\n", implOT)
	fmt.Printf("  Design OT         : %12.0f\n", designOT)
	fmt.Printf("  ---------------------------------\n")
	fmt.Printf("  SUBSTANCE OT      : %12.0f  OT@opus-4.8\n", total)
	fmt.Printf("  Effort tier       : %s\n", tier(int64(total)))

	fmt.Printf("\nBackend: %s | %d calls | in %d  out %d  cacheRead %d  cacheWrite %d\n",
		backend, cost.Calls, cost.Input, cost.Output, cost.CacheRead, cost.CacheWrite)
	fmt.Printf("Estimated cost (Opus list prices): $%.3f\n", cost.dollars())
	if cost.ReportedUSD > 0 {
		fmt.Printf("Claude-reported cost:              $%.3f\n", cost.ReportedUSD)
	}
}

// tier maps OT → a human label. PLACEHOLDER thresholds — calibrate against a corpus.
func tier(ot int64) string {
	switch {
	case ot < 50_000:
		return "AI-trivial (minutes)"
	case ot < 250_000:
		return "hours"
	case ot < 1_000_000:
		return "days"
	default:
		return "weeks+"
	}
}

func designOTs(rs []ArchReport) []int64 {
	out := make([]int64, len(rs))
	for i, r := range rs {
		out[i] = r.DesignOT
	}
	return out
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n+1:]
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
