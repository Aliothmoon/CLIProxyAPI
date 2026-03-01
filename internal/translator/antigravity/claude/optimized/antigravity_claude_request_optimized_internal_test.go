package optimized

import (
	"errors"
	"testing"

	"github.com/bytedance/sonic/ast"
	"github.com/tidwall/gjson"
)

func TestParseToolUseArgsRaw_StringRequiresValidJSONObject(t *testing.T) {
	t.Run("valid object string", func(t *testing.T) {
		root := ast.NewRaw(`{"input":"{\"city\":\"Paris\"}"}`)
		got := parseToolUseArgsRaw(root.Get("input"))
		if got != `{"city":"Paris"}` {
			t.Fatalf("unexpected parsed args: %q", got)
		}
	})

	t.Run("invalid object-like string", func(t *testing.T) {
		root := ast.NewRaw(`{"input":"{bad-json"}`)
		got := parseToolUseArgsRaw(root.Get("input"))
		if got != "" {
			t.Fatalf("expected invalid json-like object string to be rejected, got: %q", got)
		}
	})
}

func TestConvertClaudeRequestToAntigravity_DropsInvalidToolUseStringInput(t *testing.T) {
	raw := []byte(`{
		"messages":[
			{
				"role":"assistant",
				"content":[
					{"type":"text","text":"hello"},
					{"type":"tool_use","id":"tool_1","name":"weather","input":"{bad-json"}
				]
			}
		]
	}`)

	out := ConvertClaudeRequestToAntigravity("claude-sonnet-4-5-thinking", raw, false)
	if !gjson.ValidBytes(out) {
		t.Fatalf("output must remain valid json, got: %s", out)
	}
	if gjson.GetBytes(out, "request.contents.0.parts.#(functionCall.name=\"weather\")").Exists() {
		t.Fatalf("invalid tool_use input should be dropped, got: %s", out)
	}
	if gjson.GetBytes(out, "request.contents.0.parts.0.text").String() != "hello" {
		t.Fatalf("text part should be preserved, got: %s", out)
	}
}

func TestConvertClaudeRequestToAntigravity_MarshalFailureFallbackUsesMinimalRequest(t *testing.T) {
	originalMarshal := marshalRequestEnvelope
	marshalRequestEnvelope = func(any) ([]byte, error) {
		return nil, errors.New("forced marshal failure")
	}
	t.Cleanup(func() { marshalRequestEnvelope = originalMarshal })

	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	modelName := "claude-sonnet-4-5-thinking"

	out := ConvertClaudeRequestToAntigravity(modelName, raw, false)
	if !gjson.ValidBytes(out) {
		t.Fatalf("fallback output must be valid json, got: %s", out)
	}
	if got := gjson.GetBytes(out, "model").String(); got != modelName {
		t.Fatalf("fallback model mismatch: got=%q want=%q", got, modelName)
	}
	if !gjson.GetBytes(out, "request").Exists() {
		t.Fatalf("fallback output must keep antigravity request envelope, got: %s", out)
	}
	if gjson.GetBytes(out, "messages").Exists() {
		t.Fatalf("fallback must not passthrough original Claude format, got: %s", out)
	}
	if !gjson.GetBytes(out, "request.safetySettings").Exists() {
		t.Fatalf("fallback should still attach default safety settings, got: %s", out)
	}
}
