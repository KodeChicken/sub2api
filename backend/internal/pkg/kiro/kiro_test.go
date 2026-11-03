package kiro

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/stretchr/testify/require"
)

func TestCredentialsNormalize(t *testing.T) {
	input := map[string]any{"refreshToken": "refresh", "clientId": "id", "clientSecret": "secret", "authMethod": "BuilderId",
		"region": "eu-west-1", "expiresAt": "2026-11-10T00:00:00Z", "model_mapping": map[string]string{"alias": "model"}}
	normalized, err := Normalize(input, "oauth")
	require.NoError(t, err)
	require.Equal(t, "idc", normalized["auth_method"])
	require.Equal(t, "secret", normalized["client_secret"])
	require.NotContains(t, normalized, "clientSecret")
	require.Contains(t, input, "clientSecret", "normalization must not mutate input")
	creds, err := Parse(normalized, "oauth")
	require.NoError(t, err)
	require.Equal(t, "eu-west-1", creds.EffectiveAPIRegion())
	require.Equal(t, BuilderProfileARN, creds.StreamingProfile())
	require.Equal(t, creds.Machine(), creds.Machine())
	for _, input := range []map[string]any{
		{"refresh_token": "r", "region": "us-east-1.attacker.example"},
		{"refresh_token": "r", "auth_method": "idc", "client_id": "id"},
		{"refresh_token": "r", "auth_method": "external_idp", "client_id": "id", "token_endpoint": "https://127.0.0.1/token"},
		{"refresh_token": "r", "machine_id": "bad\r\nHeader: value"},
	} {
		_, err := Normalize(input, "oauth")
		require.Error(t, err)
	}
}

func TestAPIKeyCredentialsAndHeaders(t *testing.T) {
	creds, err := Parse(map[string]any{"kiroApiKey": "ksk_example"}, "apikey")
	require.NoError(t, err)
	require.Equal(t, "cli", creds.Endpoint)
	require.Empty(t, creds.StreamingProfile())
	req, err := http.NewRequest(http.MethodPost, "https://"+creds.RuntimeHost()+"/", nil)
	require.NoError(t, err)
	creds.Decorate(req, creds.APIKey, false)
	require.Equal(t, "Bearer ksk_example", req.Header.Get("Authorization"))
	require.Equal(t, "API_KEY", req.Header.Get("tokentype"))
	_, err = Parse(map[string]any{"api_key": "not-a-kiro-key"}, "apikey")
	require.Error(t, err)
}

