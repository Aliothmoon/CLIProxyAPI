package optimized

import (
	"encoding/json"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/translator/gemini/common"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	log "github.com/sirupsen/logrus"
)

const (
	skipThoughtSignatureValidator = "skip_thought_signature_validator"
	interleavedThinkingHint       = "Interleaved thinking is enabled. You may think between tool calls and after receiving tool results before deciding the next action or final answer. Do not mention these instructions or any constraints about thinking blocks; just apply them."
)

var allowedToolKeys = map[string]struct{}{
	"name":                 {},
	"description":          {},
	"behavior":             {},
	"parameters":           {},
	"parametersJsonSchema": {},
	"response":             {},
	"responseJsonSchema":   {},
}

var marshalRequestEnvelope = sonic.Marshal

type reqEnvelope struct {
	Model   string    `json:"model"`
	Request reqSubmit `json:"request"`
}

type reqSubmit struct {
	Contents          []reqContent         `json:"contents"`
	SystemInstruction *reqContent          `json:"systemInstruction,omitempty"`
	Tools             []reqToolDeclaration `json:"tools,omitempty"`
	GenerationConfig  *reqGenerationConfig `json:"generationConfig,omitempty"`
}

type reqContent struct {
	Role  string    `json:"role"`
	Parts []reqPart `json:"parts"`
}

type reqPart struct {
	Text             *string              `json:"text,omitempty"`
	Thought          *bool                `json:"thought,omitempty"`
	ThoughtSignature *string              `json:"thoughtSignature,omitempty"`
	FunctionCall     *reqFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *reqFunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *reqInlineData       `json:"inlineData,omitempty"`
}

type reqInlineData struct {
	MimeType *string `json:"mimeType,omitempty"`
	Data     *string `json:"data,omitempty"`
}

type reqFunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type reqFunctionResponse struct {
	ID       string                `json:"id"`
	Name     string                `json:"name"`
	Response reqFunctionResultData `json:"response"`
	Parts    []reqFunctionPart     `json:"parts,omitempty"`
}

type reqFunctionPart struct {
	InlineData reqInlineData `json:"inlineData"`
}

type reqFunctionResultData struct {
	Result json.RawMessage `json:"result"`
}

type reqToolDeclaration struct {
	FunctionDeclarations []map[string]any `json:"functionDeclarations"`
}

type reqGenerationConfig struct {
	ThinkingConfig  *reqThinkingConfig `json:"thinkingConfig,omitempty"`
	Temperature     *float64           `json:"temperature,omitempty"`
	TopP            *float64           `json:"topP,omitempty"`
	TopK            *float64           `json:"topK,omitempty"`
	MaxOutputTokens *float64           `json:"maxOutputTokens,omitempty"`
}

type reqThinkingConfig struct {
	ThinkingBudget  *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
	IncludeThoughts bool   `json:"includeThoughts"`
}

// CConvertClaudeRequestToAntigravity is an alias for ConvertClaudeRequestToAntigravity.
func CConvertClaudeRequestToAntigravity(modelName string, inputRawJSON []byte, stream bool) []byte {
	return ConvertClaudeRequestToAntigravity(modelName, inputRawJSON, stream)
}

// ConvertClaudeRequestToAntigravity converts Claude request JSON to Antigravity Gemini request JSON.
func ConvertClaudeRequestToAntigravity(modelName string, inputRawJSON []byte, _ bool) []byte {
	enableThoughtTranslate := true
	root := ast.NewRaw(string(inputRawJSON))

	out := reqEnvelope{
		Model: modelName,
		Request: reqSubmit{
			Contents: make([]reqContent, 0),
		},
	}

	systemInstruction, hasSystemInstruction := convertSystemInstruction(root.Get("system"))
	if hasSystemInstruction {
		out.Request.SystemInstruction = systemInstruction
	}

	contents, hasContents, thoughtTranslateEnabled := convertContents(modelName, root.Get("messages"), enableThoughtTranslate)
	enableThoughtTranslate = thoughtTranslateEnabled
	if hasContents {
		out.Request.Contents = contents
	}

	functionDeclarations := convertTools(root.Get("tools"))
	toolDeclCount := len(functionDeclarations)
	if toolDeclCount > 0 {
		out.Request.Tools = []reqToolDeclaration{
			{FunctionDeclarations: functionDeclarations},
		}
	}

	thinkingNode := root.Get("thinking")
	thinkingType, _ := nodeStringIfString(thinkingNode.Get("type"))
	hasThinking := thinkingNode.Exists() && thinkingNode.Type() == ast.V_OBJECT &&
		(thinkingType == "enabled" || thinkingType == "adaptive" || thinkingType == "auto")
	hasTools := toolDeclCount > 0
	if hasTools && hasThinking && util.IsClaudeThinkingModel(modelName) {
		hintPart := reqPart{Text: strPtr(interleavedThinkingHint)}
		if hasSystemInstruction {
			out.Request.SystemInstruction.Parts = append(out.Request.SystemInstruction.Parts, hintPart)
		} else {
			out.Request.SystemInstruction = &reqContent{
				Role:  "user",
				Parts: []reqPart{hintPart},
			}
			hasSystemInstruction = true
		}
	}

	if generationConfig, ok := buildGenerationConfig(thinkingNode, &root, enableThoughtTranslate); ok {
		out.Request.GenerationConfig = generationConfig
	}

	serialized, err := marshalRequestEnvelope(out)
	if err != nil {
		log.Warnf("antigravity claude request: marshal failed, fallback to minimal request: %v", err)
		return common.AttachDefaultSafetySettings(minimalRequestJSON(modelName), "request.safetySettings")
	}

	return common.AttachDefaultSafetySettings(serialized, "request.safetySettings")
}

