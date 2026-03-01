package optimized

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
	"github.com/tidwall/gjson"
)

func TestConvertAntigravityResponseToClaudeNonStream_TextUsage(t *testing.T) {
	requestJSON := []byte(`{"model":"claude-sonnet-4-5-thinking"}`)
	rawJSON := []byte(`{
		"response":{
			"responseId":"resp_1",
			"modelVersion":"claude-sonnet-4-5",
			"usageMetadata":{
				"promptTokenCount":10,
				"candidatesTokenCount":20,
				"thoughtsTokenCount":5,
				"totalTokenCount":35,
				"cachedContentTokenCount":2
			},
			"candidates":[{
				"finishReason":"STOP",
				"content":{"parts":[{"text":"hello world"}]}
			}]
		}
	}`)

	got := ConvertAntigravityResponseToClaudeNonStream(context.Background(), "", requestJSON, requestJSON, rawJSON, nil)
	if !gjson.Valid(got) {
		t.Fatalf("output is not valid json: %s", got)
	}
	if gjson.Get(got, "id").String() != "resp_1" {
		t.Fatalf("unexpected id: %s", gjson.Get(got, "id").String())
	}
	if gjson.Get(got, "model").String() != "claude-sonnet-4-5" {
		t.Fatalf("unexpected model: %s", gjson.Get(got, "model").String())
	}
	if gjson.Get(got, "stop_reason").String() != "end_turn" {
		t.Fatalf("unexpected stop_reason: %s", gjson.Get(got, "stop_reason").String())
	}
	if gjson.Get(got, "usage.input_tokens").Int() != 10 {
		t.Fatalf("unexpected input tokens: %d", gjson.Get(got, "usage.input_tokens").Int())
	}
	if gjson.Get(got, "usage.output_tokens").Int() != 25 {
		t.Fatalf("unexpected output tokens: %d", gjson.Get(got, "usage.output_tokens").Int())
	}
	if gjson.Get(got, "usage.cache_read_input_tokens").Int() != 2 {
		t.Fatalf("unexpected cache_read_input_tokens: %d", gjson.Get(got, "usage.cache_read_input_tokens").Int())
	}
	if gjson.Get(got, "content.0.type").String() != "text" {
		t.Fatalf("expected first content block to be text, got: %s", gjson.Get(got, "content.0.type").String())
	}
	if gjson.Get(got, "content.0.text").String() != "hello world" {
		t.Fatalf("unexpected text block content: %s", gjson.Get(got, "content.0.text").String())
	}
}

func TestConvertAntigravityResponseToClaudeNonStream_ThinkingSignature(t *testing.T) {
	model := "claude-sonnet-4-5-thinking"
	requestJSON := []byte(fmt.Sprintf(`{"model":"%s"}`, model))
	rawJSON := []byte(`{
		"response":{
			"responseId":"resp_2",
			"modelVersion":"claude-sonnet-4-5",
			"usageMetadata":{
				"promptTokenCount":1,
				"candidatesTokenCount":1,
				"thoughtsTokenCount":1,
				"totalTokenCount":3
			},
			"candidates":[{
				"finishReason":"STOP",
				"content":{"parts":[
					{"text":"thinking payload","thought":true},
					{"text":"","thought":true,"thoughtSignature":"sig_abc"}
				]}
			}]
		}
	}`)

	got := ConvertAntigravityResponseToClaudeNonStream(context.Background(), "", requestJSON, requestJSON, rawJSON, nil)
	if !gjson.Valid(got) {
		t.Fatalf("output is not valid json: %s", got)
	}
	if gjson.Get(got, "content.0.type").String() != "thinking" {
		t.Fatalf("expected thinking block, got: %s", gjson.Get(got, "content.0.type").String())
	}
	if gjson.Get(got, "content.0.thinking").String() != "thinking payload" {
		t.Fatalf("unexpected thinking content: %s", gjson.Get(got, "content.0.thinking").String())
	}

	expectedSignature := fmt.Sprintf("%s#%s", cache.GetModelGroup(model), "sig_abc")
	if gjson.Get(got, "content.0.signature").String() != expectedSignature {
		t.Fatalf("unexpected signature: %s", gjson.Get(got, "content.0.signature").String())
	}
}

