package executor

import (
	"context"
	"strings"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

// ---------------------------------------------------------------------------
// FilterSSEUsageMetadata benchmarks
// ---------------------------------------------------------------------------

var ssePayloads = map[string][]byte{
	"single_line_no_usage": []byte(`data: {"candidates":[{"content":{"parts":[{"text":"hello world this is a normal streaming chunk with some text content"}]}}]}`),

	"single_line_with_usage": []byte(`data: {"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"totalTokenCount":150}}`),

	"single_line_terminal": []byte(`data: {"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":200,"totalTokenCount":300}}`),

	"multi_line": []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"chunk1\"}]}}],\"usageMetadata\":{\"promptTokenCount\":10}}\n\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"chunk2\"}]}}],\"usageMetadata\":{\"promptTokenCount\":20}}\n\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"done\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":30,\"candidatesTokenCount\":40,\"totalTokenCount\":70}}\n"),

	"antigravity_format": []byte(`data: {"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}},"traceId":"trace-abc-123"}`),
}

func BenchmarkFilterSSEUsageMetadata_Original(b *testing.B) {
	for name, payload := range ssePayloads {
		b.Run(name, func(b *testing.B) {
			input := make([]byte, len(payload))
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				copy(input, payload)
				_ = FilterSSEUsageMetadata(input)
			}
		})
	}
}

func BenchmarkFilterSSEUsageMetadata_Optimized(b *testing.B) {
	for name, payload := range ssePayloads {
		b.Run(name, func(b *testing.B) {
			input := make([]byte, len(payload))
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				copy(input, payload)
				_ = FilterSSEUsageMetadataOptimized(input)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// convertStreamToNonStream benchmarks
// ---------------------------------------------------------------------------

func buildStreamPayload(chunks int) []byte {
	var sb strings.Builder
	for i := 0; i < chunks; i++ {
		if i > 0 {
			sb.WriteByte('\n')
		}
		if i == chunks-1 {
			sb.WriteString(`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"final chunk of text content."}]},"finishReason":"STOP"}],"modelVersion":"gemini-2.5-pro","responseId":"resp-1"},"traceId":"t-1","usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"totalTokenCount":150}}`)
		} else {
			sb.WriteString(`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"streaming text chunk number "}]}}]}}`)
		}
	}
	return []byte(sb.String())
}

func buildStreamWithThoughtsPayload() []byte {
	return []byte(
		`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"thinking about the problem","thought":true}]}}]}}` + "\n" +
			`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"more reasoning here","thought":true,"thoughtSignature":"sig123"}]}}]}}` + "\n" +
			`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"The answer is 42."}]}}]}}` + "\n" +
			`{"response":{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"calculate","args":{"x":42}}}]},"finishReason":"STOP"}],"modelVersion":"v1","responseId":"r1"},"traceId":"t1","usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":30,"totalTokenCount":80}}`)
}

func buildStreamWithInlineData() []byte {
	return []byte(
		`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"Here is the image:"}]}}]}}` + "\n" +
			`{"response":{"candidates":[{"content":{"role":"model","parts":[{"inline_data":{"mime_type":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk"}}]},"finishReason":"STOP"}],"modelVersion":"v1"},"traceId":"t1"}}`)
}

var convertStreamCases = map[string][]byte{
	"5_chunks":          buildStreamPayload(5),
	"20_chunks":         buildStreamPayload(20),
	"50_chunks":         buildStreamPayload(50),
	"thoughts_and_tool": buildStreamWithThoughtsPayload(),
	"inline_data":       buildStreamWithInlineData(),
}

func BenchmarkConvertStreamToNonStream_Original(b *testing.B) {
	executor := &AntigravityExecutor{}
	for name, stream := range convertStreamCases {
		b.Run(name, func(b *testing.B) {
			input := make([]byte, len(stream))
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				copy(input, stream)
				_ = executor.convertStreamToNonStream(input)
			}
		})
	}
}

func BenchmarkConvertStreamToNonStream_Optimized(b *testing.B) {
	executor := &AntigravityExecutor{}
	for name, stream := range convertStreamCases {
		b.Run(name, func(b *testing.B) {
			input := make([]byte, len(stream))
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				copy(input, stream)
				_ = executor.convertStreamToNonStreamOptimized(input)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildRequest benchmarks
// ---------------------------------------------------------------------------

var buildRequestCases = map[string]struct {
	modelName string
	payload   []byte
	stream    bool
}{
	"gemini_simple": {
		modelName: "gemini-2.5-pro",
		payload:   []byte(`{"request":{"contents":[{"role":"user","parts":[{"text":"hello world"}]}],"generationConfig":{"maxOutputTokens":8192}}}`),
		stream:    true,
	},
	"claude_with_tools": {
		modelName: "claude-opus-4-6",
		payload: []byte(`{"request":{"contents":[{"role":"user","parts":[{"text":"hello"}]}],
			"tools":[{"function_declarations":[
				{"name":"tool_1","parametersJsonSchema":{"type":"object","properties":{"arg":{"type":"string"}}}},
				{"name":"tool_2","parametersJsonSchema":{"type":"object","properties":{"x":{"type":"number"}}}}
			]}]}}`),
		stream: true,
	},
	"claude_with_system": {
		modelName: "claude-sonnet-4-6",
		payload:   []byte(`{"request":{"systemInstruction":{"parts":[{"text":"You are a helpful assistant."}]},"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`),
		stream:    false,
	},
}

func BenchmarkBuildRequest_Original(b *testing.B) {
	for name, tc := range buildRequestCases {
		b.Run(name, func(b *testing.B) {
			executor := &AntigravityExecutor{}
			auth := &cliproxyauth.Auth{
				Metadata: map[string]any{"project_id": "bench-project"},
			}
			ctx := context.Background()
			input := make([]byte, len(tc.payload))
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				copy(input, tc.payload)
				req, err := executor.buildRequest(ctx, auth, "token", tc.modelName, input, tc.stream, "", "https://example.com")
				if err != nil {
					b.Fatal(err)
				}
				_ = req
			}
		})
	}
}

func BenchmarkBuildRequest_Optimized(b *testing.B) {
	for name, tc := range buildRequestCases {
		b.Run(name, func(b *testing.B) {
			executor := &AntigravityExecutor{}
			auth := &cliproxyauth.Auth{
				Metadata: map[string]any{"project_id": "bench-project"},
			}
			ctx := context.Background()
			input := make([]byte, len(tc.payload))
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				copy(input, tc.payload)
				req, err := executor.buildRequestOptimized(ctx, auth, "token", tc.modelName, input, tc.stream, "", "https://example.com")
				if err != nil {
					b.Fatal(err)
				}
				_ = req
			}
		})
	}
}
