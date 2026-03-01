package optimized_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	legacy "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/antigravity/claude"
	optimized "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/antigravity/claude/optimized"
)

type equivalenceCase struct {
	Name         string
	RequestRaw   []byte
	NonStreamRaw []byte
	StreamChunks [][]byte
	IncludeDone  bool
}

type sseEvent struct {
	Event string
	Data  string
}

func TestEquivalence_NonStream_LegacyVsOptimized(t *testing.T) {
	caseCount := 220
	if testing.Short() {
		caseCount = 40
	}

	cases := buildEquivalenceCases(caseCount)
	for i := range cases {
		tc := cases[i]
		t.Run(tc.Name, func(t *testing.T) {
			var legacyParam any
			var optimizedParam any

			legacyOut := legacy.ConvertAntigravityResponseToClaudeNonStream(
				context.Background(),
				"",
				tc.RequestRaw,
				tc.RequestRaw,
				tc.NonStreamRaw,
				&legacyParam,
			)
			optimizedOut := optimized.ConvertAntigravityResponseToClaudeNonStream(
				context.Background(),
				"",
				tc.RequestRaw,
				tc.RequestRaw,
				tc.NonStreamRaw,
				&optimizedParam,
			)

			legacyCanonical, err := canonicalizeJSON(legacyOut)
			if err != nil {
				t.Fatalf("legacy output is not valid json: %v\nraw=%s", err, legacyOut)
			}
			optimizedCanonical, err := canonicalizeJSON(optimizedOut)
			if err != nil {
				t.Fatalf("optimized output is not valid json: %v\nraw=%s", err, optimizedOut)
			}

			if legacyCanonical != optimizedCanonical {
				t.Fatalf(
					"non-stream output mismatch\nlegacy=%s\noptimized=%s\nlegacy_raw=%s\noptimized_raw=%s",
					legacyCanonical,
					optimizedCanonical,
					legacyOut,
					optimizedOut,
				)
			}
		})
	}
}

func TestEquivalence_Stream_LegacyVsOptimized(t *testing.T) {
	caseCount := 180
	if testing.Short() {
		caseCount = 35
	}

	cases := buildEquivalenceCases(caseCount)
	for i := range cases {
		tc := cases[i]
		t.Run(tc.Name, func(t *testing.T) {
			legacyOut := runLegacyStream(tc.RequestRaw, tc.StreamChunks, tc.IncludeDone)
			optimizedOut := runOptimizedStream(tc.RequestRaw, tc.StreamChunks, tc.IncludeDone)

			legacyEvents := flattenSSEEvents(legacyOut)
			optimizedEvents := flattenSSEEvents(optimizedOut)

			if len(legacyEvents) != len(optimizedEvents) {
				t.Fatalf(
					"stream event count mismatch: legacy=%d optimized=%d\nlegacy_out=%q\noptimized_out=%q",
					len(legacyEvents),
					len(optimizedEvents),
					legacyOut,
					optimizedOut,
				)
			}

			for i := range legacyEvents {
				l := legacyEvents[i]
				o := optimizedEvents[i]
				if l.Event != o.Event {
					t.Fatalf(
						"event name mismatch at idx=%d legacy=%q optimized=%q\nlegacy_data=%s\noptimized_data=%s",
						i,
						l.Event,
						o.Event,
						l.Data,
						o.Data,
					)
				}

				legacyCanonical, err := normalizeAndCanonicalizeSSEData(l.Event, l.Data)
				if err != nil {
					t.Fatalf("legacy SSE data invalid json at idx=%d event=%s: %v\nraw=%s", i, l.Event, err, l.Data)
				}
				optimizedCanonical, err := normalizeAndCanonicalizeSSEData(o.Event, o.Data)
				if err != nil {
					t.Fatalf("optimized SSE data invalid json at idx=%d event=%s: %v\nraw=%s", i, o.Event, err, o.Data)
				}

				if legacyCanonical != optimizedCanonical {
					t.Fatalf(
						"SSE data mismatch at idx=%d event=%s\nlegacy=%s\noptimized=%s\nlegacy_raw=%s\noptimized_raw=%s",
						i,
						l.Event,
						legacyCanonical,
						optimizedCanonical,
						l.Data,
						o.Data,
					)
				}
			}
		})
	}
}

