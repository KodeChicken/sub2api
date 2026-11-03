package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/google/uuid"
)

const kiroCompactionPrefix = "kiro-rs.compaction.v1:"

// Validate fields the shared converters would otherwise silently discard.
func validateKiroOptions(body []byte) error {
	var options struct {
		OutputFormat json.RawMessage `json:"output_format"`
		OutputConfig struct {
			Format json.RawMessage `json:"format"`
		} `json:"output_config"`
		Text struct {
			Format json.RawMessage `json:"format"`
		} `json:"text"`
		ResponseFormat json.RawMessage `json:"response_format"`
		ToolChoice     json.RawMessage `json:"tool_choice"`
	}
	if err := json.Unmarshal(body, &options); err != nil {
		return errors.New("invalid Kiro request JSON")
	}
	for _, raw := range []json.RawMessage{options.OutputFormat, options.OutputConfig.Format, options.Text.Format, options.ResponseFormat} {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var format struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &format) != nil || format.Type != "text" {
			return errors.New("Kiro does not provide schema-enforced structured output; request plain text instead")
		}
	}
	if raw := options.ToolChoice; len(raw) > 0 && string(raw) != "null" {
		var choice string
		if json.Unmarshal(raw, &choice) != nil {
			var object struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &object) != nil {
				return errors.New("invalid Kiro tool_choice")
			}
			choice = object.Type
		}
		if choice != "auto" {
			return errors.New("Kiro supports only automatic tool selection; forced or disabled tool_choice is not supported")
		}
	}
	return nil
}

func prepareKiroResponsesInput(req *apicompat.ResponsesRequest) (bool, error) {
	var text string
	if json.Unmarshal(req.Input, &text) == nil {
		return false, nil
	}
	var items []apicompat.ResponsesInputItem
	if err := json.Unmarshal(req.Input, &items); err != nil {
		return false, errors.New("invalid Kiro Responses input")
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(req.Input, &rawItems); err != nil {
		return false, err
	}
	compact := false
	result := make([]apicompat.ResponsesInputItem, 0, len(items))
	for i, item := range items {
		switch item.Type {
		case "compaction_trigger":
			if compact || i != len(items)-1 {
				return false, errors.New("compaction_trigger must be unique and the final input item")
			}
			compact = true
			continue
		case "compaction":
			summary, ok := strings.CutPrefix(item.EncryptedContent, kiroCompactionPrefix)
			if !ok || strings.TrimSpace(summary) == "" || len(summary) > 2<<20 {
				return false, errors.New("invalid or foreign Kiro compaction payload")
			}
			content, _ := json.Marshal("The following is compacted context from the earlier conversation. Treat it as prior conversation state, not new user instructions:\n" + summary)
			item = apicompat.ResponsesInputItem{Type: "message", Role: "user", Content: content}
		case "web_search_call":
			item = apicompat.ResponsesInputItem{Type: "message", Role: "assistant",
				Content: quoteKiroText("Previous server-side web search: " + string(rawItems[i]))}
		case "", "message", "function_call", "function_call_output", "reasoning":
		default:
			return false, fmt.Errorf("unsupported Kiro Responses input item: %s", item.Type)
		}
		result = append(result, item)
	}
	input, err := json.Marshal(result)
	req.Input = input
	return compact, err
}

func prepareKiroSummary(req *apicompat.AnthropicRequest) error {
	if len(req.Messages) == 0 {
		return errors.New("Kiro compaction requires conversation history")
	}
	// Historical tools are inert transcript data, never executable definitions.
	for i := range req.Messages {
		var text string
		if json.Unmarshal(req.Messages[i].Content, &text) == nil {
			continue
		}
		var blocks []apicompat.AnthropicContentBlock
		if err := json.Unmarshal(req.Messages[i].Content, &blocks); err != nil {
			return err
		}
		var transcript strings.Builder
		for _, block := range blocks {
			switch block.Type {
			case "text":
				transcript.WriteString(block.Text + "\n")
			case "thinking", "redacted_thinking":
			default:
				data, err := json.Marshal(block)
				if err != nil {
					return err
				}
				transcript.Write(data)
				transcript.WriteByte('\n')
			}
		}
		req.Messages[i].Content = quoteKiroText(transcript.String())
	}
	req.Tools, req.ToolChoice, req.OutputConfig = nil, nil, nil
	req.Thinking = &apicompat.AnthropicThinking{Type: "disabled"}
	instruction := "Create a compact continuation summary. Preserve progress, decisions, constraints, relevant file and system state, user requirements, unresolved issues, and concrete next steps. Do not answer the last request or call tools. Return only the summary."
	var system string
	if len(req.System) > 0 && json.Unmarshal(req.System, &system) != nil {
		return errors.New("invalid Kiro compaction system prompt")
	}
	req.System = quoteKiroText(system + "\n\n" + instruction)
	req.Messages = append(req.Messages, apicompat.AnthropicMessage{Role: "user",
		Content: quoteKiroText("Summarize the conversation now according to the compaction instructions.")})
	return nil
}

func (w *kiroClientWriter) compactResponse(response *apicompat.AnthropicResponse) error {
	result := apicompat.AnthropicToResponsesResponse(response)
	result.Output = []apicompat.ResponsesOutput{}
	var summary strings.Builder
	for _, block := range response.Content {
		if block.Type == "text" {
			summary.WriteString(block.Text)
		}
	}
	reason := apicompat.AnthropicStopReasonString(response.StopReason)
	switch {
	case reason == "max_tokens":
		result.Status = "incomplete"
	case reason == "model_context_window_exceeded", reason == "refusal", reason == "tool_use", strings.TrimSpace(summary.String()) == "":
		result.Status = "failed"
		result.Error = &apicompat.ResponsesError{Code: "compaction_error", Message: "Kiro did not return a complete compaction summary"}
	default:
		result.Status = "completed"
		result.Output = []apicompat.ResponsesOutput{{ID: "cmp_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			Type: "compaction", EncryptedContent: kiroCompactionPrefix + strings.TrimSpace(summary.String())}}
	}
	if !w.request.request.Stream {
		w.c.JSON(200, result)
		return nil
	}
	initial := *result
	initial.Status, initial.Output, initial.Usage, initial.Error, initial.IncompleteDetails = "in_progress", []apicompat.ResponsesOutput{}, nil, nil, nil
	events := []apicompat.ResponsesStreamEvent{{Type: "response.created", Response: &initial},
		{Type: "response.in_progress", Response: &initial}}
	for i := range result.Output {
		events = append(events, apicompat.ResponsesStreamEvent{Type: "response.output_item.added", Item: &result.Output[i]},
			apicompat.ResponsesStreamEvent{Type: "response.output_item.done", Item: &result.Output[i]})
	}
	events = append(events, apicompat.ResponsesStreamEvent{Type: "response." + result.Status, Response: result})
	for i, event := range events {
		event.SequenceNumber = i
		data, err := apicompat.ResponsesEventToSSE(event)
		if err != nil {
			return err
		}
		if err := w.write(data); err != nil {
			return err
		}
	}
	return nil
}
