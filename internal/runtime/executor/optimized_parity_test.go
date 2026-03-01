package executor

import (
	"bytes"
	"context"
	"io"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ---------------------------------------------------------------------------
// FilterSSEUsageMetadata vs FilterSSEUsageMetadataOptimized
// ---------------------------------------------------------------------------

func TestFilterSSEUsageMetadata_Parity(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "empty",
			payload: "",
		},
		{
			name:    "single line no usage",
			payload: `data: {"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`,
		},
		{
			name:    "single line with usage non-terminal",
			payload: `data: {"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`,
		},
		{
			name:    "single line terminal with finishReason",
			payload: `data: {"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"totalTokenCount":30}}`,
		},
		{
			name:    "single line response-nested usage non-terminal",
			payload: `data: {"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"response.usageMetadata":{"promptTokenCount":5}},"traceId":"abc"}`,
		},
		{
			name:    "single line response-nested format",
			payload: `data: {"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":5}}}`,
		},
		{
			name: "multi-line mixed",
			payload: "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"a\"}]}}],\"usageMetadata\":{\"promptTokenCount\":1}}\n" +
				"\n" +
				"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"b\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":3,\"totalTokenCount\":5}}\n",
		},
		{
			name:    "raw JSON no data prefix with usage",
			payload: `{"candidates":[{"content":{"parts":[{"text":"raw"}]}}],"usageMetadata":{"promptTokenCount":10}}`,
		},
		{
			name:    "raw JSON no data prefix terminal",
			payload: `{"candidates":[{"content":{"parts":[{"text":"raw"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10}}`,
		},
		{
			name:    "invalid JSON",
			payload: `data: not-json-at-all`,
		},
		{
			name:    "event line only",
			payload: `event: message`,
		},
		{
			name:    "empty data",
			payload: `data: `,
		},
		{
			name:    "DONE marker",
			payload: `data: [DONE]`,
		},
		{
			name: "multi-line with event and data",
			payload: "event: message\n" +
				"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"x\"}]}}],\"usageMetadata\":{\"promptTokenCount\":1}}\n" +
				"\n",
		},
		{
			name:    "antigravity format response.usageMetadata",
			payload: `data: {"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":5}},"traceId":"t1"}`,
		},
		{
			name:    "antigravity format terminal",
			payload: `data: {"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5}},"traceId":"t2"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []byte(tt.payload)
			// Clear any traceId state between tests
			stopChunkWithoutUsage.Range(func(key, _ any) bool {
				stopChunkWithoutUsage.Delete(key)
				return true
			})

			original := FilterSSEUsageMetadata(append([]byte{}, input...))

			// Clear state again for optimized version
			stopChunkWithoutUsage.Range(func(key, _ any) bool {
				stopChunkWithoutUsage.Delete(key)
				return true
			})

			optimized := FilterSSEUsageMetadataOptimized(append([]byte{}, input...))

			if !bytes.Equal(original, optimized) {
				t.Errorf("output mismatch\n  original:  %s\n  optimized: %s", string(original), string(optimized))
			}
		})
	}
}

// Test the stop-chunk-without-usage + deferred-usage-only-chunk sequence.
// This exercises the traceId state machine in both versions.
func TestFilterSSEUsageMetadata_Parity_StopThenUsage(t *testing.T) {
	stopChunk := []byte(`data: {"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}],"traceId":"trace-1"}`)
	usageChunk := []byte(`data: {"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"totalTokenCount":30},"traceId":"trace-1"}`)

	// --- original ---
	stopChunkWithoutUsage.Range(func(key, _ any) bool {
		stopChunkWithoutUsage.Delete(key)
		return true
	})
	origStop := FilterSSEUsageMetadata(append([]byte{}, stopChunk...))
	origUsage := FilterSSEUsageMetadata(append([]byte{}, usageChunk...))

	// --- optimized ---
	stopChunkWithoutUsage.Range(func(key, _ any) bool {
		stopChunkWithoutUsage.Delete(key)
		return true
	})
	optStop := FilterSSEUsageMetadataOptimized(append([]byte{}, stopChunk...))
	optUsage := FilterSSEUsageMetadataOptimized(append([]byte{}, usageChunk...))

	if !bytes.Equal(origStop, optStop) {
		t.Errorf("stop chunk mismatch\n  original:  %s\n  optimized: %s", string(origStop), string(optStop))
	}
	if !bytes.Equal(origUsage, optUsage) {
		t.Errorf("usage chunk mismatch\n  original:  %s\n  optimized: %s", string(origUsage), string(optUsage))
	}
}

// ---------------------------------------------------------------------------
// convertStreamToNonStream vs convertStreamToNonStreamOptimized
// ---------------------------------------------------------------------------

func TestConvertStreamToNonStream_Parity(t *testing.T) {
	tests := []struct {
		name   string
		stream string
	}{
		{
			name:   "empty",
			stream: "",
		},
		{
			name:   "single text part",
			stream: `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"hello world"}]},"finishReason":"STOP"}],"modelVersion":"gemini-2.5-pro","responseId":"resp-1"},"traceId":"t-1","usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`,
		},
		{
			name: "multiple text chunks merged",
			stream: `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"hello "}]}}]}}` + "\n" +
				`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"world"}]},"finishReason":"STOP"}],"modelVersion":"v1","responseId":"r1"},"traceId":"t1","usageMetadata":{"promptTokenCount":1}}`,
		},
		{
			name:   "thought part with signature",
			stream: `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"thinking...","thought":true,"thoughtSignature":"sig123"}]}}]},"traceId":"t2"}`,
		},
		{
			name:   "thought_signature renamed",
			stream: `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"think","thought":true,"thought_signature":"sig456"}]}}]},"traceId":"t3"}`,
		},
		{
			name: "thought then text",
			stream: `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"reasoning","thought":true,"thoughtSignature":"s1"}]}}]}}` + "\n" +
				`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"answer"}]},"finishReason":"STOP"}],"modelVersion":"v2"},"traceId":"t4"}`,
		},
		{
			name:   "function call part",
			stream: `{"response":{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"NYC"}}}]},"finishReason":"STOP"}],"modelVersion":"v1"},"traceId":"t5"}`,
		},
		{
			name:   "inline_data renamed to inlineData",
			stream: `{"response":{"candidates":[{"content":{"role":"model","parts":[{"inline_data":{"mime_type":"image/png","data":"base64data"}}]},"finishReason":"STOP"}]},"traceId":"t6"}`,
		},
		{
			name: "mixed: thought + text + functionCall",
			stream: `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"let me think","thought":true}]}}]}}` + "\n" +
				`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"I'll call a tool"}]}}]}}` + "\n" +
				`{"response":{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"search","args":{"q":"test"}}}]},"finishReason":"STOP"}],"modelVersion":"v1","responseId":"r3"},"traceId":"t7","usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":30,"totalTokenCount":80}}`,
		},
		{
			name:   "aistudio format (no response wrapper)",
			stream: `{"candidates":[{"content":{"role":"model","parts":[{"text":"direct"}]},"finishReason":"STOP"}],"modelVersion":"v1","usageMetadata":{"promptTokenCount":5}}`,
		},
		{
			name: "empty text parts filtered",
			stream: `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":""}]}}]}}` + "\n" +
				`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"real content"}]},"finishReason":"STOP"}],"modelVersion":"v1"},"traceId":"t8"}`,
		},
		{
			name: "whitespace-only text parts filtered",
			stream: `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"   "}]}}]}}` + "\n" +
				`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}}`,
		},
		{
			name:   "no valid JSON lines",
			stream: "not-json\n\nanother-bad-line\n",
		},
		{
			name:   "usage at root level",
			stream: `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"responseId":"r1"},"traceId":"t9","usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`,
		},
	}

	executor := &AntigravityExecutor{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []byte(tt.stream)
			original := executor.convertStreamToNonStream(append([]byte{}, input...))
			optimized := executor.convertStreamToNonStreamOptimized(append([]byte{}, input...))

			// Parse both outputs and compare semantically (field order may differ)
			origJSON := gjson.ParseBytes(original)
			optJSON := gjson.ParseBytes(optimized)

			if !jsonDeepEqual(t, origJSON, optJSON, "") {
				t.Errorf("output mismatch\n  original:  %s\n  optimized: %s", string(original), string(optimized))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildRequest vs buildRequestOptimized
// ---------------------------------------------------------------------------

func TestBuildRequest_Parity(t *testing.T) {
	tests := []struct {
		name      string
		modelName string
		payload   string
		stream    bool
		alt       string
	}{
		{
			name:      "gemini model non-stream",
			modelName: "gemini-2.5-pro",
			payload:   `{"request":{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":8192}}}`,
			stream:    false,
		},
		{
			name:      "claude model stream",
			modelName: "claude-opus-4-6",
			payload:   `{"request":{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":8192}}}`,
			stream:    true,
		},
		{
			name:      "with parametersJsonSchema",
			modelName: "claude-sonnet-4-6",
			payload: `{"request":{"tools":[{"function_declarations":[{
				"name":"tool_1",
				"parametersJsonSchema":{"type":"object","properties":{"arg":{"type":"string"}}}
			}]}],"contents":[{"parts":[{"text":"hi"}]}]}}`,
			stream: true,
		},
		{
			name:      "gemini model with alt",
			modelName: "gemini-2.5-flash",
			payload:   `{"request":{"contents":[{"parts":[{"text":"test"}]}]}}`,
			stream:    true,
			alt:       "json",
		},
		{
			name:      "with systemInstruction",
			modelName: "claude-opus-4-6",
			payload:   `{"request":{"systemInstruction":{"parts":[{"text":"You are helpful."}]},"contents":[{"parts":[{"text":"hi"}]}]}}`,
			stream:    false,
		},
		{
			name:      "with toolConfig at root",
			modelName: "gemini-2.5-pro",
			payload:   `{"request":{"contents":[{"parts":[{"text":"hi"}]}]},"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}}`,
			stream:    false,
		},
		{
			name:      "gemini-3-pro-high model",
			modelName: "gemini-3-pro-high",
			payload:   `{"request":{"contents":[{"parts":[{"text":"hi"}]}]}}`,
			stream:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &AntigravityExecutor{}
			auth := &cliproxyauth.Auth{
				Metadata: map[string]any{
					"project_id": "test-project-fixed",
				},
			}
			ctx := context.Background()
			payload := []byte(tt.payload)

			origReq, origErr := executor.buildRequest(ctx, auth, "test-token", tt.modelName, append([]byte{}, payload...), tt.stream, tt.alt, "https://example.com")
			optReq, optErr := executor.buildRequestOptimized(ctx, auth, "test-token", tt.modelName, append([]byte{}, payload...), tt.stream, tt.alt, "https://example.com")

			if (origErr != nil) != (optErr != nil) {
				t.Fatalf("error mismatch: original=%v, optimized=%v", origErr, optErr)
			}
			if origErr != nil {
				return
			}

			// Compare URL
			if origReq.URL.String() != optReq.URL.String() {
				t.Errorf("URL mismatch\n  original:  %s\n  optimized: %s", origReq.URL.String(), optReq.URL.String())
			}

			// Compare headers (excluding Content-Length which may differ slightly)
			for _, hdr := range []string{"Content-Type", "Authorization", "Accept"} {
				if origReq.Header.Get(hdr) != optReq.Header.Get(hdr) {
					t.Errorf("header %s mismatch\n  original:  %s\n  optimized: %s", hdr, origReq.Header.Get(hdr), optReq.Header.Get(hdr))
				}
			}

			// Compare bodies (strip non-deterministic fields: requestId)
			origBody, _ := io.ReadAll(origReq.Body)
			optBody, _ := io.ReadAll(optReq.Body)

			// Strip non-deterministic fields: requestId (always random),
			// request.sessionId (random when payload has no role:"user" content)
			origBodyStr, _ := sjson.Delete(string(origBody), "requestId")
			origBodyStr, _ = sjson.Delete(origBodyStr, "request.sessionId")
			optBodyStr, _ := sjson.Delete(string(optBody), "requestId")
			optBodyStr, _ = sjson.Delete(optBodyStr, "request.sessionId")

			origParsed := gjson.Parse(origBodyStr)
			optParsed := gjson.Parse(optBodyStr)

			if !jsonDeepEqual(t, origParsed, optParsed, "") {
				t.Errorf("body mismatch\n  original:  %s\n  optimized: %s", origBodyStr, optBodyStr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// jsonDeepEqual compares two gjson.Result trees semantically.
// It handles object field reordering and array element comparison.
func jsonDeepEqual(t *testing.T, a, b gjson.Result, path string) bool {
	t.Helper()

	if a.Type != b.Type {
		t.Logf("type mismatch at %s: %v vs %v", path, a.Type, b.Type)
		return false
	}

	switch a.Type {
	case gjson.Null:
		return true
	case gjson.False, gjson.True:
		return a.Bool() == b.Bool()
	case gjson.Number:
		return a.Float() == b.Float()
	case gjson.String:
		return a.String() == b.String()
	case gjson.JSON:
		if a.IsArray() {
			aArr := a.Array()
			bArr := b.Array()
			if len(aArr) != len(bArr) {
				t.Logf("array length mismatch at %s: %d vs %d", path, len(aArr), len(bArr))
				return false
			}
			for i := range aArr {
				elemPath := path + "[" + string(rune('0'+i)) + "]"
				if !jsonDeepEqual(t, aArr[i], bArr[i], elemPath) {
					return false
				}
			}
			return true
		}
		// Object comparison
		aMap := map[string]gjson.Result{}
		bMap := map[string]gjson.Result{}
		a.ForEach(func(key, val gjson.Result) bool {
			aMap[key.String()] = val
			return true
		})
		b.ForEach(func(key, val gjson.Result) bool {
			bMap[key.String()] = val
			return true
		})
		if len(aMap) != len(bMap) {
			t.Logf("object key count mismatch at %s: %d vs %d", path, len(aMap), len(bMap))
			// Log missing keys
			for k := range aMap {
				if _, ok := bMap[k]; !ok {
					t.Logf("  key %q in original but not in optimized", k)
				}
			}
			for k := range bMap {
				if _, ok := aMap[k]; !ok {
					t.Logf("  key %q in optimized but not in original", k)
				}
			}
			return false
		}
		for k, aVal := range aMap {
			bVal, ok := bMap[k]
			if !ok {
				t.Logf("key %q missing in optimized at %s", k, path)
				return false
			}
			childPath := path + "." + k
			if !jsonDeepEqual(t, aVal, bVal, childPath) {
				return false
			}
		}
		return true
	}
	return a.Raw == b.Raw
}