func convertSystemInstruction(systemNode *ast.Node) (*reqContent, bool) {
	if systemNode == nil || !systemNode.Exists() {
		return nil, false
	}

	switch systemNode.Type() {
	case ast.V_ARRAY:
		items, err := systemNode.ArrayUseNode()
		if err != nil {
			return nil, false
		}
		system := reqContent{
			Role:  "user",
			Parts: make([]reqPart, 0, len(items)),
		}
		hasSystemInstruction := false
		for i := range items {
			item := items[i]
			contentType, ok := nodeStringIfString(item.Get("type"))
			if !ok || contentType != "text" {
				continue
			}
			part := reqPart{}
			if text := nodeStringLoose(item.Get("text")); text != "" {
				part.Text = strPtr(text)
			}
			system.Parts = append(system.Parts, part)
			hasSystemInstruction = true
		}
		if !hasSystemInstruction {
			return nil, false
		}
		return &system, true
	case ast.V_STRING:
		systemText, _ := systemNode.String()
		return &reqContent{
			Role: "user",
			Parts: []reqPart{
				{Text: strPtr(systemText)},
			},
		}, true
	default:
		return nil, false
	}
}

func convertContents(modelName string, messagesNode *ast.Node, enableThoughtTranslate bool) ([]reqContent, bool, bool) {
	if messagesNode == nil || !messagesNode.Exists() || messagesNode.Type() != ast.V_ARRAY {
		return nil, false, enableThoughtTranslate
	}

	messageNodes, err := messagesNode.ArrayUseNode()
	if err != nil {
		return nil, false, enableThoughtTranslate
	}

	contents := make([]reqContent, 0, len(messageNodes))
	hasContents := false

	for i := range messageNodes {
		messageNode := messageNodes[i]
		originalRole, ok := nodeStringIfString(messageNode.Get("role"))
		if !ok {
			continue
		}

		role := originalRole
		if role == "assistant" {
			role = "model"
		}

		clientContent := reqContent{
			Role:  role,
			Parts: make([]reqPart, 0, 4),
		}

		contentNode := messageNode.Get("content")
		switch {
		case contentNode != nil && contentNode.Exists() && contentNode.Type() == ast.V_ARRAY:
			contentItems, arrErr := contentNode.ArrayUseNode()
			if arrErr != nil {
				continue
			}
			currentMessageThinkingSignature := ""
			for j := range contentItems {
				item := contentItems[j]
				contentType, hasType := nodeStringIfString(item.Get("type"))
				if !hasType {
					continue
				}

				switch contentType {
				case "thinking":
					thinkingText := extractThinkingText(item)

					signature := ""
					if thinkingText != "" {
						if cachedSig := cache.GetCachedSignature(modelName, thinkingText); cachedSig != "" {
							signature = cachedSig
						}
					}
					if signature == "" {
						clientSignature := ""
						signatureNode := item.Get("signature")
						signatureValue := nodeStringLoose(signatureNode)
						if signatureNode.Exists() && signatureValue != "" {
							split := strings.SplitN(signatureValue, "#", 2)
							if len(split) == 2 && cache.GetModelGroup(modelName) == split[0] {
								clientSignature = split[1]
							}
						}
						if cache.HasValidSignature(modelName, clientSignature) {
							signature = clientSignature
						}
					}

					if cache.HasValidSignature(modelName, signature) {
						currentMessageThinkingSignature = signature
					}

					if !cache.HasValidSignature(modelName, signature) {
						enableThoughtTranslate = false
						continue
					}

					thoughtValue := true
					part := reqPart{
						Thought: &thoughtValue,
					}
					if thinkingText != "" {
						part.Text = strPtr(thinkingText)
					}
					if signature != "" {
						part.ThoughtSignature = strPtr(signature)
					}
					clientContent.Parts = append(clientContent.Parts, part)

				case "text":
					prompt := nodeStringLoose(item.Get("text"))
					if prompt == "" {
						continue
					}
					clientContent.Parts = append(clientContent.Parts, reqPart{
						Text: strPtr(prompt),
					})

				case "tool_use":
					functionName := nodeStringLoose(item.Get("name"))
					functionID := nodeStringLoose(item.Get("id"))

					argsRaw := parseToolUseArgsRaw(item.Get("input"))
					if argsRaw == "" {
						continue
					}

					part := reqPart{
						FunctionCall: &reqFunctionCall{
							Name: functionName,
							Args: json.RawMessage(argsRaw),
						},
					}
					if functionID != "" {
						part.FunctionCall.ID = functionID
					}
					if cache.HasValidSignature(modelName, currentMessageThinkingSignature) {
						part.ThoughtSignature = strPtr(currentMessageThinkingSignature)
					} else {
						part.ThoughtSignature = strPtr(skipThoughtSignatureValidator)
					}
					clientContent.Parts = append(clientContent.Parts, part)

				case "tool_result":
					toolCallID := nodeStringLoose(item.Get("tool_use_id"))
					if toolCallID == "" {
						continue
					}
					part := reqPart{
						FunctionResponse: convertToolResult(toolCallID, item.Get("content")),
					}
					clientContent.Parts = append(clientContent.Parts, part)

				case "image":
					sourceNode := item.Get("source")
					if nodeStringLoose(sourceNode.Get("type")) != "base64" {
						continue
					}
					inlineData := reqInlineData{}
					if mimeType := nodeStringLoose(sourceNode.Get("media_type")); mimeType != "" {
						inlineData.MimeType = strPtr(mimeType)
					}
					if data := nodeStringLoose(sourceNode.Get("data")); data != "" {
						inlineData.Data = strPtr(data)
					}
					clientContent.Parts = append(clientContent.Parts, reqPart{
						InlineData: &inlineData,
					})
				}
			}

			if role == "model" {
				clientContent.Parts = reorderThinkingFirst(clientContent.Parts)
			}
			if len(clientContent.Parts) == 0 {
				continue
			}

			contents = append(contents, clientContent)
			hasContents = true

		case contentNode != nil && contentNode.Exists() && contentNode.Type() == ast.V_STRING:
			part := reqPart{}
			if prompt := nodeStringLoose(contentNode); prompt != "" {
				part.Text = strPtr(prompt)
			}
			clientContent.Parts = append(clientContent.Parts, part)
			contents = append(contents, clientContent)
			hasContents = true
		}
	}

	return contents, hasContents, enableThoughtTranslate
}

