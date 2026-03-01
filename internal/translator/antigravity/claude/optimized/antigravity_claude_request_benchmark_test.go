package optimized_test

import (
	"fmt"
	"testing"

	legacy "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/antigravity/claude"
	optimized "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/antigravity/claude/optimized"
)

type requestBenchmarkCase struct {
	Name     string
	Model    string
	InputRaw []byte
}

func BenchmarkConvertClaudeRequestToAntigravity_Legacy(b *testing.B) {
	runRequestBenchmarkSuite(b, "legacy", legacy.ConvertClaudeRequestToAntigravity)
}

func BenchmarkConvertClaudeRequestToAntigravity_Optimized(b *testing.B) {
	runRequestBenchmarkSuite(b, "optimized", optimized.ConvertClaudeRequestToAntigravity)
}

func runRequestBenchmarkSuite(
	b *testing.B,
	label string,
	convertFn func(modelName string, inputRawJSON []byte, stream bool) []byte,
) {
	cases := buildRequestBenchmarkCases()
	for i := range cases {
		tc := cases[i]
		b.Run(fmt.Sprintf("%s_%s", label, tc.Name), func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				_ = convertFn(tc.Model, tc.InputRaw, false)
			}
		})
	}
}

func buildRequestBenchmarkCases() []requestBenchmarkCase {
	return []requestBenchmarkCase{
		{
			Name:     "small",
			Model:    "claude-sonnet-4-5-thinking",
			InputRaw: makeRequestBenchmarkPayload(6, 1),
		},
		{
			Name:     "medium",
			Model:    "claude-sonnet-4-5-thinking",
			InputRaw: makeRequestBenchmarkPayload(24, 3),
		},
		{
			Name:     "large",
			Model:    "claude-sonnet-4-5-thinking",
			InputRaw: makeRequestBenchmarkPayload(80, 6),
		},
	}
}

func makeRequestBenchmarkPayload(messageCount, toolCount int) []byte {
	messages := make([]any, 0, messageCount)
	for i := 0; i < messageCount; i++ {
		role := "user"
		content := []any{
			map[string]any{
				"type": "text",
				"text": fmt.Sprintf("prompt-%d", i),
			},
		}

		if i%2 == 1 {
			role = "assistant"
			content = append(content,
				map[string]any{
					"type":      "thinking",
					"thinking":  fmt.Sprintf("thinking-%d", i),
					"signature": fmt.Sprintf("claude#%s", validSignature(i, i, i, "bench")),
				},
				map[string]any{
					"type": "tool_use",
					"id":   fmt.Sprintf("call_%d", i),
					"name": "lookup",
					"input": map[string]any{
						"index": i,
						"text":  fmt.Sprintf("arg-%d", i),
					},
				},
			)
		}

		if i%3 == 0 {
			content = append(content, map[string]any{
				"type":        "tool_result",
				"tool_use_id": fmt.Sprintf("lookup-%d-%d", i, i+1),
				"content": []any{
					map[string]any{"type": "text", "text": fmt.Sprintf("result-%d", i)},
					map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": "image/png",
							"data":       "AAAABBBBCCCCDDDD",
						},
					},
				},
			})
		}

		messages = append(messages, map[string]any{
			"role":    role,
			"content": content,
		})
	}

	tools := make([]any, 0, toolCount)
	for i := 0; i < toolCount; i++ {
		tools = append(tools, map[string]any{
			"name":        fmt.Sprintf("tool_%d", i),
			"description": fmt.Sprintf("tool description %d", i),
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type": "string",
					},
					"count": map[string]any{
						"type": "number",
					},
				},
				"required": []any{"id"},
			},
		})
	}

	payload := map[string]any{
		"model": "benchmark-input-model",
		"system": []any{
			map[string]any{"type": "text", "text": "You are a benchmark test prompt."},
		},
		"thinking": map[string]any{
			"type":          "enabled",
			"budget_tokens": 1024,
		},
		"messages":    messages,
		"tools":       tools,
		"temperature": 0.4,
		"top_p":       0.95,
		"top_k":       32,
		"max_tokens":  2048,
	}
	return mustMarshalJSON(payload)
}