func runLegacyStream(requestRaw []byte, chunks [][]byte, includeDone bool) []string {
	var out []string
	var param any
	for i := range chunks {
		out = append(out, legacy.ConvertAntigravityResponseToClaude(
			context.Background(),
			"",
			requestRaw,
			requestRaw,
			chunks[i],
			&param,
		)...)
	}
	if includeDone {
		out = append(out, legacy.ConvertAntigravityResponseToClaude(
			context.Background(),
			"",
			requestRaw,
			requestRaw,
			[]byte("[DONE]"),
			&param,
		)...)
	}
	return out
}

func runOptimizedStream(requestRaw []byte, chunks [][]byte, includeDone bool) []string {
	var out []string
	var param any
	for i := range chunks {
		out = append(out, optimized.ConvertAntigravityResponseToClaude(
			context.Background(),
			"",
			requestRaw,
			requestRaw,
			chunks[i],
			&param,
		)...)
	}
	if includeDone {
		out = append(out, optimized.ConvertAntigravityResponseToClaude(
			context.Background(),
			"",
			requestRaw,
			requestRaw,
			[]byte("[DONE]"),
			&param,
		)...)
	}
	return out
}

func flattenSSEEvents(chunks []string) []sseEvent {
	out := make([]sseEvent, 0, len(chunks)*4)
	for i := range chunks {
		out = append(out, parseSSEEvents(chunks[i])...)
	}
	return out
}

func parseSSEEvents(chunk string) []sseEvent {
	lines := strings.Split(chunk, "\n")
	events := make([]sseEvent, 0, len(lines)/3)
	currentEvent := ""
	for i := range lines {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			events = append(events, sseEvent{
				Event: currentEvent,
				Data:  strings.TrimPrefix(line, "data: "),
			})
		}
	}
	return events
}

func normalizeAndCanonicalizeSSEData(eventName, data string) (string, error) {
	var parsed any
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&parsed); err != nil {
		return "", err
	}

	// tool_use id includes timestamp/counter and is intentionally unstable.
	if eventName == "content_block_start" {
		if obj, ok := parsed.(map[string]any); ok {
			if block, ok := obj["content_block"].(map[string]any); ok {
				if blockType, ok := block["type"].(string); ok && blockType == "tool_use" {
					block["id"] = "__tool_use_id__"
				}
			}
		}
	}

	canonical, err := json.Marshal(parsed)
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