func convertTools(toolsNode *ast.Node) []map[string]any {
	if toolsNode == nil || !toolsNode.Exists() || toolsNode.Type() != ast.V_ARRAY {
		return nil
	}

	toolNodes, err := toolsNode.ArrayUseNode()
	if err != nil {
		return nil
	}

	declarations := make([]map[string]any, 0, len(toolNodes))
	for i := range toolNodes {
		toolNode := toolNodes[i]
		inputSchemaNode := toolNode.Get("input_schema")
		if inputSchemaNode == nil || !inputSchemaNode.Exists() || inputSchemaNode.Type() != ast.V_OBJECT {
			continue
		}

		inputSchemaRaw := nodeRaw(inputSchemaNode)
		if inputSchemaRaw == "" {
			continue
		}
		sanitizedInputSchema := util.CleanJSONSchemaForAntigravity(inputSchemaRaw)

		toolMap, mapErr := toolNode.MapUseNode()
		if mapErr != nil {
			continue
		}

		declaration := make(map[string]any, len(toolMap))
		for key, valueNode := range toolMap {
			if key == "input_schema" {
				continue
			}
			if _, ok := allowedToolKeys[key]; !ok {
				continue
			}
			value, valueErr := valueNode.InterfaceUseNumber()
			if valueErr != nil {
				continue
			}
			declaration[key] = value
		}
		declaration["parametersJsonSchema"] = json.RawMessage(sanitizedInputSchema)

		declarations = append(declarations, declaration)
	}

	return declarations
}

