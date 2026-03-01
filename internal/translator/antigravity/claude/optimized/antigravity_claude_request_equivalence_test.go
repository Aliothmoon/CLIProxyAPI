package optimized_test

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
	legacy "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/antigravity/claude"
	optimized "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/antigravity/claude/optimized"
)

type requestEquivalenceCase struct {
	Name       string
	ModelName  string
	InputRaw   []byte
	CacheSeeds []requestCacheSeed
}

type requestCacheSeed struct {
	ModelName string `json:"model_name"`
	Text      string `json:"text"`
	Signature string `json:"signature"`
}

type requestFixtureFile struct {
	Cases []requestFixtureCase `json:"cases"`
}

type requestFixtureCase struct {
	Name      string             `json:"name"`
	ModelName string             `json:"model_name"`
	Input     json.RawMessage    `json:"input"`
	Cache     []requestCacheSeed `json:"cache,omitempty"`
}

func TestRequestEquivalence_LegacyVsOptimized_Random(t *testing.T) {
	caseCount := 360
	if testing.Short() {
		caseCount = 80
	}

	cases := buildRandomRequestEquivalenceCases(caseCount)
	for i := range cases {
		tc := cases[i]
		t.Run(tc.Name, func(t *testing.T) {
			assertRequestEquivalent(t, tc)
		})
	}
}

func TestRequestEquivalenceFixtures_LegacyVsOptimized(t *testing.T) {
	cases := loadRequestFixtureCases(t)
	for i := range cases {
		tc := cases[i]
		t.Run(tc.Name, func(t *testing.T) {
			assertRequestEquivalent(t, tc)
		})
	}
}

func assertRequestEquivalent(t *testing.T, tc requestEquivalenceCase) {
	t.Helper()

	cache.ClearSignatureCache("")
	seedSignatureCache(tc.CacheSeeds)
	legacyOut := legacy.ConvertClaudeRequestToAntigravity(tc.ModelName, tc.InputRaw, false)

	cache.ClearSignatureCache("")
	seedSignatureCache(tc.CacheSeeds)
	optimizedOut := optimized.ConvertClaudeRequestToAntigravity(tc.ModelName, tc.InputRaw, false)

	legacyCanonical, err := canonicalizeJSON(string(legacyOut))
	if err != nil {
		if string(legacyOut) != string(optimizedOut) {
			t.Fatalf(
				"legacy output is invalid json and raw outputs differ\nlegacy_raw=%s\noptimized_raw=%s\ninput=%s\nmodel=%s",
				legacyOut,
				optimizedOut,
				tc.InputRaw,
				tc.ModelName,
			)
		}
		return
	}
	optimizedCanonical, err := canonicalizeJSON(string(optimizedOut))
	if err != nil {
		t.Fatalf("optimized output invalid json: %v\nraw=%s\ninput=%s", err, optimizedOut, tc.InputRaw)
	}

	if legacyCanonical != optimizedCanonical {
		t.Fatalf(
			"request translation mismatch\nlegacy=%s\noptimized=%s\nlegacy_raw=%s\noptimized_raw=%s\ninput=%s\nmodel=%s",
			legacyCanonical,
			optimizedCanonical,
			legacyOut,
			optimizedOut,
			tc.InputRaw,
			tc.ModelName,
		)
	}
}

func seedSignatureCache(seeds []requestCacheSeed) {
	for i := range seeds {
		seed := seeds[i]
		cache.CacheSignature(seed.ModelName, seed.Text, seed.Signature)
	}
}

