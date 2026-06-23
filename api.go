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

func (b *apiBackend) Name() string     { return "api (claude-opus-4-8)" }
func (b *apiBackend) CanPreCount() bool { return true }

// CountTokens uses Anthropic's tokenizer (free, exact). VERIFY: CountTokens
// params/return against your installed SDK version.
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

func (b *apiBackend) Score(ctx context.Context, system, user string, schema Schema, maxTokens int64) (string, Usage, error) {
	props, _ := schema.Object["properties"].(map[string]any)
	tool := anthropic.ToolParam{
		Name:        schema.Name,
		Description: anthropic.String(schema.Description),
		InputSchema: anthropic.ToolInputSchemaParam{Properties: props},
	}
	resp, err := b.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     apiModel,
		MaxTokens: maxTokens,
		// rubric + (repo|blueprint) as a single cached prefix — repeated calls read it at ~0.1x.
		System:   []anthropic.TextBlockParam{{Text: system, CacheControl: anthropic.NewCacheControlEphemeralParam()}},
		Thinking: anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}},
		Tools:    []anthropic.ToolUnionParam{{OfTool: &tool}},
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(user))},
	})
	if err != nil {
		return "", Usage{}, err
	}
	// VERIFY: usage field names against your SDK version.
	u := Usage{
		Input:      resp.Usage.InputTokens,
		Output:     resp.Usage.OutputTokens,
		CacheRead:  resp.Usage.CacheReadInputTokens,
		CacheWrite: resp.Usage.CacheCreationInputTokens,
	}
	for _, block := range resp.Content {
		if tu, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
			// VERIFY: raw tool-input accessor against your SDK version.
			return tu.JSON.Input.Raw(), u, nil
		}
	}
	return "", u, fmt.Errorf("model returned no tool_use block")
}
