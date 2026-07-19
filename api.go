package main

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

// apiModel is the forced reference instrument — the OT unit is defined relative to it.
const apiModel = anthropic.ModelClaudeOpus4_8

// apiBackend talks to the Anthropic API directly (per-token billing).
type apiBackend struct{ client anthropic.Client }

func newAPIBackend() *apiBackend { return &apiBackend{client: anthropic.NewClient()} }

func (b *apiBackend) Name() string      { return "api (claude-opus-4-8)" }
func (b *apiBackend) CanPreCount() bool { return true }

// CountTokens uses Anthropic's tokenizer (free, exact).
func (b *apiBackend) CountTokens(ctx context.Context, text string) (int64, error) {
	ct, err := b.client.Messages.CountTokens(ctx, anthropic.MessageCountTokensParams{
		Model:    apiModel,
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(text))},
	})
	if err != nil {
		return 0, err
	}
	return ct.InputTokens, nil
}

func (b *apiBackend) Score(ctx context.Context, system, user string, schema Schema, maxTokens int64, opts ScoreOpts) (string, Usage, error) {
	props, _ := schema.Object["properties"].(map[string]any)
	required, _ := schema.Object["required"].([]string)
	tool := anthropic.ToolParam{
		Name:        schema.Name,
		Description: anthropic.String(schema.Description),
		// strict guarantees the input validates against the full schema (required
		// fields included) — without it a missing "multiplier" unmarshals to 0.
		Strict: anthropic.Bool(true),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties:  props,
			Required:    required,
			ExtraFields: map[string]any{"additionalProperties": false},
		},
	}

	// Forced tool_choice is incompatible with thinking, so the tool call is
	// elicited by prompt instead; a missing tool_use block errors below.
	cache := anthropic.NewCacheControlEphemeralParam()
	if opts.LongCache {
		cache = anthropic.CacheControlEphemeralParam{TTL: anthropic.CacheControlEphemeralTTLTTL1h}
	}
	params := anthropic.MessageNewParams{
		Model:     apiModel,
		MaxTokens: maxTokens,
		// rubric + (repo|blueprint) as a single cached prefix. NOTE: Opus 4.8's
		// minimum cacheable prefix is 4096 tokens — the small Pass-2 prefix
		// (rubric + blueprint) may silently not cache; only Pass 1 reliably does.
		System:   []anthropic.TextBlockParam{{Text: system, CacheControl: cache}},
		Thinking: anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}},
		Tools:    []anthropic.ToolUnionParam{{OfTool: &tool}},
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(user))},
	}
	if opts.Effort != "" {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(opts.Effort)}
	}

	// Stream + accumulate: thinking turns over a large repo can outlive the
	// non-streaming HTTP timeout.
	stream := b.client.Messages.NewStreaming(ctx, params)
	var msg anthropic.Message
	for stream.Next() {
		if err := msg.Accumulate(stream.Current()); err != nil {
			return "", Usage{}, err
		}
	}
	if err := stream.Err(); err != nil {
		return "", Usage{}, err
	}
	u := Usage{
		Input:      msg.Usage.InputTokens,
		Output:     msg.Usage.OutputTokens,
		CacheRead:  msg.Usage.CacheReadInputTokens,
		CacheWrite: msg.Usage.CacheCreationInputTokens,
	}
	for _, block := range msg.Content {
		if tu, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
			// .Input, not .JSON.Input.Raw(): accumulated messages fill the field
			// directly from input_json_delta events; the raw-JSON view is empty.
			return string(tu.Input), u, nil
		}
	}
	return "", u, fmt.Errorf("model returned no tool_use block")
}
