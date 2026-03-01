package optimized

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
	log "github.com/sirupsen/logrus"
)

// Params stores stream conversion state across chunks.
type Params struct {
	HasFirstResponse     bool
	ResponseType         int
	ResponseIndex        int
	HasFinishReason      bool
	FinishReason         string
	HasUsageMetadata     bool
	PromptTokenCount     int64
	CandidatesTokenCount int64
	ThoughtsTokenCount   int64
	TotalTokenCount      int64
	CachedTokenCount     int64
	HasSentFinalEvents   bool
	HasToolUse           bool
	HasContent           bool
	ModelName            string

	CurrentThinkingText strings.Builder
}

var toolUseIDCounter uint64

type requestEnvelope struct {
	Model string `json:"model"`
}

type antigravityEnvelope struct {
	Response antigravityResponse `json:"response"`
}

type antigravityResponse struct {
	CPAUsageMetadata *usageMetadata         `json:"cpaUsageMetadata"`
	UsageMetadata    *usageMetadata         `json:"usageMetadata"`
	ModelVersion     *string                `json:"modelVersion"`
	ResponseID       *string                `json:"responseId"`
	Candidates       []antigravityCandidate `json:"candidates"`
}

type usageMetadata struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
	TotalTokenCount         int64 `json:"totalTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
}

type antigravityCandidate struct {
	FinishReason *string            `json:"finishReason"`
	Content      antigravityContent `json:"content"`
}

type antigravityContent struct {
	Parts []antigravityPart `json:"parts"`
}

type antigravityPart struct {
	Text                  *string                  `json:"text"`
	Thought               bool                     `json:"thought"`
	ThoughtSignature      *string                  `json:"thoughtSignature"`
	ThoughtSignatureSnake *string                  `json:"thought_signature"`
	FunctionCall          *antigravityFunctionCall `json:"functionCall"`
}

type antigravityFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type claudeUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

type claudeTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeThinkingBlock struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking"`
	Signature string `json:"signature,omitempty"`
}

type claudeToolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type claudeNonStreamResponse struct {
	ID           string       `json:"id"`
	Type         string       `json:"type"`
	Role         string       `json:"role"`
	Model        string       `json:"model"`
	Content      []any        `json:"content"`
	StopReason   any          `json:"stop_reason"`
	StopSequence any          `json:"stop_sequence"`
	Usage        *claudeUsage `json:"usage,omitempty"`
}