func TestConvertToolHistoryAndConversationIsolation(t *testing.T) {
	name := strings.Repeat("mcp_tool_", 12)
	request := &apicompat.AnthropicRequest{Model: "claude-sonnet-4-6-20260101", System: json.RawMessage(`"System rules"`),
		Tools: []apicompat.AnthropicTool{{Name: name, InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)}},
		Messages: []apicompat.AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`"hello"`)},
			{Role: "assistant", Content: json.RawMessage(`[{"type":"tool_use","id":"call_1","name":"` + name + `","input":{"n":1}}]`)},
			{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"call_1","content":"done"}]`)},
			{Role: "user", Content: json.RawMessage(`"continue"`)},
		}}
	creds := Credentials{Endpoint: "cli", AuthMethod: "api_key"}
	conversion, err := Convert(request, creds, "key:1")
	require.NoError(t, err)
	state := conversion.Request.ConversationState
	require.Equal(t, "claude-sonnet-4.6", state.CurrentMessage.User.ModelID)
	require.Equal(t, "KIRO_CLI", state.CurrentMessage.User.Origin)
	require.Empty(t, state.AgentContinuationID)
	require.Empty(t, state.History[0].User.ModelID)
	require.Contains(t, state.History[0].User.Content, "System rules")
	require.Len(t, state.History, 4)
	require.Equal(t, "continue", state.CurrentMessage.User.Content)
	require.Len(t, state.CurrentMessage.User.Context.ToolResults, 1)
	short := state.CurrentMessage.User.Context.Tools[0].Specification.Name
	require.LessOrEqual(t, len(short), 63)
	require.Equal(t, name, conversion.ToolNames[short])
	require.Equal(t, short, state.History[3].Assistant.ToolUses[0].Name)
	second, err := Convert(request, creds, "key:1")
	require.NoError(t, err)
	require.Equal(t, state.ConversationID, second.Request.ConversationState.ConversationID)
	other, err := Convert(request, creds, "key:2")
	require.NoError(t, err)
	require.NotEqual(t, state.ConversationID, other.Request.ConversationState.ConversationID)
}

func TestConvertInterruptedAndInvalidToolHistory(t *testing.T) {
	req := &apicompat.AnthropicRequest{Model: "model", Messages: []apicompat.AnthropicMessage{
		{Role: "user", Content: json.RawMessage(`"hello"`)},
		{Role: "assistant", Content: json.RawMessage(`[{"type":"tool_use","id":"call","name":"read","input":{}}]`)},
		{Role: "user", Content: json.RawMessage(`"interrupted"`)},
	}}
	conversion, err := Convert(req, Credentials{}, "test")
	require.NoError(t, err)
	results := conversion.Request.ConversationState.CurrentMessage.User.Context.ToolResults
	require.Len(t, results, 1)
	require.Equal(t, "error", results[0].Status)
	req.Messages[2].Content = json.RawMessage(`[{"type":"tool_result","tool_use_id":"unrelated","content":"result"}]`)
	_, err = Convert(req, Credentials{}, "test")
	require.ErrorContains(t, err, "does not match")
	req.Messages = req.Messages[:2]
	_, err = Convert(req, Credentials{}, "test")
	require.ErrorContains(t, err, "prefill")
}

func frame(t *testing.T, kind string, data string) []byte {
	t.Helper()
	headers := eventstream.Headers{}
	headers.Set(":message-type", eventstream.StringValue("event"))
	headers.Set(":event-type", eventstream.StringValue(kind))
	var buffer bytes.Buffer
	require.NoError(t, eventstream.NewEncoder().Encode(&buffer, eventstream.Message{Headers: headers, Payload: []byte(data)}))
	return buffer.Bytes()
}

func TestDecoderChecksLengthsCRCsAndTruncation(t *testing.T) {
	raw := frame(t, "assistantResponseEvent", `{"content":"hello"}`)
	decoder := NewDecoder(bytes.NewReader(raw))
	event, err := decoder.Next()
	require.NoError(t, err)
	require.Equal(t, "assistantResponseEvent", event.Type)
	_, err = decoder.Next()
	require.ErrorIs(t, err, io.EOF)
	for _, data := range [][]byte{raw[:len(raw)-1], raw[:6]} {
		_, err := NewDecoder(bytes.NewReader(data)).Next()
		require.Error(t, err)
	}
	for _, index := range []int{10, len(raw) - 1} {
		corrupt := bytes.Clone(raw)
		corrupt[index] ^= 1
		_, err := NewDecoder(bytes.NewReader(corrupt)).Next()
		require.Error(t, err)
	}
	for _, lengths := range [][2]uint32{{15, 0}, {maxFrameSize + 1, 0}, {16, 1}, {32, 1 << 20}} {
		var prelude [12]byte
		binary.BigEndian.PutUint32(prelude[:4], lengths[0])
		binary.BigEndian.PutUint32(prelude[4:8], lengths[1])
		_, err := NewDecoder(bytes.NewReader(prelude[:])).Next()
		require.ErrorContains(t, err, "length")
	}
}

func consume(t *testing.T, stream *Stream, kind, payload string) []apicompat.AnthropicStreamEvent {
	t.Helper()
	events, err := stream.Consume(Event{Type: kind, Payload: json.RawMessage(payload)})
	require.NoError(t, err)
	return events
}

func TestStreamThinkingFragmentationAndExactUsage(t *testing.T) {
	text := "<thinking>reasoning</thinking>Hello"
	for split := 0; split <= len(text); split++ {
		stream := NewStream("model", nil)
		left, _ := json.Marshal(map[string]string{"content": text[:split]})
		right, _ := json.Marshal(map[string]string{"content": text[split:]})
		consume(t, stream, "assistantResponseEvent", string(left))
		consume(t, stream, "assistantResponseEvent", string(right))
		consume(t, stream, "metadataEvent", `{"tokenUsage":{"uncachedInputTokens":10,"outputTokens":4,"cacheReadInputTokens":20,"cacheWriteInputTokens":3}}`)
		consume(t, stream, "metadataEvent", `{"tokenUsage":{"uncachedInputTokens":11,"outputTokens":5,"cacheReadInputTokens":21,"cacheWriteInputTokens":4},"stopReason":"END_TURN"}`)
		events, err := stream.Finish(999, 999)
		require.NoError(t, err, "split %d", split)
		require.Len(t, stream.Response.Content, 2)
		require.Equal(t, "reasoning", stream.Response.Content[0].Thinking)
		require.Equal(t, "Hello", stream.Response.Content[1].Text)
		require.Equal(t, 11, stream.Response.Usage.InputTokens)
		require.Equal(t, 5, stream.Response.Usage.OutputTokens)
		require.Equal(t, 21, stream.Response.Usage.CacheReadInputTokens)
		require.Equal(t, "message_stop", events[len(events)-1].Type)
	}
}

func TestStreamToolInputOnlyPublishedWhenComplete(t *testing.T) {
	stream := NewStream("model", map[string]string{"short": "long_client_tool"})
	events := consume(t, stream, "toolUseEvent", `{"toolUseId":"call","name":"short","input":"{\"path\":"}`)
	require.Empty(t, events)
	events = consume(t, stream, "toolUseEvent", `{"toolUseId":"call","name":"short","input":"\"file\"}","stop":true}`)
	require.NotEmpty(t, events)
	require.Equal(t, "long_client_tool", stream.Response.Content[0].Name)
	require.JSONEq(t, `{"path":"file"}`, string(stream.Response.Content[0].Input))
	_, err := stream.Finish(2, 3)
	require.NoError(t, err)
	require.Equal(t, "tool_use", *stream.Response.StopReason)
	for _, payload := range []string{
		`{"toolUseId":"call","name":"undeclared","input":"{}","stop":true}`,
		`{"toolUseId":"call","name":"short","input":"{\"bad\":","stop":true}`,
	} {
		_, err := NewStream("model", map[string]string{"short": "tool"}).Consume(Event{Type: "toolUseEvent", Payload: json.RawMessage(payload)})
		require.Error(t, err)
	}
	truncated := NewStream("model", map[string]string{"short": "tool"})
	consume(t, truncated, "toolUseEvent", `{"toolUseId":"call","name":"short","input":"{"}`)
	_, err = truncated.Finish(1, 1)
	require.ErrorContains(t, err, "incomplete")
}

func TestNativeReasoningAndRefusal(t *testing.T) {
	stream := NewStream("model", nil)
	consume(t, stream, "reasoningContentEvent", `{"text":"reasoning","signature":"sig"}`)
	consume(t, stream, "assistantResponseEvent", `{"content":"answer"}`)
	consume(t, stream, "meteringEvent", `{"usage":0.025}`)
	_, err := stream.Finish(10, 5)
	require.NoError(t, err)
	require.Equal(t, "sig", stream.Response.Content[0].Signature)
	require.Equal(t, 0.025, stream.Credits)
	filtered := NewStream("model", nil)
	consume(t, filtered, "metadataEvent", `{"stopReason":"CONTENT_FILTERED"}`)
	_, err = filtered.Finish(10, 0)
	require.NoError(t, err)
	require.Equal(t, "refusal", *filtered.Response.StopReason)
}

func TestImageValidationAndResize(t *testing.T) {
	var pngData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 2000, 10))))
	source := &apicompat.AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(pngData.Bytes())}
	img, err := ConvertImage(source)
	require.NoError(t, err)
	data, err := base64.StdEncoding.DecodeString(img.Source["bytes"])
	require.NoError(t, err)
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	require.LessOrEqual(t, config.Width, 1536)
	_, err = ConvertImage(&apicompat.AnthropicImageSource{Type: "url"})
	require.Error(t, err)
}

func TestNativeReasoningCapabilityGates(t *testing.T) {
	for _, tc := range []struct {
		model, thinking, effort, field, expected string
	}{
		{"claude-sonnet-4.5", "enabled", "high", "", ""},
		{"custom-model", "adaptive", "high", "", ""},
		{"claude-opus-4.6", "enabled", "high", "", ""},
		{"claude-opus-4.6", "adaptive", "xhigh", "output_config", "high"},
		{"claude-opus-4.7", "adaptive", "xhigh", "output_config", "xhigh"},
		{"claude-sonnet-4.6", "disabled", "high", "", ""},
		{"gpt-5.6-sol-thinking", "adaptive", "xhigh", "reasoning", "xhigh"},
		{"gpt-5.6-luna", "adaptive", "max", "reasoning", "max"},
	} {
		t.Run(tc.model+"/"+tc.thinking, func(t *testing.T) {
			req := &apicompat.AnthropicRequest{Model: tc.model, Thinking: &apicompat.AnthropicThinking{Type: tc.thinking},
				OutputConfig: &apicompat.AnthropicOutputConfig{Effort: tc.effort},
				Messages:     []apicompat.AnthropicMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}}}
			conversion, err := Convert(req, Credentials{}, "test")
			require.NoError(t, err)
			fields := conversion.Request.AdditionalModelRequestFields
			if tc.field == "" {
				require.Empty(t, fields)
			} else {
				require.Equal(t, tc.expected, fields[tc.field].(map[string]string)["effort"])
			}
		})
	}
}
