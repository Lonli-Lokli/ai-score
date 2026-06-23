package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// cliBackend drives the locally-installed Claude Code in headless print mode.
// It uses whatever `claude` is logged in with (subscription OAuth or API key).
// NOTE: we intentionally do NOT pass --bare, which would force ANTHROPIC_API_KEY
// auth and ignore the subscription login.
type cliBackend struct{ model string }

func newCLIBackend() *cliBackend { return &cliBackend{model: "opus"} }

func (b *cliBackend) Name() string     { return "cli (claude -p, model=" + b.model + ")" }
func (b *cliBackend) CanPreCount() bool { return false } // no pre-flight count via the CLI

func (b *cliBackend) CountTokens(ctx context.Context, text string) (int64, error) {
	return 0, fmt.Errorf("cli backend cannot pre-count; tokens are derived from usage")
}

// cliEnvelope is the JSON shape from `claude -p --output-format json`.
// VERIFY field names with: claude -p "hi" --output-format json
type cliEnvelope struct {
	Result       json.RawMessage `json:"result"` // string or object (handled by unwrapResult)
	TotalCostUSD float64         `json:"total_cost_usd"`
	IsError      bool            `json:"is_error"`
	Usage        struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

func (b *cliBackend) Score(ctx context.Context, system, user string, schema Schema, maxTokens int64) (string, Usage, error) {
	schemaJSON, err := json.Marshal(schema.Object)
	if err != nil {
		return "", Usage{}, err
	}
	args := []string{
		"-p",
		"--model", b.model,
		"--output-format", "json",
		"--json-schema", string(schemaJSON),
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	// Rubric + blueprint + file all go through stdin as one prompt; --json-schema
	// constrains the output. (Keeps us off the system-prompt/persona flags.)
	cmd.Stdin = bytes.NewBufferString(system + "\n\n----\n\n" + user)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", Usage{}, fmt.Errorf("claude -p failed: %v: %s", err, errb.String())
	}
	var env cliEnvelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		return "", Usage{}, fmt.Errorf("parse claude envelope: %w (raw: %s)", err, out.String())
	}
	if env.IsError {
		return "", Usage{}, fmt.Errorf("claude reported an error: %s", out.String())
	}
	u := Usage{
		Input:       env.Usage.InputTokens,
		Output:      env.Usage.OutputTokens,
		CacheRead:   env.Usage.CacheReadInputTokens,
		CacheWrite:  env.Usage.CacheCreationInputTokens,
		ReportedUSD: env.TotalCostUSD,
	}
	return unwrapResult(env.Result), u, nil
}

// unwrapResult returns the structured JSON whether `result` is a JSON object or a
// JSON-encoded string containing the object.
func unwrapResult(raw json.RawMessage) string {
	s := string(bytes.TrimSpace(raw))
	if len(s) > 0 && s[0] == '"' {
		var str string
		if json.Unmarshal(raw, &str) == nil {
			return str
		}
	}
	return s
}