func loadRequestFixtureCases(t *testing.T) []requestEquivalenceCase {
	t.Helper()

	raw, err := os.ReadFile("testdata/request_equivalence_cases.json")
	if err != nil {
		t.Fatalf("read request fixture file failed: %v", err)
	}

	var payload requestFixtureFile
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("parse request fixture file failed: %v", err)
	}
	if len(payload.Cases) == 0 {
		t.Fatal("request fixture file has no cases")
	}

	out := make([]requestEquivalenceCase, 0, len(payload.Cases))
	for i := range payload.Cases {
		c := payload.Cases[i]
		if c.Name == "" {
			t.Fatalf("request fixture case %d has empty name", i)
		}
		if c.ModelName == "" {
			t.Fatalf("request fixture case %s missing model_name", c.Name)
		}
		if len(c.Input) == 0 {
			t.Fatalf("request fixture case %s missing input", c.Name)
		}

		out = append(out, requestEquivalenceCase{
			Name:       c.Name,
			ModelName:  c.ModelName,
			InputRaw:   append([]byte(nil), c.Input...),
			CacheSeeds: append([]requestCacheSeed(nil), c.Cache...),
		})
	}
	return out
}

func buildRandomRequestEquivalenceCases(n int) []requestEquivalenceCase {
	r := rand.New(rand.NewSource(20260301))

	models := []string{
		"claude-sonnet-4-5-thinking",
		"claude-3-5-sonnet-20241022",
		"gemini-2.5-pro",
		"gpt-4.1",
		"claude-opus-4-1",
	}

	out := make([]requestEquivalenceCase, 0, n)
	for i := 0; i < n; i++ {
		modelName := models[r.Intn(len(models))]
		caseData := map[string]any{
			"model": fmt.Sprintf("input_model_%d", i),
		}

		if v, ok := randomSystem(r, i); ok {
			caseData["system"] = v
		}

		cacheSeeds := make([]requestCacheSeed, 0, 8)
		caseData["messages"] = randomMessages(r, i, modelName, &cacheSeeds)

		if tools, ok := randomTools(r, i); ok {
			caseData["tools"] = tools
		}
		if thinking, ok := randomThinkingConfig(r, i); ok {
			caseData["thinking"] = thinking
		}

		if r.Intn(100) < 70 {
			caseData["temperature"] = float64(r.Intn(500)-200) / 100
		} else if r.Intn(100) < 15 {
			caseData["temperature"] = "0.8"
		}
		if r.Intn(100) < 70 {
			caseData["top_p"] = float64(r.Intn(101)) / 100
		} else if r.Intn(100) < 15 {
			caseData["top_p"] = "0.9"
		}
		if r.Intn(100) < 70 {
			caseData["top_k"] = float64(r.Intn(50))
		} else if r.Intn(100) < 15 {
			caseData["top_k"] = "40"
		}
		if r.Intn(100) < 70 {
			caseData["max_tokens"] = float64(1 + r.Intn(4096))
		} else if r.Intn(100) < 15 {
			caseData["max_tokens"] = "1024"
		}

		out = append(out, requestEquivalenceCase{
			Name:       fmt.Sprintf("random_case_%03d", i),
			ModelName:  modelName,
			InputRaw:   mustMarshalJSON(caseData),
			CacheSeeds: cacheSeeds,
		})
	}
	return out
}

func randomSystem(r *rand.Rand, caseIndex int) (any, bool) {
	switch r.Intn(5) {
	case 0:
		return nil, false
	case 1:
		return fmt.Sprintf("system-line-%d", caseIndex), true
	case 2:
		return "", true
	case 3:
		return []any{
			map[string]any{"type": "text", "text": fmt.Sprintf("sys-a-%d", caseIndex)},
			map[string]any{"type": "text", "text": ""},
			map[string]any{"type": "unknown", "text": "ignored"},
		}, true
	default:
		return []any{}, true
	}
}

