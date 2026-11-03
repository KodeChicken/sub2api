package kiro

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/google/uuid"
)

const (
	maxFrameSize     = 16 << 20
	maxResponseSize  = 32 << 20
	maxToolInputSize = 4 << 20
)

type Event struct {
	Type    string
	Payload json.RawMessage
}

type Decoder struct {
	reader  *bufio.Reader
	decoder *eventstream.Decoder
}

func NewDecoder(reader io.Reader) *Decoder {
	return &Decoder{reader: bufio.NewReaderSize(reader, 64<<10), decoder: eventstream.NewDecoder()}
}

func (d *Decoder) Next() (Event, error) {
	prelude, err := d.reader.Peek(12)
	if err != nil {
		if errors.Is(err, io.EOF) && len(prelude) > 0 {
			err = io.ErrUnexpectedEOF
		}
		return Event{}, err
	}
	total, headers := binary.BigEndian.Uint32(prelude[:4]), binary.BigEndian.Uint32(prelude[4:8])
	// The SDK checks CRCs; bound lengths before its payload allocation and unsigned subtraction.
	if total < 16 || total > maxFrameSize || headers > 128<<10 || headers > total-16 {
		return Event{}, errors.New("invalid Kiro event frame length")
	}
	message, err := d.decoder.Decode(d.reader, nil)
	if err != nil {
		return Event{}, fmt.Errorf("decode Kiro event: %w", err)
	}
	header := func(name string) string {
		if value := message.Headers.Get(name); value != nil {
			return value.String()
		}
		return ""
	}
	kind := header(":message-type")
	if kind == "exception" || kind == "error" {
		name := first(header(":exception-type"), header(":error-code"), "upstream_error")
		return Event{}, &StreamError{Code: name}
	}
	if !json.Valid(message.Payload) {
		return Event{}, errors.New("invalid JSON in Kiro event")
	}
	return Event{Type: header(":event-type"), Payload: message.Payload}, nil
}

type StreamError struct {
	Code string
}

func (e *StreamError) Error() string { return "Kiro stream error: " + e.Code }

type pendingTool struct {
	name  string
	input strings.Builder
}

type Stream struct {
	Response          apicompat.AnthropicResponse
	Credits           float64
	ProviderUsage     bool
	ContextPercentage *float64
	ToolNames         map[string]string
	pending           map[string]*pendingTool
	completed         map[string]bool
	openIndex         int
	started           bool
	finished          bool
	bytes             int
	textPending       string
	tagThinking       bool
	nativeThinking    bool
	stopReason        string
}

