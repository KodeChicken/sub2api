package kiro

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/google/uuid"
)

type Request struct {
	ConversationState            ConversationState `json:"conversationState"`
	ProfileARN                   string            `json:"profileArn,omitempty"`
	AdditionalModelRequestFields map[string]any    `json:"additionalModelRequestFields,omitempty"`
}

type ConversationState struct {
	ConversationID      string    `json:"conversationId"`
	AgentContinuationID string    `json:"agentContinuationId,omitempty"`
	AgentTaskType       string    `json:"agentTaskType"`
	ChatTriggerType     string    `json:"chatTriggerType"`
	CurrentMessage      Message   `json:"currentMessage"`
	History             []Message `json:"history,omitempty"`
}

type Message struct {
	User      *UserMessage      `json:"userInputMessage,omitempty"`
	Assistant *AssistantMessage `json:"assistantResponseMessage,omitempty"`
}

type UserMessage struct {
	Content string          `json:"content"`
	ModelID string          `json:"modelId,omitempty"`
	Origin  string          `json:"origin"`
	Context *MessageContext `json:"userInputMessageContext,omitempty"`
	Images  []Image         `json:"images,omitempty"`
}

type MessageContext struct {
	EnvState    map[string]string `json:"envState"`
	Tools       []Tool            `json:"tools,omitempty"`
	ToolResults []ToolResult      `json:"toolResults,omitempty"`
}

type AssistantMessage struct {
	Content  string    `json:"content"`
	ToolUses []ToolUse `json:"toolUses,omitempty"`
}

type Tool struct {
	Specification ToolSpecification `json:"toolSpecification"`
}

