package optimized_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/bytedance/sonic"
	legacy "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/antigravity/claude"
	optimized "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/antigravity/claude/optimized"
)

var (
	benchmarkStringSink string
	benchmarkIntSink    int
	benchmarkCtx        = context.Background()
)

type benchmarkDataset struct {
	Name         string
	RequestRaw   []byte
	NonStreamRaw []byte
	StreamChunks [][]byte
}

func BenchmarkCompareNonStream(b *testing.B) {
	datasets := buildBenchmarkDatasets()
	for i := range datasets {
		ds := datasets[i]
		b.Run(ds.Name+"/legacy", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(ds.NonStreamRaw)))
			for i := 0; i < b.N; i++ {
				var param any
				out := legacy.ConvertAntigravityResponseToClaudeNonStream(
					benchmarkCtx,
					"",
					ds.RequestRaw,
					ds.RequestRaw,
					ds.NonStreamRaw,
					&param,
				)
				benchmarkStringSink = out
			}
		})

		b.Run(ds.Name+"/optimized", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(ds.NonStreamRaw)))
			for i := 0; i < b.N; i++ {
				var param any
				out := optimized.ConvertAntigravityResponseToClaudeNonStream(
					benchmarkCtx,
					"",
					ds.RequestRaw,
					ds.RequestRaw,
					ds.NonStreamRaw,
					&param,
				)
				benchmarkStringSink = out
			}
		})
	}
}

func BenchmarkCompareStreamSession(b *testing.B) {
	datasets := buildBenchmarkDatasets()
	for i := range datasets {
		ds := datasets[i]
		sessionInputBytes := totalBytes(ds.StreamChunks) + len("[DONE]")

		b.Run(ds.Name+"/legacy", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(sessionInputBytes))
			for i := 0; i < b.N; i++ {
				benchmarkIntSink = runLegacyStreamSession(ds.RequestRaw, ds.StreamChunks)
			}
		})

		b.Run(ds.Name+"/optimized", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(sessionInputBytes))
			for i := 0; i < b.N; i++ {
				benchmarkIntSink = runOptimizedStreamSession(ds.RequestRaw, ds.StreamChunks)
			}
		})
	}
}

func runLegacyStreamSession(requestRaw []byte, chunks [][]byte) int {
	total := 0
	var param any
	for i := range chunks {
		out := legacy.ConvertAntigravityResponseToClaude(
			benchmarkCtx,
			"",
			requestRaw,
			requestRaw,
			chunks[i],
			&param,
		)
		for j := range out {
			total += len(out[j])
		}
	}
	done := legacy.ConvertAntigravityResponseToClaude(
		benchmarkCtx,
		"",
		requestRaw,
		requestRaw,
		[]byte("[DONE]"),
		&param,
	)
	for i := range done {
		total += len(done[i])
	}
	return total
}

func runOptimizedStreamSession(requestRaw []byte, chunks [][]byte) int {
	total := 0
	var param any
	for i := range chunks {
		out := optimized.ConvertAntigravityResponseToClaude(
			benchmarkCtx,
			"",
			requestRaw,
			requestRaw,
			chunks[i],
			&param,
		)
		for j := range out {
			total += len(out[j])
		}
	}
	done := optimized.ConvertAntigravityResponseToClaude(
		benchmarkCtx,
		"",
		requestRaw,
		requestRaw,
		[]byte("[DONE]"),
		&param,
	)
	for i := range done {
		total += len(done[i])
	}
	return total
}