func TestConvertAntigravityResponseToClaudeNonStream_ToolUse(t *testing.T) {
	requestJSON := []byte(`{"model":"claude-sonnet-4-5-thinking"}`)
	rawJSON := []byte(`{
		"response":{
			"responseId":"resp_3",
			"modelVersion":"claude-sonnet-4-5",
			"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1,"thoughtsTokenCount":0,"totalTokenCount":4},
			"candidates":[{
				"finishReason":"STOP",
				"content":{"parts":[
					{"functionCall":{"name":"weather_lookup","args":{"city":"Paris","days":2}}}
				]}
			}]
		}
	}`)

	got := ConvertAntigravityResponseToClaudeNonStream(context.Background(), "", requestJSON, requestJSON, rawJSON, nil)
	if !gjson.Valid(got) {
		t.Fatalf("output is not valid json: %s", got)
	}
	if gjson.Get(got, "stop_reason").String() != "tool_use" {
		t.Fatalf("expected stop_reason tool_use, got: %s", gjson.Get(got, "stop_reason").String())
	}
	if gjson.Get(got, "content.0.type").String() != "tool_use" {
		t.Fatalf("expected tool_use block, got: %s", gjson.Get(got, "content.0.type").String())
	}
	if gjson.Get(got, "content.0.id").String() != "tool_1" {
		t.Fatalf("unexpected tool id: %s", gjson.Get(got, "content.0.id").String())
	}
	if gjson.Get(got, "content.0.name").String() != "weather_lookup" {
		t.Fatalf("unexpected tool name: %s", gjson.Get(got, "content.0.name").String())
	}
	if gjson.Get(got, "content.0.input.city").String() != "Paris" {
		t.Fatalf("unexpected tool input city: %s", gjson.Get(got, "content.0.input.city").String())
	}
	if gjson.Get(got, "content.0.input.days").Int() != 2 {
		t.Fatalf("unexpected tool input days: %d", gjson.Get(got, "content.0.input.days").Int())
	}
}

func TestConvertAntigravityResponseToClaudeNonStream_UsageRemovedWhenMissing(t *testing.T) {
	requestJSON := []byte(`{"model":"claude-sonnet-4-5-thinking"}`)
	rawJSON := []byte(`{
		"response":{
			"responseId":"resp_4",
			"modelVersion":"claude-sonnet-4-5",
			"candidates":[{
				"finishReason":"STOP",
				"content":{"parts":[{"text":"text without usage"}]}
			}]
		}
	}`)

	got := ConvertAntigravityResponseToClaudeNonStream(context.Background(), "", requestJSON, requestJSON, rawJSON, nil)
	if !gjson.Valid(got) {
		t.Fatalf("output is not valid json: %s", got)
	}
	if gjson.Get(got, "usage").Exists() {
		t.Fatalf("usage should be removed when usageMetadata is missing and token counts are zero: %s", got)
	}
}

func TestConvertAntigravityResponseToClaude_ThinkingSignatureCache(t *testing.T) {
	cache.ClearSignatureCache("")
	model := "claude-sonnet-4-5-thinking"
	signature := "sig_xyz_12345678901234567890123456789012345678901234567890"
	requestJSON := []byte(fmt.Sprintf(`{"model":"%s"}`, model))

	chunk1 := []byte(`{
		"response":{
			"candidates":[{
				"content":{"parts":[{"text":"first thinking block","thought":true}]}
			}]
		}
	}`)
	chunk2 := []byte(`{
		"response":{
			"candidates":[{
				"content":{"parts":[{"text":"","thought":true,"thoughtSignature":"` + signature + `"}]}
			}]
		}
	}`)

	var param any
	out1 := ConvertAntigravityResponseToClaude(context.Background(), "", requestJSON, requestJSON, chunk1, &param)
	if len(out1) != 1 {
		t.Fatalf("expected one output chunk for first chunk, got: %d", len(out1))
	}
	if !strings.Contains(out1[0], "event: message_start") {
		t.Fatalf("missing message_start event: %s", out1[0])
	}
	if !strings.Contains(out1[0], "thinking_delta") {
		t.Fatalf("missing thinking_delta in first output: %s", out1[0])
	}

	params := param.(*Params)
	thinkingText := params.CurrentThinkingText.String()
	if thinkingText == "" {
		t.Fatal("thinking text should be accumulated after first chunk")
	}

	out2 := ConvertAntigravityResponseToClaude(context.Background(), "", requestJSON, requestJSON, chunk2, &param)
	if len(out2) != 1 {
		t.Fatalf("expected one output chunk for second chunk, got: %d", len(out2))
	}
	if !strings.Contains(out2[0], "signature_delta") {
		t.Fatalf("missing signature_delta in second output: %s", out2[0])
	}

	expectedSignature := fmt.Sprintf("%s#%s", cache.GetModelGroup(model), signature)
	if !strings.Contains(out2[0], expectedSignature) {
		t.Fatalf("missing expected prefixed signature in output: %s", out2[0])
	}
	if cache.GetCachedSignature(model, thinkingText) != signature {
		t.Fatalf("signature was not cached for thinking text: %s", thinkingText)
	}
	if params.CurrentThinkingText.Len() != 0 {
		t.Fatalf("thinking text should be reset after signature chunk, got: %s", params.CurrentThinkingText.String())
	}
}