type ToolSpecification struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type ToolUse struct {
	ID    string          `json:"toolUseId"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type ToolResult struct {
	ID      string              `json:"toolUseId"`
	Content []map[string]string `json:"content"`
	Status  string              `json:"status"`
	IsError bool                `json:"isError,omitempty"`
}

type Image struct {
	Format string            `json:"format"`
	Source map[string]string `json:"source"`
}

type Conversion struct {
	Request   Request
	ToolNames map[string]string
}

var (
	claudeDateSuffix = regexp.MustCompile(`-\d{8}$`)
	claudeVersion    = regexp.MustCompile(`^(claude-(?:sonnet|opus|haiku|fable|mythos))-(\d+)-(\d+)$`)
	sessionID        = regexp.MustCompile(`(?:session_|session_id[":\s]+)([a-fA-F0-9-]{36})`)
)

func ModelID(model string) (string, error) {
	if len(model) == 0 || len(model) > 256 || strings.IndexFunc(model, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return "", errors.New("invalid Kiro model ID")
	}
	if strings.HasPrefix(strings.ToLower(model), "claude-") {
		model = strings.ToLower(model)
		model = strings.TrimSuffix(strings.TrimSuffix(model, "-thinking"), "-latest")
		model = claudeDateSuffix.ReplaceAllString(model, "")
		model = claudeVersion.ReplaceAllString(model, "$1-$2.$3")
	} else if base := strings.TrimSuffix(strings.ToLower(model), "-thinking"); gptReasoningModel(base) {
		model = base
	}
	return model, nil
}

// Convert preserves the client tool schema; Kiro only receives names short enough for its wire format.
func Convert(req *apicompat.AnthropicRequest, creds Credentials, namespace string) (*Conversion, error) {
	if req == nil || len(req.Messages) == 0 {
		return nil, errors.New("messages must not be empty")
	}
	model, err := ModelID(req.Model)
	if err != nil {
		return nil, err
	}
	origin := "AI_EDITOR"
	if creds.Endpoint == "cli" {
		origin = "KIRO_CLI"
	}
	converted := &Conversion{ToolNames: make(map[string]string)}
	toolNames := make(map[string]string)
	shortName := func(name string) string {
		if existing, ok := toolNames[name]; ok {
			return existing
		}
		short := name
		if len(short) > 63 || strings.IndexFunc(short, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
		}) >= 0 {
			sum := sha256.Sum256([]byte(name))
			short = "tool_" + hex.EncodeToString(sum[:16])
		}
		toolNames[name], converted.ToolNames[short] = short, name
		return short
	}
	var tools []Tool
	declared := make(map[string]bool)
	for _, tool := range req.Tools {
		if tool.Name == "" {
			return nil, errors.New("tool name is required")
		}
		name := shortName(tool.Name)
		if declared[strings.ToLower(name)] {
			return nil, errors.New("Kiro tool names must be unique ignoring case")
		}
		declared[strings.ToLower(name)] = true
		schema := map[string]any{"type": "object", "properties": map[string]any{}}
		if len(tool.InputSchema) > 0 {
			if err := json.Unmarshal(tool.InputSchema, &schema); err != nil || schema == nil {
				return nil, fmt.Errorf("invalid schema for tool %s", tool.Name)
			}
		}
		if schema["type"] == nil {
			schema["type"] = "object"
		}
		delete(schema, "$schema")
		delete(schema, "$id")
		tools = append(tools, Tool{ToolSpecification{
			Name: name, Description: first(tool.Description, "Invoke "+tool.Name),
			InputSchema: map[string]any{"json": schema},
		}})
	}
	var messages []Message
	system, err := contentText(req.System)
	if err != nil {
		return nil, fmt.Errorf("invalid system prompt: %w", err)
	}
	if req.Thinking != nil && (req.Thinking.Type == "enabled" || req.Thinking.Type == "adaptive") {
		system = "<thinking_mode>enabled</thinking_mode>\n<thinking_budget>" +
			fmt.Sprint(max(1024, req.Thinking.BudgetTokens)) + "</thinking_budget>\n" + system
	}
	newUser := func(text string) *UserMessage {
		return &UserMessage{Content: text, ModelID: model, Origin: origin}
	}
	if system != "" {
		messages = append(messages,
			Message{User: newUser("<client_system_prompt>\n" + system + "\n</client_system_prompt>")},
			Message{Assistant: &AssistantMessage{Content: "Understood. I will follow the provided system instructions."}},
		)
	}
	for _, message := range req.Messages {
		var blocks []apicompat.AnthropicContentBlock
		var text string
		if err := json.Unmarshal(message.Content, &text); err == nil {
			blocks = []apicompat.AnthropicContentBlock{{Type: "text", Text: text}}
		} else if err := json.Unmarshal(message.Content, &blocks); err != nil {
			return nil, errors.New("message content must be text or content blocks")
		}
		switch message.Role {
		case "user", "system":
			user := newUser("")
			for _, block := range blocks {
				switch block.Type {
				case "text":
					user.Content = join(user.Content, block.Text)
				case "image":
					img, err := ConvertImage(block.Source)
					if err != nil {
						return nil, err
					}
					user.Images = append(user.Images, img)
				case "tool_result":
					if block.ToolUseID == "" {
						return nil, errors.New("tool_result requires tool_use_id")
					}
					value, err := toolResultContent(block.Content, &user.Images)
					if err != nil {
						return nil, err
					}
					if user.Context == nil {
						user.Context = &MessageContext{}
					}
					status := "success"
					if block.IsError {
						status = "error"
					}
					user.Context.ToolResults = append(user.Context.ToolResults, ToolResult{
						ID: block.ToolUseID, Content: []map[string]string{{"text": value}},
						Status: status, IsError: block.IsError,
					})
				case "web_search_tool_result":
					user.Content = join(user.Content, "Previous web search results: "+string(block.Content))
				default:
					return nil, fmt.Errorf("unsupported user content block: %s", block.Type)
				}
			}
			if message.Role == "system" {
				user.Content = "<system-reminder>\n" + user.Content + "\n</system-reminder>"
			}
			if len(messages) > 0 && messages[len(messages)-1].User != nil {
				prior := messages[len(messages)-1].User
				prior.Content = join(prior.Content, user.Content)
				prior.Images = append(prior.Images, user.Images...)
				if user.Context != nil {
					if prior.Context == nil {
						prior.Context = &MessageContext{}
					}
					prior.Context.ToolResults = append(prior.Context.ToolResults, user.Context.ToolResults...)
				}
			} else {
				messages = append(messages, Message{User: user})
			}
		case "assistant":
			assistant := &AssistantMessage{}
			for _, block := range blocks {
				switch block.Type {
				case "text":
					assistant.Content = join(assistant.Content, block.Text)
				case "thinking":
					if block.Thinking != "" {
						assistant.Content = join(assistant.Content, "<thinking>"+block.Thinking+"</thinking>")
					}
				case "redacted_thinking":
					// Kiro's history wire type has no encrypted-reasoning field.
				case "server_tool_use":
					assistant.Content = join(assistant.Content, "Previous server search: "+string(block.Input))
				case "web_search_tool_result":
					assistant.Content = join(assistant.Content, "Previous web search results: "+string(block.Content))
				case "tool_use":
					if block.ID == "" || block.Name == "" {
						return nil, errors.New("tool_use requires id and name")
					}
					input := block.Input
					if len(input) == 0 {
						input = json.RawMessage("{}")
					}
					if !json.Valid(input) {
						return nil, errors.New("invalid tool_use input")
					}
					name := shortName(block.Name)
					assistant.ToolUses = append(assistant.ToolUses, ToolUse{block.ID, name, input})
					if !declared[strings.ToLower(name)] {
						declared[strings.ToLower(name)] = true
						tools = append(tools, Tool{ToolSpecification{name, "Historical tool " + block.Name,
							map[string]any{"json": map[string]any{"type": "object", "properties": map[string]any{}}}}})
					}
				default:
					return nil, fmt.Errorf("unsupported assistant content block: %s", block.Type)
				}
			}
			if len(messages) == 0 {
				return nil, errors.New("Kiro history must start with a user message")
			}
			if prior := messages[len(messages)-1].Assistant; prior != nil {
				prior.Content = join(prior.Content, assistant.Content)
				prior.ToolUses = append(prior.ToolUses, assistant.ToolUses...)
			} else {
				messages = append(messages, Message{Assistant: assistant})
			}
		default:
			return nil, fmt.Errorf("unsupported message role: %s", message.Role)
		}
	}
	if len(messages) == 0 || messages[len(messages)-1].User == nil {
		return nil, errors.New("Kiro does not support assistant prefill; the last message must be user")
	}
	for i := range messages {
		if messages[i].User == nil {
			continue
		}
		user := messages[i].User
		pending := map[string]bool{}
		if i > 0 && messages[i-1].Assistant != nil {
			for _, use := range messages[i-1].Assistant.ToolUses {
				if pending[use.ID] {
					return nil, errors.New("duplicate tool_use id")
				}
				pending[use.ID] = true
			}
		}
		if user.Context != nil {
			for _, result := range user.Context.ToolResults {
				if !pending[result.ID] {
					return nil, errors.New("tool_result does not match the preceding assistant tool_use")
				}
				delete(pending, result.ID)
			}
		}
		if len(pending) > 0 {
			if user.Context == nil {
				user.Context = &MessageContext{}
			}
			for _, use := range messages[i-1].Assistant.ToolUses {
				if pending[use.ID] {
					user.Context.ToolResults = append(user.Context.ToolResults, ToolResult{ID: use.ID,
						Content: []map[string]string{{"text": "No result was received; this tool call was interrupted."}},
						Status:  "error", IsError: true})
				}
			}
		}
		if user.Context != nil {
			user.Context.EnvState = map[string]string{"operatingSystem": "linux", "currentWorkingDirectory": "/"}
		}
		if creds.Endpoint == "cli" && i < len(messages)-1 {
			user.ModelID = ""
		}
	}
	current := messages[len(messages)-1].User
	if current.Context == nil {
		current.Context = &MessageContext{}
	}
	current.Context.EnvState = map[string]string{"operatingSystem": "linux", "currentWorkingDirectory": "/"}
	current.Context.Tools = tools
	anchor := namespace + "\n" + system
	for _, message := range req.Messages {
		if message.Role == "user" {
			anchor += "\n" + string(message.Content)
			break
		}
	}
	conversationID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(anchor)).String()
	if match := sessionID.FindSubmatch(req.Metadata); len(match) > 1 {
		conversationID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(namespace+string(match[1]))).String()
	}
	state := ConversationState{ConversationID: conversationID, AgentTaskType: "vibe",
		ChatTriggerType: "MANUAL", CurrentMessage: messages[len(messages)-1], History: messages[:len(messages)-1]}
	if creds.Endpoint != "cli" {
		state.AgentContinuationID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(conversationID)).String()
	}
	converted.Request = Request{ConversationState: state, ProfileARN: creds.StreamingProfile()}
	converted.Request.AdditionalModelRequestFields = reasoningFields(req, model)
	return converted, nil
}