// CConvertAntigravityResponseToClaude is an alias for ConvertAntigravityResponseToClaude.
func CConvertAntigravityResponseToClaude(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []string {
	return ConvertAntigravityResponseToClaude(ctx, modelName, originalRequestRawJSON, requestRawJSON, rawJSON, param)
}

// ConvertAntigravityResponseToClaude converts antigravity stream chunks to Claude SSE chunks.
func ConvertAntigravityResponseToClaude(_ context.Context, _ string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []string {
	_ = originalRequestRawJSON
	if *param == nil {
		*param = &Params{
			HasFirstResponse: false,
			ResponseType:     0,
			ResponseIndex:    0,
			ModelName:        parseRequestModel(requestRawJSON),
		}
	}

	params := (*param).(*Params)
	if params.ModelName == "" {
		params.ModelName = parseRequestModel(requestRawJSON)
	}

	if bytes.Equal(rawJSON, []byte("[DONE]")) {
		if !params.HasContent {
			return []string{}
		}
		var out strings.Builder
		appendFinalEvents(params, &out, true)
		appendMessageStopEvent(&out)
		return []string{out.String()}
	}

	var payload antigravityEnvelope
	if err := sonic.Unmarshal(rawJSON, &payload); err != nil {
		log.Warnf(
			"antigravity claude response(stream): failed to unmarshal chunk len=%d: %v, raw=%q",
			len(rawJSON),
			err,
			previewRawForLog(rawJSON, 256),
		)
		// Keep behavior non-fatal and legacy-compatible: continue with zero-value payload.
	}

	var out strings.Builder
	if !params.HasFirstResponse {
		appendMessageStartEvent(&out, payload.Response)
		params.HasFirstResponse = true
	}

	candidate := firstCandidate(&payload.Response)
	if candidate != nil {
		for i := range candidate.Content.Parts {
			part := candidate.Content.Parts[i]
			if part.Text != nil {
				partText := *part.Text
				if part.Thought {
					signature := extractThoughtSignatureStream(part)
					if signature != "" {
						if params.CurrentThinkingText.Len() > 0 {
							cache.CacheSignature(params.ModelName, params.CurrentThinkingText.String(), signature)
							params.CurrentThinkingText.Reset()
						}
						appendSignatureDeltaEvent(
							&out,
							params.ResponseIndex,
							fmt.Sprintf("%s#%s", cache.GetModelGroup(params.ModelName), signature),
						)
						params.HasContent = true
					} else if params.ResponseType == 2 {
						params.CurrentThinkingText.WriteString(partText)
						appendThinkingDeltaEvent(&out, params.ResponseIndex, partText)
						params.HasContent = true
					} else {
						if params.ResponseType != 0 {
							appendContentBlockStopEvent(&out, params.ResponseIndex)
							params.ResponseIndex++
						}
						appendThinkingStartEvent(&out, params.ResponseIndex)
						appendThinkingDeltaEvent(&out, params.ResponseIndex, partText)
						params.ResponseType = 2
						params.HasContent = true
						params.CurrentThinkingText.Reset()
						params.CurrentThinkingText.WriteString(partText)
					}
					continue
				}

				finishReasonExists := candidate.FinishReason != nil
				if partText != "" || !finishReasonExists {
					if params.ResponseType == 1 {
						appendTextDeltaEvent(&out, params.ResponseIndex, partText)
						params.HasContent = true
					} else {
						if params.ResponseType != 0 {
							appendContentBlockStopEvent(&out, params.ResponseIndex)
							params.ResponseIndex++
						}
						if partText != "" {
							appendTextStartEvent(&out, params.ResponseIndex)
							appendTextDeltaEvent(&out, params.ResponseIndex, partText)
							params.ResponseType = 1
							params.HasContent = true
						}
					}
				}
				continue
			}

			if part.FunctionCall != nil {
				params.HasToolUse = true
				fcName := part.FunctionCall.Name

				if params.ResponseType == 3 {
					appendContentBlockStopEvent(&out, params.ResponseIndex)
					params.ResponseIndex++
					params.ResponseType = 0
				}
				if params.ResponseType != 0 {
					appendContentBlockStopEvent(&out, params.ResponseIndex)
					params.ResponseIndex++
				}

				toolID := fmt.Sprintf("%s-%d-%d", fcName, time.Now().UnixNano(), atomic.AddUint64(&toolUseIDCounter, 1))
				appendToolUseStartEvent(&out, params.ResponseIndex, toolID, fcName)
				if len(part.FunctionCall.Args) > 0 {
					appendInputJSONDeltaEvent(&out, params.ResponseIndex, string(part.FunctionCall.Args))
				}

				params.ResponseType = 3
				params.HasContent = true
			}
		}

		if candidate.FinishReason != nil {
			params.HasFinishReason = true
			params.FinishReason = *candidate.FinishReason
		}
	}

	if payload.Response.UsageMetadata != nil {
		params.HasUsageMetadata = true
		params.CachedTokenCount = payload.Response.UsageMetadata.CachedContentTokenCount
		params.PromptTokenCount = payload.Response.UsageMetadata.PromptTokenCount - params.CachedTokenCount
		params.CandidatesTokenCount = payload.Response.UsageMetadata.CandidatesTokenCount
		params.ThoughtsTokenCount = payload.Response.UsageMetadata.ThoughtsTokenCount
		params.TotalTokenCount = payload.Response.UsageMetadata.TotalTokenCount
		if params.CandidatesTokenCount == 0 && params.TotalTokenCount > 0 {
			params.CandidatesTokenCount = params.TotalTokenCount - params.PromptTokenCount - params.ThoughtsTokenCount
			if params.CandidatesTokenCount < 0 {
				params.CandidatesTokenCount = 0
			}
		}
	}

	if params.HasUsageMetadata && params.HasFinishReason {
		appendFinalEvents(params, &out, false)
	}

	return []string{out.String()}
}

func appendFinalEvents(params *Params, out *strings.Builder, force bool) {
	if params.HasSentFinalEvents {
		return
	}
	if !params.HasUsageMetadata && !force {
		return
	}
	if !params.HasContent {
		return
	}

	if params.ResponseType != 0 {
		appendContentBlockStopEvent(out, params.ResponseIndex)
		params.ResponseType = 0
	}

	usageOutputTokens := params.CandidatesTokenCount + params.ThoughtsTokenCount
	if usageOutputTokens == 0 && params.TotalTokenCount > 0 {
		usageOutputTokens = params.TotalTokenCount - params.PromptTokenCount
		if usageOutputTokens < 0 {
			usageOutputTokens = 0
		}
	}

	appendMessageDeltaEvent(
		out,
		resolveStopReason(params),
		params.PromptTokenCount,
		usageOutputTokens,
		params.CachedTokenCount,
	)
	params.HasSentFinalEvents = true
}

func resolveStopReason(params *Params) string {
	if params.HasToolUse {
		return "tool_use"
	}
	switch params.FinishReason {
	case "MAX_TOKENS":
		return "max_tokens"
	case "STOP", "FINISH_REASON_UNSPECIFIED", "UNKNOWN":
		return "end_turn"
	default:
		return "end_turn"
	}
}

// CConvertAntigravityResponseToClaudeNonStream is an alias for ConvertAntigravityResponseToClaudeNonStream.
func CConvertAntigravityResponseToClaudeNonStream(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) string {
	return ConvertAntigravityResponseToClaudeNonStream(ctx, modelName, originalRequestRawJSON, requestRawJSON, rawJSON, param)
}

// ConvertAntigravityResponseToClaudeNonStream converts a non-stream antigravity response to Claude format.
func ConvertAntigravityResponseToClaudeNonStream(_ context.Context, _ string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, _ *any) string {
	_ = originalRequestRawJSON
	modelName := parseRequestModel(requestRawJSON)

	var payload antigravityEnvelope
	if err := sonic.Unmarshal(rawJSON, &payload); err != nil {
		log.Warnf(
			"antigravity claude response(non-stream): failed to unmarshal payload len=%d: %v, raw=%q",
			len(rawJSON),
			err,
			previewRawForLog(rawJSON, 256),
		)
		// Keep behavior non-fatal and legacy-compatible: continue with zero-value payload.
	}

	var promptTokens, candidateTokens, thoughtTokens, totalTokens, cachedTokens int64
	hasUsageMetadata := payload.Response.UsageMetadata != nil
	if hasUsageMetadata {
		promptTokens = payload.Response.UsageMetadata.PromptTokenCount
		candidateTokens = payload.Response.UsageMetadata.CandidatesTokenCount
		thoughtTokens = payload.Response.UsageMetadata.ThoughtsTokenCount
		totalTokens = payload.Response.UsageMetadata.TotalTokenCount
		cachedTokens = payload.Response.UsageMetadata.CachedContentTokenCount
	}

	outputTokens := candidateTokens + thoughtTokens
	if outputTokens == 0 && totalTokens > 0 {
		outputTokens = totalTokens - promptTokens
		if outputTokens < 0 {
			outputTokens = 0
		}
	}

	response := claudeNonStreamResponse{
		ID:           derefString(payload.Response.ResponseID),
		Type:         "message",
		Role:         "assistant",
		Model:        derefString(payload.Response.ModelVersion),
		Content:      nil,
		StopReason:   nil,
		StopSequence: nil,
		Usage: &claudeUsage{
			InputTokens:  promptTokens,
			OutputTokens: outputTokens,
		},
	}

	var blocks []any
	textBuilder := strings.Builder{}
	thinkingBuilder := strings.Builder{}
	thinkingSignature := ""
	toolIDCounter := 0
	hasToolCall := false

	flushText := func() {
		if textBuilder.Len() == 0 {
			return
		}
		blocks = append(blocks, claudeTextBlock{
			Type: "text",
			Text: textBuilder.String(),
		})
		textBuilder.Reset()
	}
	flushThinking := func() {
		if thinkingBuilder.Len() == 0 && thinkingSignature == "" {
			return
		}
		block := claudeThinkingBlock{
			Type:     "thinking",
			Thinking: thinkingBuilder.String(),
		}
		if thinkingSignature != "" {
			block.Signature = fmt.Sprintf("%s#%s", cache.GetModelGroup(modelName), thinkingSignature)
		}
		blocks = append(blocks, block)
		thinkingBuilder.Reset()
		thinkingSignature = ""
	}

	candidate := firstCandidate(&payload.Response)
	if candidate != nil {
		for i := range candidate.Content.Parts {
			part := candidate.Content.Parts[i]
			isThought := part.Thought
			if isThought {
				if signature := extractThoughtSignature(part); signature != "" {
					thinkingSignature = signature
				}
			}

			if part.Text != nil && *part.Text != "" {
				if isThought {
					flushText()
					thinkingBuilder.WriteString(*part.Text)
					continue
				}
				flushThinking()
				textBuilder.WriteString(*part.Text)
				continue
			}

			if part.FunctionCall != nil {
				flushThinking()
				flushText()
				hasToolCall = true

				toolIDCounter++
				tool := claudeToolUseBlock{
					Type:  "tool_use",
					ID:    fmt.Sprintf("tool_%d", toolIDCounter),
					Name:  part.FunctionCall.Name,
					Input: json.RawMessage(`{}`),
				}
				if len(part.FunctionCall.Args) > 0 && isJSONObject(part.FunctionCall.Args) {
					tool.Input = append(json.RawMessage(nil), part.FunctionCall.Args...)
				}
				blocks = append(blocks, tool)
			}
		}
	}

	flushThinking()
	flushText()
	if len(blocks) > 0 {
		response.Content = blocks
	}
	response.StopReason = convertStopReason(candidate, hasToolCall)

	serialized, err := sonic.Marshal(response)
	if err != nil {
		return string(rawJSON)
	}

	root := ast.NewRaw(string(serialized))
	if cachedTokens > 0 {
		usage := root.Get("usage")
		if usage != nil && usage.Exists() {
			_, _ = usage.Set("cache_read_input_tokens", newNumberNode(cachedTokens))
		}
	}
	if promptTokens == 0 && outputTokens == 0 && !hasUsageMetadata {
		_, _ = root.Unset("usage")
	}

	finalJSON, err := root.MarshalJSON()
	if err != nil {
		return string(serialized)
	}
	return string(finalJSON)
}

func convertStopReason(candidate *antigravityCandidate, hasToolCall bool) string {
	if hasToolCall {
		return "tool_use"
	}
	if candidate == nil || candidate.FinishReason == nil {
		return "end_turn"
	}
	switch *candidate.FinishReason {
	case "MAX_TOKENS":
		return "max_tokens"
	case "STOP", "FINISH_REASON_UNSPECIFIED", "UNKNOWN":
		return "end_turn"
	default:
		return "end_turn"
	}
}

func appendMessageStartEvent(out *strings.Builder, response antigravityResponse) {
	messageID := "msg_1nZdL29xx5MUA1yADyHTEsnR8uuvGzszyY"
	model := "claude-3-5-sonnet-20241022"
	inputTokens := int64(0)
	outputTokens := int64(0)
	if response.ResponseID != nil {
		messageID = *response.ResponseID
	}
	if response.ModelVersion != nil {
		model = *response.ModelVersion
	}
	if response.CPAUsageMetadata != nil {
		inputTokens = response.CPAUsageMetadata.PromptTokenCount
		outputTokens = response.CPAUsageMetadata.CandidatesTokenCount
	}

	appendSSEHeader(out, "message_start")
	out.WriteString(`{"type":"message_start","message":{"id":`)
	writeJSONString(out, messageID)
	out.WriteString(`,"type":"message","role":"assistant","content":[],"model":`)
	writeJSONString(out, model)
	out.WriteString(`,"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":`)
	out.WriteString(strconv.FormatInt(inputTokens, 10))
	out.WriteString(`,"output_tokens":`)
	out.WriteString(strconv.FormatInt(outputTokens, 10))
	out.WriteString(`}}}`)
	appendSSEFooter(out)
}

func appendContentBlockStopEvent(out *strings.Builder, index int) {
	appendSSEHeader(out, "content_block_stop")
	out.WriteString(`{"type":"content_block_stop","index":`)
	out.WriteString(strconv.Itoa(index))
	out.WriteString(`}`)
	appendSSEFooter(out)
}

func appendThinkingStartEvent(out *strings.Builder, index int) {
	appendSSEHeader(out, "content_block_start")
	out.WriteString(`{"type":"content_block_start","index":`)
	out.WriteString(strconv.Itoa(index))
	out.WriteString(`,"content_block":{"type":"thinking","thinking":""}}`)
	appendSSEFooter(out)
}

func appendTextStartEvent(out *strings.Builder, index int) {
	appendSSEHeader(out, "content_block_start")
	out.WriteString(`{"type":"content_block_start","index":`)
	out.WriteString(strconv.Itoa(index))
	out.WriteString(`,"content_block":{"type":"text","text":""}}`)
	appendSSEFooter(out)
}

func appendToolUseStartEvent(out *strings.Builder, index int, id, name string) {
	appendSSEHeader(out, "content_block_start")
	out.WriteString(`{"type":"content_block_start","index":`)
	out.WriteString(strconv.Itoa(index))
	out.WriteString(`,"content_block":{"type":"tool_use","id":`)
	writeJSONString(out, id)
	out.WriteString(`,"name":`)
	writeJSONString(out, name)
	out.WriteString(`,"input":{}}}`)
	appendSSEFooter(out)
}

func appendTextDeltaEvent(out *strings.Builder, index int, text string) {
	appendSSEHeader(out, "content_block_delta")
	out.WriteString(`{"type":"content_block_delta","index":`)
	out.WriteString(strconv.Itoa(index))
	out.WriteString(`,"delta":{"type":"text_delta","text":`)
	writeJSONString(out, text)
	out.WriteString(`}}`)
	appendSSEFooter(out)
}

func appendThinkingDeltaEvent(out *strings.Builder, index int, thinking string) {
	appendSSEHeader(out, "content_block_delta")
	out.WriteString(`{"type":"content_block_delta","index":`)
	out.WriteString(strconv.Itoa(index))
	out.WriteString(`,"delta":{"type":"thinking_delta","thinking":`)
	writeJSONString(out, thinking)
	out.WriteString(`}}`)
	appendSSEFooter(out)
}

func appendSignatureDeltaEvent(out *strings.Builder, index int, signature string) {
	appendSSEHeader(out, "content_block_delta")
	out.WriteString(`{"type":"content_block_delta","index":`)
	out.WriteString(strconv.Itoa(index))
	out.WriteString(`,"delta":{"type":"signature_delta","signature":`)
	writeJSONString(out, signature)
	out.WriteString(`}}`)
	appendSSEFooter(out)
}

func appendInputJSONDeltaEvent(out *strings.Builder, index int, partialJSON string) {
	appendSSEHeader(out, "content_block_delta")
	out.WriteString(`{"type":"content_block_delta","index":`)
	out.WriteString(strconv.Itoa(index))
	out.WriteString(`,"delta":{"type":"input_json_delta","partial_json":`)
	writeJSONString(out, partialJSON)
	out.WriteString(`}}`)
	appendSSEFooter(out)
}

func appendMessageDeltaEvent(out *strings.Builder, stopReason string, inputTokens, outputTokens, cachedTokens int64) {
	appendSSEHeader(out, "message_delta")
	out.WriteString(`{"type":"message_delta","delta":{"stop_reason":`)
	writeJSONString(out, stopReason)
	out.WriteString(`,"stop_sequence":null},"usage":{"input_tokens":`)
	out.WriteString(strconv.FormatInt(inputTokens, 10))
	out.WriteString(`,"output_tokens":`)
	out.WriteString(strconv.FormatInt(outputTokens, 10))
	if cachedTokens > 0 {
		out.WriteString(`,"cache_read_input_tokens":`)
		out.WriteString(strconv.FormatInt(cachedTokens, 10))
	}
	out.WriteString(`}}`)
	appendSSEFooter(out)
}

func appendMessageStopEvent(out *strings.Builder) {
	appendSSEHeader(out, "message_stop")
	out.WriteString(`{"type":"message_stop"}`)
	appendSSEFooter(out)
}

func appendSSEHeader(out *strings.Builder, eventName string) {
	out.WriteString("event: ")
	out.WriteString(eventName)
	out.WriteString("\n")
	out.WriteString("data: ")
}

func appendSSEFooter(out *strings.Builder) {
	out.WriteString("\n\n\n")
}

func writeJSONString(out *strings.Builder, value string) {
	encoded, err := sonic.Marshal(value)
	if err != nil {
		// String marshaling should not fail, but keep JSON output valid.
		out.WriteString(`""`)
		return
	}
	_, _ = out.Write(encoded)
}

func parseRequestModel(requestRawJSON []byte) string {
	var req requestEnvelope
	if err := sonic.Unmarshal(requestRawJSON, &req); err != nil {
		return ""
	}
	return req.Model
}

func firstCandidate(response *antigravityResponse) *antigravityCandidate {
	if response == nil || len(response.Candidates) == 0 {
		return nil
	}
	return &response.Candidates[0]
}

func extractThoughtSignature(part antigravityPart) string {
	if part.ThoughtSignature != nil && *part.ThoughtSignature != "" {
		return *part.ThoughtSignature
	}
	if part.ThoughtSignatureSnake != nil && *part.ThoughtSignatureSnake != "" {
		return *part.ThoughtSignatureSnake
	}
	return ""
}

func extractThoughtSignatureStream(part antigravityPart) string {
	if part.ThoughtSignature != nil && *part.ThoughtSignature != "" {
		return *part.ThoughtSignature
	}
	return ""
}

func newNumberNode(value int64) ast.Node {
	return ast.NewNumber(strconv.FormatInt(value, 10))
}

func derefString(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

func isJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	node := ast.NewRaw(string(raw))
	return node.Valid() && node.Type() == ast.V_OBJECT
}

func previewRawForLog(raw []byte, max int) string {
	if max <= 0 || len(raw) <= max {
		return string(raw)
	}
	return string(raw[:max]) + "...(truncated)"
}
