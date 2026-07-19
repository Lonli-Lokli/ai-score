// aiscore-spike — validates the two-pass OpusTokens (OT) scoring core on one repo.
//
//	Substance OT = Implementation OT + Design OT
//	  Implementation OT = Σ tokens(file) × m(file)   (Pass 2, batched per call)
//	  Design OT         = cost to invent the architecture (Pass 1, median-of-N)
//
// Token counts always come from Anthropic (never estimated here):
//   - backend=api : count_tokens (exact, free)
//   - backend=cli : derived from the real usage Claude Code reports (prefix
//     differencing, apportioned across a batch by byte share)
//
// Usage:
//   go build && ./aiscore-spike -path <repo> -backend api   # needs ANTHROPIC_API_KEY
//   go build && ./aiscore-spike -path <repo> -backend cli   # uses local `claude` login
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
)

func main() {
	path := flag.String("path", ".", "repo directory to score")
	n := flag.Int("n", 3, "median-of-N samples for Design OT")
	maxFiles := flag.Int("max-files", 40, "max files to score; 0 = all (cost guard)")
	batchSize := flag.Int("batch", 8, "files scored per Pass-2 call")
	verifyTop := flag.Int("verify-top", 5, "re-score the K token-heaviest files twice more, median multiplier; 0 = off")
	backendName := flag.String("backend", "api", "judge backend: api | cli")
	flag.Parse()
	if *batchSize < 1 {
		*batchSize = 1
	}

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
	if !be.CanPreCount() {
		fmt.Println("note: the cli backend is a convenience/demo mode — per-file token counts are")
		fmt.Println("      approximate, prompt caching across invocations is unreliable, and every")
		fmt.Println("      call draws from your Claude subscription. Use -backend api to compare projects.")
	}

	// 1. Ingest -------------------------------------------------------------
	chunks, err := ingest(*path)
	if err != nil {
		die("ingest: %v", err)
	}
	if len(chunks) == 0 {
		die("no scorable source files under %s", *path)
	}
	// Largest files first: they dominate Σ tokens×m, so if -max-files truncates,
	// it keeps the files that carry the score instead of the alphabetically first.
	sort.SliceStable(chunks, func(a, b int) bool {
		if len(chunks[a].Content) != len(chunks[b].Content) {
			return len(chunks[a].Content) > len(chunks[b].Content)
		}
		return chunks[a].Path < chunks[b].Path
	})
	cov := Coverage{TotalFiles: len(chunks), TotalLOC: locTotal(chunks)}
	if *maxFiles > 0 && len(chunks) > *maxFiles {
		fmt.Printf("WARNING: found %d source files; scoring the %d largest. The score is a\n", len(chunks), *maxFiles)
		fmt.Printf("         lower bound — use -max-files 0 before comparing against another project.\n")
		chunks = chunks[:*maxFiles]
	} else {
		fmt.Printf("Ingested %d files from %s\n", len(chunks), *path)
	}
	cov.ScoredFiles = len(chunks)
	cov.ScoredLOC = locTotal(chunks)

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

	// 4. Pass 2 in batches. On no-pre-count backends, derive tokens from usage
	//    via a one-time prefix baseline, then apportion each batch's prompt
	//    tokens across its files by byte share (real counts, differenced).
	var baseline int64
	if !be.CanPreCount() {
		probe := &FileChunk{Path: "(baseline)", Content: ""}
		bu := j.Pass2Batch(ctx, median.ArchitectureMap, []*FileChunk{probe})
		baseline = bu.totalPrompt()
	}
	for start := 0; start < len(chunks); start += *batchSize {
		end := start + *batchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		batch := chunks[start:end]
		u := j.Pass2Batch(ctx, median.ArchitectureMap, batch)
		fmt.Printf("  Pass2 batch %d-%d/%d scored\n", start+1, end, len(chunks))
		if !be.CanPreCount() {
			batchTokens := u.totalPrompt() - baseline
			if batchTokens < 1 {
				batchTokens = 1
			}
			var totalBytes int64
			for _, c := range batch {
				totalBytes += int64(len(c.Content))
			}
			for _, c := range batch {
				if c.Err != nil {
					continue
				}
				if totalBytes > 0 {
					c.Tokens = batchTokens * int64(len(c.Content)) / totalBytes
				}
				if c.Tokens < 1 {
					c.Tokens = 1
				}
			}
		}
	}

	// 5. Median-of-3 multipliers for the files that dominate the score ------
	if *verifyTop > 0 {
		fmt.Printf("  Verifying multipliers on the %d token-heaviest files (median of 3)\n", *verifyTop)
		j.VerifyTop(ctx, median.ArchitectureMap, chunks, *verifyTop)
	}

	// 6. Aggregate + report -------------------------------------------------
	report(be.Name(), chunks, median, reports, &j.cost, cov)
}