func NewStream(model string, toolNames map[string]string) *Stream {
	return &Stream{
		Response: apicompat.AnthropicResponse{ID: "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			Type: "message", Role: "assistant", Model: model, Content: []apicompat.AnthropicContentBlock{}},
		ToolNames: toolNames, pending: make(map[string]*pendingTool), completed: make(map[string]bool),
		openIndex: -1,
	}
}

func (s *Stream) Consume(event Event) ([]apicompat.AnthropicStreamEvent, error) {
	if s.finished {
		return nil, errors.New("Kiro stream already finished")
	}
	s.bytes += len(event.Payload)
	if s.bytes > maxResponseSize {
		return nil, errors.New("Kiro response exceeds size limit")
	}
	switch event.Type {
	case "assistantResponseEvent":
		var payload struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil, err
		}
		return s.text(payload.Content, false), nil
	case "reasoningContentEvent":
		var payload struct {
			Text      string `json:"text"`
			Signature string `json:"signature"`
			Redacted  string `json:"redactedContent"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil, err
		}
		events := s.text("", true)
		s.nativeThinking = true
		if payload.Text != "" {
			events = append(events, s.appendText("thinking", payload.Text)...)
		}
		if payload.Signature != "" {
			events = append(events, s.openBlock("thinking")...)
			s.Response.Content[s.openIndex].Signature += payload.Signature
			events = append(events, apicompat.AnthropicStreamEvent{Type: "content_block_delta",
				Index: ptr(s.openIndex), Delta: &apicompat.AnthropicDelta{Type: "signature_delta", Signature: payload.Signature}})
		}
		if payload.Redacted != "" {
			events = append(events, s.closeBlock()...)
			block := apicompat.AnthropicContentBlock{Type: "redacted_thinking", Data: payload.Redacted}
			index := len(s.Response.Content)
			s.Response.Content = append(s.Response.Content, block)
			events = append(events, s.start()...)
			events = append(events, apicompat.AnthropicStreamEvent{Type: "content_block_start", Index: ptr(index), ContentBlock: &block},
				apicompat.AnthropicStreamEvent{Type: "content_block_stop", Index: ptr(index)})
		}
		return events, nil
	case "toolUseEvent":
		var payload struct {
			ID    string `json:"toolUseId"`
			Name  string `json:"name"`
			Input string `json:"input"`
			Stop  bool   `json:"stop"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil, err
		}
		if payload.ID == "" || s.completed[payload.ID] {
			return nil, errors.New("missing or duplicate Kiro tool call id")
		}
		tool := s.pending[payload.ID]
		if tool == nil {
			tool = &pendingTool{}
			s.pending[payload.ID] = tool
		}
		if payload.Name != "" {
			if tool.name != "" && tool.name != payload.Name {
				return nil, errors.New("Kiro tool name changed within a call")
			}
			tool.name = payload.Name
		}
		if tool.input.Len()+len(payload.Input) > maxToolInputSize {
			return nil, errors.New("Kiro tool input exceeds size limit")
		}
		tool.input.WriteString(payload.Input)
		if !payload.Stop {
			return nil, nil
		}
		name, ok := s.ToolNames[tool.name]
		if !ok {
			return nil, errors.New("Kiro called an undeclared tool")
		}
		input := first(tool.input.String(), "{}")
		if !json.Valid([]byte(input)) || !strings.HasPrefix(strings.TrimSpace(input), "{") {
			return nil, errors.New("invalid Kiro tool input JSON object")
		}
		delete(s.pending, payload.ID)
		s.completed[payload.ID] = true
		events := s.text("", true)
		events = append(events, s.closeBlock()...)
		events = append(events, s.start()...)
		index := len(s.Response.Content)
		block := apicompat.AnthropicContentBlock{Type: "tool_use", ID: payload.ID, Name: name, Input: json.RawMessage(input)}
		s.Response.Content = append(s.Response.Content, block)
		startBlock := block
		startBlock.Input = json.RawMessage("{}")
		return append(events,
			apicompat.AnthropicStreamEvent{Type: "content_block_start", Index: ptr(index), ContentBlock: &startBlock},
			apicompat.AnthropicStreamEvent{Type: "content_block_delta", Index: ptr(index),
				Delta: &apicompat.AnthropicDelta{Type: "input_json_delta", PartialJSON: input}},
			apicompat.AnthropicStreamEvent{Type: "content_block_stop", Index: ptr(index)},
		), nil
	case "metadataEvent":
		var payload struct {
			StopReason string `json:"stopReason"`
			Usage      *struct {
				Input  int `json:"uncachedInputTokens"`
				Output int `json:"outputTokens"`
				Read   int `json:"cacheReadInputTokens"`
				Write  int `json:"cacheWriteInputTokens"`
			} `json:"tokenUsage"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil, err
		}
		if payload.Usage != nil {
			u := payload.Usage
			s.Response.Usage = apicompat.AnthropicUsage{InputTokens: boundedTokens(u.Input), OutputTokens: boundedTokens(u.Output),
				CacheReadInputTokens: boundedTokens(u.Read), CacheCreationInputTokens: boundedTokens(u.Write)}
			s.ProviderUsage = true
		}
		if payload.StopReason != "" {
			s.stopReason = strings.ToLower(payload.StopReason)
		}
		if s.stopReason == "content_filtered" {
			s.pending = make(map[string]*pendingTool)
			return s.appendText("text", "Request declined by the upstream content filter."), nil
		}
		return nil, nil
	case "meteringEvent":
		var payload struct {
			Usage float64 `json:"usage"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil, err
		}
		if payload.Usage < 0 {
			return nil, errors.New("negative Kiro credit usage")
		}
		s.Credits += payload.Usage
		return nil, nil
	case "contextUsageEvent":
		var payload struct {
			Percentage float64 `json:"contextUsagePercentage"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil, err
		}
		if payload.Percentage >= 0 && payload.Percentage <= 100 {
			s.ContextPercentage = &payload.Percentage
		}
		return nil, nil
	default:
		return nil, nil
	}
}

func boundedTokens(value int) int { return min(max(value, 0), 1_000_000_000) }

func (s *Stream) Finish(inputTokens, outputTokens int) ([]apicompat.AnthropicStreamEvent, error) {
	if s.finished {
		return nil, errors.New("Kiro stream already finished")
	}
	if len(s.pending) > 0 {
		return nil, errors.New("Kiro stream ended with incomplete tool arguments")
	}
	events := s.text("", true)
	if len(s.Response.Content) == 0 {
		return nil, errors.New("empty Kiro event stream")
	}
	events = append(events, s.closeBlock()...)
	if !s.ProviderUsage {
		s.Response.Usage = apicompat.AnthropicUsage{InputTokens: max(0, inputTokens), OutputTokens: max(0, outputTokens)}
	}
	reason := "end_turn"
	if len(s.completed) > 0 {
		reason = "tool_use"
	}
	switch s.stopReason {
	case "max_tokens", "stop_sequence", "model_context_window_exceeded":
		reason = s.stopReason
	case "content_filtered":
		reason = "refusal"
	}
	s.Response.StopReason = apicompat.AnthropicStopReasonPtr(reason)
	s.finished = true
	usage := s.Response.Usage
	return append(events,
		apicompat.AnthropicStreamEvent{Type: "message_delta", Delta: &apicompat.AnthropicDelta{StopReason: reason}, Usage: &usage},
		apicompat.AnthropicStreamEvent{Type: "message_stop"},
	), nil
}

func (s *Stream) start() []apicompat.AnthropicStreamEvent {
	if s.started {
		return nil
	}
	s.started = true
	message := s.Response
	message.Content = []apicompat.AnthropicContentBlock{}
	message.Usage = apicompat.AnthropicUsage{}
	message.StopReason = nil
	return []apicompat.AnthropicStreamEvent{{Type: "message_start", Message: &message}}
}

func (s *Stream) openBlock(kind string) []apicompat.AnthropicStreamEvent {
	if s.openIndex >= 0 && s.Response.Content[s.openIndex].Type == kind {
		return nil
	}
	events := s.closeBlock()
	events = append(events, s.start()...)
	block := apicompat.AnthropicContentBlock{Type: kind}
	s.openIndex = len(s.Response.Content)
	s.Response.Content = append(s.Response.Content, block)
	return append(events, apicompat.AnthropicStreamEvent{Type: "content_block_start", Index: ptr(s.openIndex), ContentBlock: &block})
}

func (s *Stream) closeBlock() []apicompat.AnthropicStreamEvent {
	if s.openIndex < 0 {
		return nil
	}
	index := s.openIndex
	s.openIndex = -1
	return []apicompat.AnthropicStreamEvent{{Type: "content_block_stop", Index: ptr(index)}}
}

func (s *Stream) appendText(kind, value string) []apicompat.AnthropicStreamEvent {
	if value == "" {
		return nil
	}
	events := s.openBlock(kind)
	delta := &apicompat.AnthropicDelta{Type: "text_delta", Text: value}
	if kind == "thinking" {
		s.Response.Content[s.openIndex].Thinking += value
		delta = &apicompat.AnthropicDelta{Type: "thinking_delta", Thinking: value}
	} else {
		s.Response.Content[s.openIndex].Text += value
	}
	return append(events, apicompat.AnthropicStreamEvent{Type: "content_block_delta", Index: ptr(s.openIndex), Delta: delta})
}

func (s *Stream) text(value string, flush bool) []apicompat.AnthropicStreamEvent {
	s.textPending += value
	var events []apicompat.AnthropicStreamEvent
	for s.textPending != "" {
		kind, tag := "text", "<thinking>"
		if s.tagThinking {
			kind, tag = "thinking", "</thinking>"
		}
		if s.nativeThinking {
			tag = ""
		}
		index := -1
		if tag != "" {
			index = strings.Index(s.textPending, tag)
		}
		if index >= 0 {
			events = append(events, s.appendText(kind, s.textPending[:index])...)
			s.textPending = s.textPending[index+len(tag):]
			s.tagThinking = !s.tagThinking
			continue
		}
		keep := 0
		if !flush && tag != "" {
			for n := 1; n < len(tag) && n <= len(s.textPending); n++ {
				if strings.HasSuffix(s.textPending, tag[:n]) {
					keep = n
				}
			}
		}
		end := len(s.textPending) - keep
		for end > 0 && end < len(s.textPending) && !utf8.RuneStart(s.textPending[end]) {
			end--
		}
		events = append(events, s.appendText(kind, s.textPending[:end])...)
		s.textPending = s.textPending[end:]
		break
	}
	return events
}

func ptr(value int) *int { return &value }