func TestConvertAntigravityResponseToClaude_DoneHandling(t *testing.T) {
	t.Run("with content", func(t *testing.T) {
		requestJSON := []byte(`{"model":"claude-sonnet-4-5-thinking"}`)
		chunk := []byte(`{
			"response":{
				"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"thoughtsTokenCount":0,"totalTokenCount":5},
				"candidates":[{
					"finishReason":"STOP",
					"content":{"parts":[{"text":"hello"}]}
				}]
			}
		}`)

		var param any
		_ = ConvertAntigravityResponseToClaude(context.Background(), "", requestJSON, requestJSON, chunk, &param)
		done := ConvertAntigravityResponseToClaude(context.Background(), "", requestJSON, requestJSON, []byte("[DONE]"), &param)
		if len(done) != 1 {
			t.Fatalf("expected one done output chunk, got: %d", len(done))
		}
		if !strings.Contains(done[0], "event: message_stop") {
			t.Fatalf("done chunk should contain message_stop event: %s", done[0])
		}
	})

	t.Run("without content", func(t *testing.T) {
		requestJSON := []byte(`{"model":"claude-sonnet-4-5-thinking"}`)
		var param any
		done := ConvertAntigravityResponseToClaude(context.Background(), "", requestJSON, requestJSON, []byte("[DONE]"), &param)
		if len(done) != 0 {
			t.Fatalf("expected empty output when no content is emitted, got: %v", done)
		}
	})
}

func TestConvertAntigravityResponseToClaude_StreamJSONEscaping(t *testing.T) {
	requestJSON := []byte(`{"model":"claude-sonnet-4-5-thinking"}`)
	expectedText := "line1\n\"quoted\" \\ slash"
	expectedPartialJSON := `{"q":"a\"b","path":"c\\d"}`
	chunk := []byte(`{
		"response":{
			"candidates":[{
				"content":{"parts":[
					{"text":"line1\n\"quoted\" \\ slash"},
					{"functionCall":{"name":"tool_x","args":{"q":"a\"b","path":"c\\d"}}}
				]}
			}]
		}
	}`)

	var param any
	out := ConvertAntigravityResponseToClaude(context.Background(), "", requestJSON, requestJSON, chunk, &param)
	if len(out) != 1 {
		t.Fatalf("expected one output chunk, got: %d", len(out))
	}

	dataLines := sseDataLines(out[0])
	if len(dataLines) == 0 {
		t.Fatalf("expected non-empty SSE data lines, raw output: %s", out[0])
	}

	var seenTextDelta bool
	var seenInputJSONDelta bool
	for i := range dataLines {
		line := dataLines[i]
		if !gjson.Valid(line) {
			t.Fatalf("invalid SSE data JSON at index %d: %s", i, line)
		}

		dataType := gjson.Get(line, "type").String()
		if dataType != "content_block_delta" {
			continue
		}

		deltaType := gjson.Get(line, "delta.type").String()
		if deltaType == "text_delta" {
			seenTextDelta = true
			if got := gjson.Get(line, "delta.text").String(); got != expectedText {
				t.Fatalf("unexpected escaped text delta: got %q want %q", got, expectedText)
			}
		}
		if deltaType == "input_json_delta" {
			seenInputJSONDelta = true
			if got := gjson.Get(line, "delta.partial_json").String(); got != expectedPartialJSON {
				t.Fatalf("unexpected partial_json delta: got %q want %q", got, expectedPartialJSON)
			}
		}
	}

	if !seenTextDelta {
		t.Fatalf("did not find text_delta in output: %s", out[0])
	}
	if !seenInputJSONDelta {
		t.Fatalf("did not find input_json_delta in output: %s", out[0])
	}
}