func buildBenchmarkDatasets() []benchmarkDataset {
	model := "claude-sonnet-4-5-thinking"
	requestRaw := mustJSON(map[string]any{
		"model": model,
	})

	smallParts := []any{
		map[string]any{"text": "hello world from benchmark"},
	}
	smallNonStream := buildNonStreamRaw("resp_small", model, "STOP", usageSpec{
		Prompt: 12, Candidates: 20, Thoughts: 3, Total: 35, Cached: 2,
	}, smallParts)
	smallChunks := [][]byte{
		buildStreamChunk(nil, "", []any{map[string]any{"text": "hello "}}),
		buildStreamChunk(nil, "", []any{map[string]any{"text": "world"}}),
		buildStreamChunk(usagePtr(usageSpec{
			Prompt: 12, Candidates: 20, Thoughts: 3, Total: 35, Cached: 2,
		}), "STOP", []any{}),
	}

	complexParts := make([]any, 0, 320)
	signature := "sig_12345678901234567890123456789012345678901234567890"
	for i := 0; i < 40; i++ {
		complexParts = append(complexParts, map[string]any{
			"text":    fmt.Sprintf("thinking-%d-a;", i),
			"thought": true,
		})
		complexParts = append(complexParts, map[string]any{
			"text":    fmt.Sprintf("thinking-%d-b;", i),
			"thought": true,
		})
		complexParts = append(complexParts, map[string]any{
			"text":             "",
			"thought":          true,
			"thoughtSignature": signature,
		})
		complexParts = append(complexParts, map[string]any{
			"text": fmt.Sprintf("text-output-%d;", i),
		})
		if i%5 == 0 {
			complexParts = append(complexParts, map[string]any{
				"functionCall": map[string]any{
					"name": fmt.Sprintf("tool_fn_%d", i),
					"args": map[string]any{
						"index": i,
						"mode":  "fast",
						"flag":  true,
					},
				},
			})
		}
	}
	complexNonStream := buildNonStreamRaw("resp_complex", model, "STOP", usageSpec{
		Prompt: 512, Candidates: 680, Thoughts: 320, Total: 1512, Cached: 128,
	}, complexParts)
	complexChunks := make([][]byte, 0, 220)
	for i := 0; i < 28; i++ {
		complexChunks = append(complexChunks,
			buildStreamChunk(nil, "", []any{map[string]any{
				"text":    fmt.Sprintf("thinking-%d-a;", i),
				"thought": true,
			}}),
			buildStreamChunk(nil, "", []any{map[string]any{
				"text":    fmt.Sprintf("thinking-%d-b;", i),
				"thought": true,
			}}),
			buildStreamChunk(nil, "", []any{map[string]any{
				"text":             "",
				"thought":          true,
				"thoughtSignature": signature,
			}}),
			buildStreamChunk(nil, "", []any{map[string]any{
				"text": fmt.Sprintf("text-output-%d;", i),
			}}),
		)
		if i%4 == 0 {
			complexChunks = append(complexChunks,
				buildStreamChunk(nil, "", []any{map[string]any{
					"functionCall": map[string]any{
						"name": fmt.Sprintf("tool_fn_%d", i),
						"args": map[string]any{
							"index": i,
							"mode":  "fast",
							"flag":  true,
						},
					},
				}}),
			)
		}
	}
	complexChunks = append(complexChunks, buildStreamChunk(usagePtr(usageSpec{
		Prompt: 512, Candidates: 680, Thoughts: 320, Total: 1512, Cached: 128,
	}), "STOP", []any{}))

	return []benchmarkDataset{
		{
			Name:         "small",
			RequestRaw:   requestRaw,
			NonStreamRaw: smallNonStream,
			StreamChunks: smallChunks,
		},
		{
			Name:         "complex_mixed",
			RequestRaw:   requestRaw,
			NonStreamRaw: complexNonStream,
			StreamChunks: complexChunks,
		},
	}
}

type usageSpec struct {
	Prompt     int64
	Candidates int64
	Thoughts   int64
	Total      int64
	Cached     int64
}

func usagePtr(v usageSpec) *usageSpec {
	return &v
}

func buildNonStreamRaw(responseID, model, finishReason string, usage usageSpec, parts []any) []byte {
	return mustJSON(map[string]any{
		"response": map[string]any{
			"responseId":   responseID,
			"modelVersion": model,
			"usageMetadata": map[string]any{
				"promptTokenCount":        usage.Prompt,
				"candidatesTokenCount":    usage.Candidates,
				"thoughtsTokenCount":      usage.Thoughts,
				"totalTokenCount":         usage.Total,
				"cachedContentTokenCount": usage.Cached,
			},
			"candidates": []any{
				map[string]any{
					"finishReason": finishReason,
					"content": map[string]any{
						"parts": parts,
					},
				},
			},
		},
	})
}

func buildStreamChunk(usage *usageSpec, finishReason string, parts []any) []byte {
	candidate := map[string]any{
		"content": map[string]any{
			"parts": parts,
		},
	}
	if finishReason != "" {
		candidate["finishReason"] = finishReason
	}

	response := map[string]any{
		"candidates": []any{candidate},
	}
	if usage != nil {
		response["usageMetadata"] = map[string]any{
			"promptTokenCount":        usage.Prompt,
			"candidatesTokenCount":    usage.Candidates,
			"thoughtsTokenCount":      usage.Thoughts,
			"totalTokenCount":         usage.Total,
			"cachedContentTokenCount": usage.Cached,
		}
	}

	return mustJSON(map[string]any{
		"response": response,
	})
}

func totalBytes(items [][]byte) int {
	total := 0
	for i := range items {
		total += len(items[i])
	}
	return total
}

func mustJSON(v any) []byte {
	b, err := sonic.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
