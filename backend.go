package main

import "context"

// Usage is the token accounting from one model call. ReportedUSD is the
// backend's own cost figure when it provides one (Claude Code does; the raw API
// does not), otherwise 0.
type Usage struct {
	Input, Output, CacheRead, CacheWrite int64
	ReportedUSD                          float64
}

// totalPrompt is the full prompt size regardless of caching: uncached input plus
// cache reads. Used to derive a chunk's token count by differencing on backends
// that can't pre-count.
func (u Usage) totalPrompt() int64 { return u.Input + u.CacheRead }

// Schema describes the forced output shape. The API backend turns it into a tool;
// the CLI backend passes Object to `--json-schema`.
type Schema struct {
	Name        string
	Description string
	Object      map[string]any // full JSON Schema: {type, properties, required, additionalProperties}
}

// Backend is the pluggable judge: API (per-token, exact count_tokens) or local
// Claude Code CLI (uses whatever `claude` is logged in with; counts via usage).
type Backend interface {
	Name() string
	// CanPreCount reports whether CountTokens can exactly count an isolated string.
	CanPreCount() bool
	// CountTokens returns the exact token count of text. Only valid when CanPreCount.
	CountTokens(ctx context.Context, text string) (int64, error)
	// Score sends system+user constrained to schema; returns the structured JSON
	// (as a string to be unmarshalled) plus usage.
	Score(ctx context.Context, system, user string, schema Schema, maxTokens int64) (string, Usage, error)
}

// ---- cost accounting --------------------------------------------------------

// Opus 4.8 list pricing, $/1M tokens. Cache read = 0.1x input; write (5m) = 1.25x.
const (
	priceInput      = 5.00
	priceOutput     = 25.00
	priceCacheRead  = 0.50
	priceCacheWrite = 6.25
)

type Cost struct {
	Input, Output, CacheRead, CacheWrite int64
	Calls                                int
	ReportedUSD                          float64 // summed backend-reported cost (CLI)
}

func (c *Cost) add(u Usage) {
	c.Input += u.Input
	c.Output += u.Output
	c.CacheRead += u.CacheRead
	c.CacheWrite += u.CacheWrite
	c.ReportedUSD += u.ReportedUSD
	c.Calls++
}

// dollars estimates spend from token usage at Opus list prices.
func (c *Cost) dollars() float64 {
	return float64(c.Input)/1e6*priceInput +
		float64(c.Output)/1e6*priceOutput +
		float64(c.CacheRead)/1e6*priceCacheRead +
		float64(c.CacheWrite)/1e6*priceCacheWrite
}