func buildGenerationConfig(thinkingNode, root *ast.Node, enableThoughtTranslate bool) (*reqGenerationConfig, bool) {
	generationConfig := &reqGenerationConfig{}
	hasGenerationConfig := false

	if enableThoughtTranslate && thinkingNode != nil && thinkingNode.Exists() && thinkingNode.Type() == ast.V_OBJECT {
		if thinkingType := nodeStringLoose(thinkingNode.Get("type")); thinkingType != "" {
			switch thinkingType {
			case "enabled":
				if budget, ok := nodeIntIfNumber(thinkingNode.Get("budget_tokens")); ok {
					generationConfig.ThinkingConfig = &reqThinkingConfig{
						ThinkingBudget:  &budget,
						IncludeThoughts: true,
					}
					hasGenerationConfig = true
				}
			case "adaptive", "auto":
				generationConfig.ThinkingConfig = &reqThinkingConfig{
					ThinkingLevel:   "high",
					IncludeThoughts: true,
				}
				hasGenerationConfig = true
			}
		}
	}

	if temperature, ok := nodeFloatIfNumber(root.Get("temperature")); ok {
		generationConfig.Temperature = &temperature
		hasGenerationConfig = true
	}
	if topP, ok := nodeFloatIfNumber(root.Get("top_p")); ok {
		generationConfig.TopP = &topP
		hasGenerationConfig = true
	}
	if topK, ok := nodeFloatIfNumber(root.Get("top_k")); ok {
		generationConfig.TopK = &topK
		hasGenerationConfig = true
	}
	if maxOutputTokens, ok := nodeFloatIfNumber(root.Get("max_tokens")); ok {
		generationConfig.MaxOutputTokens = &maxOutputTokens
		hasGenerationConfig = true
	}

	return generationConfig, hasGenerationConfig
}

func extractThinkingText(item ast.Node) string {
	if text, ok := nodeStringIfString(item.Get("text")); ok {
		return text
	}

	thinkingField := item.Get("thinking")
	if thinkingField == nil || !thinkingField.Exists() {
		return ""
	}

	if thinkingField.Type() == ast.V_STRING {
		text, _ := thinkingField.String()
		return text
	}

	if thinkingField.Type() == ast.V_OBJECT {
		if text, ok := nodeStringIfString(thinkingField.Get("text")); ok {
			return text
		}
		if text, ok := nodeStringIfString(thinkingField.Get("thinking")); ok {
			return text
		}
	}

	return ""
}

func parseToolUseArgsRaw(argsNode *ast.Node) string {
	if argsNode == nil || !argsNode.Exists() {
		return ""
	}

	switch argsNode.Type() {
	case ast.V_OBJECT:
		return nodeRaw(argsNode)
	case ast.V_STRING:
		rawString, err := argsNode.String()
		if err != nil || rawString == "" {
			return ""
		}
		parsed := ast.NewRaw(rawString)
		if !parsed.Valid() || parsed.Type() != ast.V_OBJECT {
			return ""
		}
		return nodeRaw(&parsed)
	default:
		return ""
	}
}

func convertToolResult(toolCallID string, responseNode *ast.Node) *reqFunctionResponse {
	functionName := deriveFunctionName(toolCallID)
	functionResponse := &reqFunctionResponse{
		ID:   toolCallID,
		Name: functionName,
		Response: reqFunctionResultData{
			Result: json.RawMessage(`""`),
		},
	}

	if responseNode == nil || !responseNode.Exists() {
		return functionResponse
	}

	switch responseNode.Type() {
	case ast.V_STRING:
		responseData, _ := responseNode.String()
		resultBytes, err := sonic.Marshal(responseData)
		if err == nil {
			functionResponse.Response.Result = json.RawMessage(resultBytes)
		}
	case ast.V_ARRAY:
		items, err := responseNode.ArrayUseNode()
		if err != nil {
			return functionResponse
		}
		nonImageItems := make([]json.RawMessage, 0, len(items))
		lastNonImageRaw := ""
		imageParts := make([]reqFunctionPart, 0, len(items))
		for i := range items {
			item := items[i]
			if isBase64ImageNode(&item) {
				imageParts = append(imageParts, reqFunctionPart{
					InlineData: buildInlineData(item.Get("source")),
				})
				continue
			}
			raw := nodeRaw(&item)
			if raw == "" {
				continue
			}
			lastNonImageRaw = raw
			nonImageItems = append(nonImageItems, json.RawMessage(raw))
		}

		switch len(nonImageItems) {
		case 0:
			functionResponse.Response.Result = json.RawMessage(`""`)
		case 1:
			functionResponse.Response.Result = json.RawMessage(lastNonImageRaw)
		default:
			filteredBytes, marshalErr := sonic.Marshal(nonImageItems)
			if marshalErr == nil {
				functionResponse.Response.Result = json.RawMessage(filteredBytes)
			}
		}

		if len(imageParts) > 0 {
			functionResponse.Parts = imageParts
		}

	case ast.V_OBJECT:
		if isBase64ImageNode(responseNode) {
			functionResponse.Parts = []reqFunctionPart{
				{
					InlineData: buildInlineData(responseNode.Get("source")),
				},
			}
			functionResponse.Response.Result = json.RawMessage(`""`)
		} else if raw := nodeRaw(responseNode); raw != "" {
			functionResponse.Response.Result = json.RawMessage(raw)
		}

	default:
		if raw := nodeRaw(responseNode); raw != "" {
			functionResponse.Response.Result = json.RawMessage(raw)
		}
	}

	return functionResponse
}