func randomMessages(r *rand.Rand, caseIndex int, modelName string, cacheSeeds *[]requestCacheSeed) []any {
	count := r.Intn(7)
	messages := make([]any, 0, count)
	for i := 0; i < count; i++ {
		msg := map[string]any{}
		switch r.Intn(6) {
		case 0:
			msg["role"] = "user"
		case 1:
			msg["role"] = "assistant"
		case 2:
			msg["role"] = "tool"
		case 3:
			msg["role"] = ""
		case 4:
			msg["role"] = i
		default:
			// missing role
		}

		switch r.Intn(6) {
		case 0:
			msg["content"] = fmt.Sprintf("message-content-%d-%d", caseIndex, i)
		case 1:
			msg["content"] = ""
		case 2:
			msg["content"] = randomContentArray(r, caseIndex, i, modelName, cacheSeeds)
		case 3:
			msg["content"] = map[string]any{"unexpected": true}
		case 4:
			msg["content"] = nil
		default:
			// missing content
		}

		messages = append(messages, msg)
	}
	return messages
}

func randomContentArray(r *rand.Rand, caseIndex, messageIndex int, modelName string, cacheSeeds *[]requestCacheSeed) []any {
	count := 1 + r.Intn(8)
	content := make([]any, 0, count)
	for i := 0; i < count; i++ {
		switch r.Intn(10) {
		case 0:
			content = append(content, map[string]any{
				"type": "text",
				"text": randomRequestText(caseIndex, messageIndex, i),
			})
		case 1:
			content = append(content, map[string]any{
				"type": "text",
				"text": "",
			})
		case 2:
			text := randomRequestText(caseIndex, messageIndex, i)
			sig := validSignature(caseIndex, messageIndex, i, "cache")
			*cacheSeeds = append(*cacheSeeds, requestCacheSeed{
				ModelName: modelName,
				Text:      text,
				Signature: sig,
			})
			content = append(content, map[string]any{
				"type":      "thinking",
				"thinking":  text,
				"signature": "wrong-group#" + validSignature(caseIndex, messageIndex, i, "stale"),
			})
		case 3:
			text := randomRequestText(caseIndex, messageIndex, i)
			content = append(content, map[string]any{
				"type":      "thinking",
				"thinking":  map[string]any{"text": text},
				"signature": cache.GetModelGroup(modelName) + "#" + validSignature(caseIndex, messageIndex, i, "client"),
			})
		case 4:
			content = append(content, map[string]any{
				"type":     "thinking",
				"thinking": randomRequestText(caseIndex, messageIndex, i),
			})
		case 5:
			content = append(content, randomToolUseContent(r, caseIndex, messageIndex, i))
		case 6:
			content = append(content, randomToolResultContent(r, caseIndex, messageIndex, i))
		case 7:
			content = append(content, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": "image/png",
					"data":       "ZmFrZV9pbWFnZV9kYXRh",
				},
			})
		case 8:
			content = append(content, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type": "url",
					"url":  "https://example.com/a.png",
				},
			})
		default:
			content = append(content, map[string]any{
				"type": "unknown_type",
				"foo":  "bar",
			})
		}
	}
	return content
}

func randomToolUseContent(r *rand.Rand, caseIndex, messageIndex, contentIndex int) map[string]any {
	item := map[string]any{
		"type": "tool_use",
		"id":   fmt.Sprintf("tool_%d_%d_%d", caseIndex, messageIndex, contentIndex),
		"name": fmt.Sprintf("fn_%d_%d_%d", caseIndex, messageIndex, contentIndex),
	}

	switch r.Intn(6) {
	case 0:
		item["input"] = map[string]any{
			"city": "Paris",
			"id":   caseIndex + messageIndex + contentIndex,
		}
	case 1:
		item["input"] = `{"city":"Paris","unit":"C"}`
	case 2:
		item["input"] = `{"city":"Rome"}`
	case 3:
		item["input"] = []any{"not", "an", "object"}
	case 4:
		item["input"] = nil
	default:
		item["input"] = "{}"
	}
	return item
}

