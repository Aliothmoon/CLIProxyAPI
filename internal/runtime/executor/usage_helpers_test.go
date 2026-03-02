package executor

import "testing"

func TestParseOpenAIUsageChatCompletions(t *testing.T) {
	data := []byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":5}}}`)
	detail := parseOpenAIUsage(data)
	if detail.InputTokens != 1 {
		t.Fatalf("input tokens = %d, want %d", detail.InputTokens, 1)
	}
	if detail.OutputTokens != 2 {
		t.Fatalf("output tokens = %d, want %d", detail.OutputTokens, 2)
	}
	if detail.TotalTokens != 3 {
		t.Fatalf("total tokens = %d, want %d", detail.TotalTokens, 3)
	}
	if detail.CachedTokens != 4 {
		t.Fatalf("cached tokens = %d, want %d", detail.CachedTokens, 4)
	}
	if detail.ReasoningTokens != 5 {
		t.Fatalf("reasoning tokens = %d, want %d", detail.ReasoningTokens, 5)
	}
}

func TestParseOpenAIUsageResponses(t *testing.T) {
	data := []byte(`{"usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30,"input_tokens_details":{"cached_tokens":7},"output_tokens_details":{"reasoning_tokens":9}}}`)
	detail := parseOpenAIUsage(data)
	if detail.InputTokens != 10 {
		t.Fatalf("input tokens = %d, want %d", detail.InputTokens, 10)
	}
	if detail.OutputTokens != 20 {
		t.Fatalf("output tokens = %d, want %d", detail.OutputTokens, 20)
	}
	if detail.TotalTokens != 30 {
		t.Fatalf("total tokens = %d, want %d", detail.TotalTokens, 30)
	}
	if detail.CachedTokens != 7 {
		t.Fatalf("cached tokens = %d, want %d", detail.CachedTokens, 7)
	}
	if detail.ReasoningTokens != 9 {
		t.Fatalf("reasoning tokens = %d, want %d", detail.ReasoningTokens, 9)
	}
}

// ---------------------------------------------------------------------------
// parseAntigravityStreamUsage vs parseAntigravityStreamUsageOptimized
// ---------------------------------------------------------------------------

func TestParseAntigravityStreamUsage_Parity(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{name: "empty", payload: ""},
		{name: "invalid JSON", payload: "not-json"},
		{name: "no usage", payload: `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`},
		{
			name:    "response.usageMetadata",
			payload: `{"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}}`,
		},
		{
			name:    "top-level usageMetadata",
			payload: `{"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":10,"totalTokenCount":30}}`,
		},
		{
			name:    "usage_metadata snake_case",
			payload: `{"usage_metadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}`,
		},
		{
			name:    "with thoughtsTokenCount",
			payload: `{"response":{"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":30,"thoughtsTokenCount":10,"totalTokenCount":90}}}`,
		},
		{
			name:    "with cachedContentTokenCount",
			payload: `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"cachedContentTokenCount":20,"totalTokenCount":150}}`,
		},
		{
			name:    "SSE data: prefix (original handles, optimized receives pre-extracted)",
			payload: `{"response":{"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}}`,
		},
		{
			name:    "zero counts",
			payload: `{"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0,"totalTokenCount":0}}`,
		},
		{
			name:    "nested with traceId",
			payload: `{"response":{"candidates":[],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"totalTokenCount":10}},"traceId":"abc-123"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []byte(tt.payload)

			origDetail, origOK := parseAntigravityStreamUsage(input)
			optDetail, optOK := parseAntigravityStreamUsageOptimized(input)

			if origOK != optOK {
				t.Fatalf("ok mismatch: original=%v, optimized=%v", origOK, optOK)
			}
			if origDetail != optDetail {
				t.Fatalf("detail mismatch\n  original:  %+v\n  optimized: %+v", origDetail, optDetail)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

var antigravityUsagePayloads = map[string][]byte{
	"no_usage":                []byte(`{"candidates":[{"content":{"parts":[{"text":"hello world streaming chunk"}]}}]}`),
	"response_usageMetadata":  []byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"totalTokenCount":150}}}`),
	"top_level_usageMetadata": []byte(`{"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":10,"thoughtsTokenCount":5,"totalTokenCount":35}}`),
	"usage_metadata_snake":    []byte(`{"usage_metadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}`),
}

func BenchmarkParseAntigravityStreamUsage_Original(b *testing.B) {
	for name, payload := range antigravityUsagePayloads {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = parseAntigravityStreamUsage(payload)
			}
		})
	}
}

func BenchmarkParseAntigravityStreamUsage_Optimized(b *testing.B) {
	for name, payload := range antigravityUsagePayloads {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = parseAntigravityStreamUsageOptimized(payload)
			}
		})
	}
}