// Coverage records how much of the ingestable source was actually scored, so a
// partial scan is always visible in the verdict and on the badge.
type Coverage struct {
	TotalFiles, ScoredFiles int
	TotalLOC, ScoredLOC     int64
}

// Pct is the scored share of ingestable lines of code, 0-100.
func (c Coverage) Pct() int {
	if c.TotalLOC == 0 {
		return 0
	}
	return int(100 * c.ScoredLOC / c.TotalLOC)
}

// Label is the human/badge form: "full scan" or "NN% scanned".
func (c Coverage) Label() string {
	if c.ScoredFiles == c.TotalFiles {
		return "full scan"
	}
	return fmt.Sprintf("%d%% scanned", c.Pct())
}

func locTotal(chunks []*FileChunk) int64 {
	var n int64
	for _, c := range chunks {
		n += int64(strings.Count(c.Content, "\n") + 1)
	}
	return n
}

func report(backend string, chunks []*FileChunk, median ArchReport, samples []ArchReport, cost *Cost, cov Coverage) {
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
	fmt.Printf("  Coverage          : %s (%d/%d files, %d/%d LOC)\n",
		cov.Label(), cov.ScoredFiles, cov.TotalFiles, cov.ScoredLOC, cov.TotalLOC)
	if cov.ScoredFiles < cov.TotalFiles {
		fmt.Printf("                      partial scan — the score is a LOWER BOUND\n")
	}

	fmt.Printf("\nBadge (paste into your README):\n")
	fmt.Printf("  [![aiscore](%s)](https://github.com/Lonli-Lokli/ai-score)\n", badgeURL(total, tier(int64(total)), cov))

	fmt.Printf("\nBackend: %s | %d calls | in %d  out %d  cacheRead %d  cacheWrite %d\n",
		backend, cost.Calls, cost.Input, cost.Output, cost.CacheRead, cost.CacheWrite)
	fmt.Printf("Estimated cost (Opus list prices): $%.3f\n", cost.dollars())
	if cost.ReportedUSD > 0 {
		fmt.Printf("Claude-reported cost:              $%.3f\n", cost.ReportedUSD)
	}
}

// badgeURL renders the verdict as a shields.io static badge so every scored
// repo shows the same format, coverage included.
func badgeURL(total float64, tierLabel string, cov Coverage) string {
	msg := fmt.Sprintf("%s OT · %s · %s", compactOT(total), tierLabel, cov.Label())
	return "https://img.shields.io/badge/aiscore-" + shieldsEscape(msg) + "-8a2be2"
}

// compactOT formats an OT total the way it should read on a badge: 8.1M, 240k.
func compactOT(v float64) string {
	switch {
	case v >= 1e6:
		return fmt.Sprintf("%.1fM", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.0fk", v/1e3)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

// shieldsEscape encodes a message for a shields.io path segment: literal dashes
// and underscores double, spaces become underscores, the rest is URL-escaped.
func shieldsEscape(s string) string {
	s = strings.ReplaceAll(s, "-", "--")
	s = strings.ReplaceAll(s, "_", "__")
	s = strings.ReplaceAll(s, " ", "_")
	// PathEscape leaves "+" literal, but badge proxies may decode it as a space.
	return strings.ReplaceAll(url.PathEscape(s), "+", "%2B")
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