func canonicalizeJSON(s string) (string, error) {
	var parsed any
	decoder := json.NewDecoder(strings.NewReader(s))
	decoder.UseNumber()
	if err := decoder.Decode(&parsed); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(parsed)
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

func buildEquivalenceCases(n int) []equivalenceCase {
	r := rand.New(rand.NewSource(20260301))
	models := []string{
		"claude-sonnet-4-5-thinking",
		"claude-3-5-sonnet-20241022",
		"claude-opus-4-1",
	}
	finishReasons := []string{
		"",
		"STOP",
		"MAX_TOKENS",
		"UNKNOWN",
		"FINISH_REASON_UNSPECIFIED",
	}

	out := make([]equivalenceCase, 0, n)
	for i := 0; i < n; i++ {
		model := models[r.Intn(len(models))]
		requestRaw := mustMarshalJSON(map[string]any{
			"model": model,
		})

		partCount := 1 + r.Intn(12)
		parts := make([]any, 0, partCount)
		for p := 0; p < partCount; p++ {
			parts = append(parts, randomPart(r, i, p))
		}

		finishReason := finishReasons[r.Intn(len(finishReasons))]
		includeFinishReason := r.Intn(100) < 85
		includeUsage := r.Intn(100) < 80
		usage := randomUsage(r)
		cpaUsage := randomUsage(r)

		nonStreamPayload := map[string]any{
			"response": map[string]any{
				"modelVersion": model,
				"responseId":   fmt.Sprintf("resp_case_%d", i),
				"candidates": []any{
					map[string]any{
						"content": map[string]any{
							"parts": parts,
						},
					},
				},
			},
		}
		if includeFinishReason && finishReason != "" {
			nonStreamPayload["response"].(map[string]any)["candidates"].([]any)[0].(map[string]any)["finishReason"] = finishReason
		}
		if includeUsage {
			nonStreamPayload["response"].(map[string]any)["usageMetadata"] = usage
		}
		nonStreamRaw := mustMarshalJSON(nonStreamPayload)

		streamChunks := make([][]byte, 0, partCount+2)
		for start := 0; start < len(parts); {
			size := 1 + r.Intn(3)
			end := start + size
			if end > len(parts) {
				end = len(parts)
			}
			chunkParts := parts[start:end]
			chunkResponse := map[string]any{
				"candidates": []any{
					map[string]any{
						"content": map[string]any{
							"parts": chunkParts,
						},
					},
				},
			}
			if start == 0 && r.Intn(100) < 60 {
				chunkResponse["cpaUsageMetadata"] = cpaUsage
			}
			if end == len(parts) {
				if includeUsage {
					chunkResponse["usageMetadata"] = usage
				}
				if includeFinishReason && finishReason != "" {
					chunkResponse["candidates"].([]any)[0].(map[string]any)["finishReason"] = finishReason
				}
			}

			streamChunks = append(streamChunks, mustMarshalJSON(map[string]any{
				"response": chunkResponse,
			}))
			start = end
		}

		out = append(out, equivalenceCase{
			Name:         fmt.Sprintf("case_%03d", i),
			RequestRaw:   requestRaw,
			NonStreamRaw: nonStreamRaw,
			StreamChunks: streamChunks,
			IncludeDone:  true,
		})
	}
	return out
}

func randomPart(r *rand.Rand, caseIndex, partIndex int) map[string]any {
	switch r.Intn(8) {
	case 0:
		return map[string]any{
			"text": randomText(caseIndex, partIndex),
		}
	case 1:
		return map[string]any{
			"text":    randomText(caseIndex, partIndex),
			"thought": true,
		}
	case 2:
		return map[string]any{
			"text":             "",
			"thought":          true,
			"thoughtSignature": randomSignature(caseIndex, partIndex),
		}
	case 3:
		return map[string]any{
			"text":              "",
			"thought":           true,
			"thought_signature": randomSignature(caseIndex, partIndex),
		}
	case 4:
		return map[string]any{
			"functionCall": map[string]any{
				"name": fmt.Sprintf("fn_%d_%d", caseIndex, partIndex),
				"args": randomFunctionArgs(r, caseIndex, partIndex),
			},
		}
	case 5:
		return map[string]any{
			"text": "",
		}
	case 6:
		return map[string]any{
			"text":    "",
			"thought": true,
		}
	default:
		return map[string]any{
			"text": fmt.Sprintf("plain-%d-%d", caseIndex, partIndex),
		}
	}
}

func randomFunctionArgs(r *rand.Rand, caseIndex, partIndex int) any {
	switch r.Intn(6) {
	case 0:
		return map[string]any{
			"a": caseIndex,
			"b": partIndex,
			"s": randomText(caseIndex, partIndex),
		}
	case 1:
		return map[string]any{}
	case 2:
		return []any{"x", caseIndex, partIndex}
	case 3:
		return fmt.Sprintf("arg-string-%d-%d", caseIndex, partIndex)
	case 4:
		return nil
	default:
		return map[string]any{
			"nested": map[string]any{
				"ok":   true,
				"code": caseIndex + partIndex,
			},
		}
	}
}

func randomText(caseIndex, partIndex int) string {
	switch (caseIndex + partIndex) % 5 {
	case 0:
		return fmt.Sprintf("simple-%d-%d", caseIndex, partIndex)
	case 1:
		return fmt.Sprintf("line-%d-%d\nnext-line", caseIndex, partIndex)
	case 2:
		return fmt.Sprintf("quote-%d-%d \"x\" \\\\ slash", caseIndex, partIndex)
	case 3:
		return fmt.Sprintf("tab-%d-%d\tsep", caseIndex, partIndex)
	default:
		return fmt.Sprintf("mix-%d-%d\\n\"q\"\\tend", caseIndex, partIndex)
	}
}

func randomSignature(caseIndex, partIndex int) string {
	seed := fmt.Sprintf("sig_%d_%d_", caseIndex, partIndex)
	for len(seed) < 64 {
		seed += "abcdef0123456789"
	}
	return seed[:64]
}

func randomUsage(r *rand.Rand) map[string]any {
	prompt := int64(r.Intn(600))
	candidates := int64(r.Intn(600))
	thoughts := int64(r.Intn(320))
	total := prompt + candidates + thoughts
	if r.Intn(4) == 0 {
		candidates = 0
		total = prompt + thoughts + int64(r.Intn(80))
	}
	cached := int64(0)
	if prompt > 0 {
		cached = int64(r.Intn(int(prompt) + 1))
	}
	return map[string]any{
		"promptTokenCount":        prompt,
		"candidatesTokenCount":    candidates,
		"thoughtsTokenCount":      thoughts,
		"totalTokenCount":         total,
		"cachedContentTokenCount": cached,
	}
}

func mustMarshalJSON(v any) []byte {
	b, err := sonic.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