func randomToolResultContent(r *rand.Rand, caseIndex, messageIndex, contentIndex int) map[string]any {
	item := map[string]any{
		"type":        "tool_result",
		"tool_use_id": fmt.Sprintf("lookup-weather-%d-%d", messageIndex, contentIndex),
	}

	switch r.Intn(9) {
	case 0:
		item["content"] = fmt.Sprintf("result-%d-%d-%d", caseIndex, messageIndex, contentIndex)
	case 1:
		item["content"] = map[string]any{
			"ok":   true,
			"code": caseIndex + messageIndex + contentIndex,
		}
	case 2:
		item["content"] = []any{
			map[string]any{"type": "text", "text": "partial"},
			map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": "image/jpeg",
					"data":       "YmluYXJ5",
				},
			},
		}
	case 3:
		item["content"] = []any{
			map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": "image/png",
					"data":       "AAAABBBB",
				},
			},
			map[string]any{
				"type": "image",
				"source": map[string]any{
					"type": "base64",
					"data": "CCCCDDDD",
				},
			},
		}
	case 4:
		item["content"] = map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": "image/webp",
				"data":       "R0lGODlhAQABAIAAAAUEBA==",
			},
		}
	case 5:
		item["content"] = map[string]any{
			"type": "image",
			"source": map[string]any{
				"type": "base64",
			},
		}
	case 6:
		item["content"] = nil
	case 7:
		// missing content
	default:
		item["tool_use_id"] = "a-b"
		item["content"] = []any{
			map[string]any{"type": "text", "text": "edge"},
		}
	}

	return item
}

func randomTools(r *rand.Rand, caseIndex int) ([]any, bool) {
	if r.Intn(100) < 35 {
		return nil, false
	}

	count := r.Intn(4)
	tools := make([]any, 0, count)
	for i := 0; i < count; i++ {
		tool := map[string]any{
			"name":        fmt.Sprintf("tool_%d_%d", caseIndex, i),
			"description": fmt.Sprintf("tool desc %d %d", caseIndex, i),
			"extra_field": "should_be_removed",
		}

		switch r.Intn(4) {
		case 0:
			tool["input_schema"] = map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
				"required": []any{"id"},
			}
		case 1:
			tool["input_schema"] = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		case 2:
			tool["input_schema"] = "not-object"
		default:
			// missing input_schema
		}

		if r.Intn(100) < 35 {
			tool["behavior"] = "NON_BLOCKING"
		}

		tools = append(tools, tool)
	}

	return tools, true
}

func randomThinkingConfig(r *rand.Rand, caseIndex int) (map[string]any, bool) {
	switch r.Intn(6) {
	case 0:
		return map[string]any{
			"type":          "enabled",
			"budget_tokens": float64(32 + (caseIndex % 1024)),
		}, true
	case 1:
		return map[string]any{
			"type": "adaptive",
		}, true
	case 2:
		return map[string]any{
			"type": "auto",
		}, true
	case 3:
		return map[string]any{
			"type":          "enabled",
			"budget_tokens": "not-number",
		}, true
	case 4:
		return map[string]any{
			"type": "disabled",
		}, true
	default:
		return nil, false
	}
}

func randomRequestText(caseIndex, messageIndex, contentIndex int) string {
	switch (caseIndex + messageIndex + contentIndex) % 6 {
	case 0:
		return fmt.Sprintf("text-%d-%d-%d", caseIndex, messageIndex, contentIndex)
	case 1:
		return fmt.Sprintf("line-%d-%d-%d\nnext-line", caseIndex, messageIndex, contentIndex)
	case 2:
		return fmt.Sprintf("quote-%d-%d-%d \"q\" \\\\ path", caseIndex, messageIndex, contentIndex)
	case 3:
		return fmt.Sprintf("tab-%d-%d-%d\tsep", caseIndex, messageIndex, contentIndex)
	case 4:
		return fmt.Sprintf("mix-%d-%d-%d\\n\\t", caseIndex, messageIndex, contentIndex)
	default:
		return ""
	}
}

func validSignature(caseIndex, messageIndex, contentIndex int, label string) string {
	base := fmt.Sprintf("sig_%s_%d_%d_%d_", label, caseIndex, messageIndex, contentIndex)
	for len(base) < 64 {
		base += "abcdef0123456789"
	}
	return base[:64]
}