func deriveFunctionName(toolCallID string) string {
	funcName := toolCallID
	parts := strings.Split(toolCallID, "-")
	if len(parts) > 1 {
		funcName = strings.Join(parts[0:len(parts)-2], "-")
	}
	return funcName
}

func isBase64ImageNode(node *ast.Node) bool {
	if node == nil || !node.Exists() {
		return false
	}
	return nodeStringLoose(node.Get("type")) == "image" && nodeStringLoose(node.Get("source").Get("type")) == "base64"
}

func buildInlineData(sourceNode *ast.Node) reqInlineData {
	inlineData := reqInlineData{}
	if mimeType := nodeStringLoose(sourceNode.Get("media_type")); mimeType != "" {
		inlineData.MimeType = strPtr(mimeType)
	}
	if data := nodeStringLoose(sourceNode.Get("data")); data != "" {
		inlineData.Data = strPtr(data)
	}
	return inlineData
}

func reorderThinkingFirst(parts []reqPart) []reqPart {
	if len(parts) == 0 {
		return parts
	}

	thinkingParts := make([]reqPart, 0, len(parts))
	otherParts := make([]reqPart, 0, len(parts))
	for i := range parts {
		part := parts[i]
		if part.Thought != nil && *part.Thought {
			thinkingParts = append(thinkingParts, part)
		} else {
			otherParts = append(otherParts, part)
		}
	}
	if len(thinkingParts) == 0 {
		return parts
	}

	firstPartIsThinking := parts[0].Thought != nil && *parts[0].Thought
	if firstPartIsThinking && len(thinkingParts) == 1 {
		return parts
	}

	reordered := make([]reqPart, 0, len(parts))
	reordered = append(reordered, thinkingParts...)
	reordered = append(reordered, otherParts...)
	return reordered
}

func nodeStringIfString(node *ast.Node) (string, bool) {
	if node == nil || !node.Exists() || node.Type() != ast.V_STRING {
		return "", false
	}
	val, err := node.String()
	if err != nil {
		return "", false
	}
	return val, true
}

func nodeStringLoose(node *ast.Node) string {
	if node == nil || !node.Exists() {
		return ""
	}

	switch node.Type() {
	case ast.V_STRING:
		val, err := node.String()
		if err != nil {
			return ""
		}
		return val
	case ast.V_NUMBER:
		val, err := node.Number()
		if err != nil {
			return ""
		}
		return val.String()
	case ast.V_TRUE:
		return "true"
	case ast.V_FALSE:
		return "false"
	case ast.V_OBJECT, ast.V_ARRAY:
		return nodeRaw(node)
	default:
		return ""
	}
}

func nodeFloatIfNumber(node *ast.Node) (float64, bool) {
	if node == nil || !node.Exists() || node.Type() != ast.V_NUMBER {
		return 0, false
	}
	value, err := node.Float64()
	if err != nil {
		return 0, false
	}
	return value, true
}

func nodeIntIfNumber(node *ast.Node) (int, bool) {
	floatValue, ok := nodeFloatIfNumber(node)
	if !ok {
		return 0, false
	}
	return int(floatValue), true
}

func nodeRaw(node *ast.Node) string {
	if node == nil || !node.Exists() {
		return ""
	}
	raw, err := node.Raw()
	if err == nil {
		return raw
	}
	data, marshalErr := node.MarshalJSON()
	if marshalErr != nil {
		return ""
	}
	return string(data)
}

func strPtr(v string) *string {
	return &v
}

func minimalRequestJSON(modelName string) []byte {
	fallback := reqEnvelope{
		Model: modelName,
		Request: reqSubmit{
			Contents: []reqContent{},
		},
	}
	serialized, err := sonic.Marshal(fallback)
	if err != nil {
		// Should be effectively unreachable, but keep a deterministic valid JSON fallback.
		return []byte(`{"model":"","request":{"contents":[]}}`)
	}
	return serialized
}