func gptReasoningModel(model string) bool {
	return model == "gpt-5.6-sol" || model == "gpt-5.6-terra" || model == "gpt-5.6-luna"
}

func reasoningFields(req *apicompat.AnthropicRequest, model string) map[string]any {
	if req.Thinking != nil && req.Thinking.Type == "disabled" {
		return nil
	}
	supported := gptReasoningModel(model) ||
		model == "claude-opus-4.6" || model == "claude-opus-4.7" ||
		model == "claude-opus-4.8" || model == "claude-sonnet-4.6"
	for _, family := range []string{"fable-5", "mythos-5", "sonnet-5", "opus-5", "haiku-5", "claude-5"} {
		supported = supported || strings.Contains(model, family)
	}
	if !supported || model == "claude-opus-4.6" && (req.Thinking == nil || req.Thinking.Type != "adaptive") {
		return nil
	}
	effort := ""
	if req.OutputConfig != nil {
		effort = req.OutputConfig.Effort
	}
	if effort == "" && req.Thinking != nil && (req.Thinking.Type == "enabled" || req.Thinking.Type == "adaptive") {
		effort = "high"
		if req.Thinking.BudgetTokens > 0 && req.Thinking.BudgetTokens <= 4000 {
			effort = "low"
		} else if req.Thinking.BudgetTokens > 0 && req.Thinking.BudgetTokens <= 16000 {
			effort = "medium"
		} else if req.Thinking.BudgetTokens > 64000 {
			effort = "xhigh"
		}
	}
	if effort != "" {
		effort = strings.ToLower(strings.TrimSpace(effort))
		switch effort {
		case "none", "low", "medium", "high", "xhigh", "max":
		default:
			effort = "high"
		}
		if effort == "none" && !gptReasoningModel(model) ||
			effort == "xhigh" && (model == "claude-opus-4.6" || model == "claude-sonnet-4.6") {
			effort = "high"
		}
		field := "output_config"
		if gptReasoningModel(model) {
			field = "reasoning"
		}
		return map[string]any{field: map[string]string{"effort": effort}}
	}
	return nil
}

func contentText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var blocks []apicompat.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", err
	}
	for _, block := range blocks {
		if block.Type != "text" {
			return "", fmt.Errorf("unsupported text block: %s", block.Type)
		}
		text = join(text, block.Text)
	}
	return text, nil
}

func toolResultContent(raw json.RawMessage, images *[]Image) (string, error) {
	var text string
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var blocks []apicompat.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", errors.New("invalid tool_result content")
	}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			text = join(text, block.Text)
		case "image":
			img, err := ConvertImage(block.Source)
			if err != nil {
				return "", err
			}
			*images = append(*images, img)
			text = join(text, fmt.Sprintf("[Attached image %d]", len(*images)))
		default:
			return "", fmt.Errorf("unsupported tool_result content: %s", block.Type)
		}
	}
	return text, nil
}

func join(left, right string) string {
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	return left + "\n" + right
}