func TestConvertAntigravityResponseToClaude_StreamJSONEscaping_ControlChars(t *testing.T) {
	requestJSON := []byte(`{"model":"claude-sonnet-4-5-thinking"}`)
	expectedText := "a" + "\a" + "b" + "\v" + "c"
	chunk := []byte(`{
		"response":{
			"candidates":[{
				"content":{"parts":[
					{"text":"a\u0007b\u000bc"}
				]}
			}]
		}
	}`)

	var param any
	out := ConvertAntigravityResponseToClaude(context.Background(), "", requestJSON, requestJSON, chunk, &param)
	if len(out) != 1 {
		t.Fatalf("expected one output chunk, got: %d", len(out))
	}

	dataLines := sseDataLines(out[0])
	if len(dataLines) == 0 {
		t.Fatalf("expected non-empty SSE data lines, raw output: %s", out[0])
	}

	var seenTextDelta bool
	for i := range dataLines {
		line := dataLines[i]
		if !gjson.Valid(line) {
			t.Fatalf("invalid SSE data JSON at index %d: %s", i, line)
		}

		if gjson.Get(line, "type").String() != "content_block_delta" {
			continue
		}
		if gjson.Get(line, "delta.type").String() != "text_delta" {
			continue
		}

		seenTextDelta = true
		if got := gjson.Get(line, "delta.text").String(); got != expectedText {
			t.Fatalf("unexpected control-char text delta: got %q want %q", got, expectedText)
		}
	}

	if !seenTextDelta {
		t.Fatalf("did not find text_delta in output: %s", out[0])
	}
}

func TestConvertAntigravityResponseToClaude_StreamMalformedChunkFallback(t *testing.T) {
	requestJSON := []byte(`{"model":"claude-sonnet-4-5-thinking"}`)
	malformed := []byte(`{"response":`)

	var param any
	out := ConvertAntigravityResponseToClaude(context.Background(), "", requestJSON, requestJSON, malformed, &param)
	if len(out) != 1 {
		t.Fatalf("expected one output chunk, got: %d", len(out))
	}
	if !strings.Contains(out[0], "event: message_start") {
		t.Fatalf("expected message_start on first malformed chunk, got: %s", out[0])
	}

	dataLines := sseDataLines(out[0])
	if len(dataLines) == 0 {
		t.Fatalf("expected at least one SSE data line, raw output: %s", out[0])
	}
	for i := range dataLines {
		if !gjson.Valid(dataLines[i]) {
			t.Fatalf("expected valid SSE data JSON on malformed fallback at index %d: %s", i, dataLines[i])
		}
	}

	out2 := ConvertAntigravityResponseToClaude(context.Background(), "", requestJSON, requestJSON, malformed, &param)
	if len(out2) != 1 {
		t.Fatalf("expected one output chunk for subsequent malformed chunk, got: %d", len(out2))
	}
	if out2[0] != "" {
		t.Fatalf("expected empty output for subsequent malformed chunk, got: %q", out2[0])
	}
}

func TestConvertAntigravityResponseToClaudeNonStream_InvalidJSONFallback(t *testing.T) {
	requestJSON := []byte(`{"model":"claude-sonnet-4-5-thinking"}`)
	malformed := []byte(`{"response":`)

	got := ConvertAntigravityResponseToClaudeNonStream(context.Background(), "", requestJSON, requestJSON, malformed, nil)
	if !gjson.Valid(got) {
		t.Fatalf("expected valid JSON fallback for malformed non-stream payload, got: %s", got)
	}
	if gjson.Get(got, "stop_reason").String() != "end_turn" {
		t.Fatalf("unexpected stop_reason in malformed fallback: %s", gjson.Get(got, "stop_reason").String())
	}
	if gjson.Get(got, "usage").Exists() {
		t.Fatalf("usage should be absent for malformed non-stream fallback: %s", got)
	}
}

func sseDataLines(s string) []string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for i := range lines {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "data: ") {
			out = append(out, strings.TrimPrefix(line, "data: "))
		}
	}
	return out
}
