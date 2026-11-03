package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type kiroSearchCall struct {
	id      string
	query   string
	results json.RawMessage
}

func kiroHasSearch(request *apicompat.AnthropicRequest) bool {
	for _, tool := range request.Tools {
		if strings.HasPrefix(tool.Type, "web_search") {
			return true
		}
	}
	return false
}

func (s *KiroService) search(ctx context.Context, account *Account, query string) (json.RawMessage, error) {
	if strings.TrimSpace(query) == "" || len(query) > 4096 {
		return nil, errors.New("invalid Kiro search query")
	}
	account, token, err := s.AccessToken(ctx, account, "")
	if err != nil {
		return nil, err
	}
	creds, err := kiro.Parse(account.Credentials, account.Type)
	if err != nil {
		return nil, err
	}
	creds, err = s.resolveProfile(ctx, account, creds, token)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": uuid.NewString(), "method": "tools/call",
		"params": map[string]any{"name": "web_search", "arguments": map[string]string{"query": query}}})
	if err != nil {
		return nil, err
	}
	searchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(searchCtx, http.MethodPost, "https://"+creds.RuntimeHost()+"/mcp", strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	creds.Decorate(req, token, false)
	req.Header.Set("Content-Type", "application/json")
	if profile := creds.StreamingProfile(); profile != "" {
		req.Header.Set("x-amzn-kiro-profile-arn", profile)
	}
	resp, err := s.do(req, account)
	if err != nil {
		return nil, err
	}
	data, err := readKiroBody(resp, 2<<20)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newKiroHTTPError(resp, data)
	}
	var rpc struct {
		Error  json.RawMessage `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &rpc) != nil || (len(rpc.Error) > 0 && string(rpc.Error) != "null") || rpc.Result.IsError {
		return nil, errors.New("Kiro search returned an invalid or failed MCP response")
	}
	for _, content := range rpc.Result.Content {
		if content.Type == "text" && json.Valid([]byte(content.Text)) {
			var search struct {
				Results json.RawMessage `json:"results"`
				Error   string          `json:"error"`
			}
			if json.Unmarshal([]byte(content.Text), &search) != nil || search.Error != "" || len(search.Results) == 0 {
				return nil, errors.New("Kiro search returned no valid results")
			}
			return json.RawMessage(content.Text), nil
		}
	}
	return nil, errors.New("Kiro search response is missing results")
}

func (s *KiroService) forwardSearch(ctx context.Context, c *gin.Context, account *Account, request *kiroGatewayRequest, parsed *ParsedRequest, namespace string, start time.Time) (*ForwardResult, error) {
	searchCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	canonical, _ := json.Marshal(request.request)
	var current apicompat.AnthropicRequest
	if err := json.Unmarshal(canonical, &current); err != nil {
		return nil, err
	}
	for i := range current.Tools {
		if strings.HasPrefix(current.Tools[i].Type, "web_search") {
			current.Tools[i].Name = "web_search"
			current.Tools[i].Description = "Search the web for current information."
			current.Tools[i].InputSchema = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)
		}
	}
	var calls []kiroSearchCall
	var total apicompat.AnthropicUsage
	var last *apicompat.AnthropicResponse
	totalCredits := 0.0
	allProviderUsage := true
	metered := false
	defer func() {
		if metered {
			s.recordMetering(ctx, account, &kiro.Stream{Credits: totalCredits, ProviderUsage: allProviderUsage})
		}
	}()
	result := &ForwardResult{Model: request.clientModel, UpstreamModel: request.request.Model, Stream: request.request.Stream}
	fail := func(err error) (*ForwardResult, error) {
		result.Usage, result.Duration = claudeUsageFromKiro(total), time.Since(start)
		result.SearchCount = len(calls)
		if total.InputTokens+total.OutputTokens+total.CacheReadInputTokens+total.CacheCreationInputTokens > 0 {
			_ = writeKiroGatewayError(c, request.protocol, 502, "upstream_error", "Kiro search could not complete")
			return result, err
		}
		return nil, s.gatewayFailure(ctx, c, account, request.protocol, err)
	}
	for round := 0; round < 5; round++ {
		resp, conversion, selected, err := s.openInference(searchCtx, account, &current, namespace)
		if err != nil {
			return fail(err)
		}
		account = selected
		if parsed != nil && parsed.OnUpstreamAccepted != nil {
			parsed.OnUpstreamAccepted()
		}
		result.RequestID, result.UpstreamHeaders = kiroRequestID(resp), resp.Header.Clone()
		stream := kiro.NewStream(request.clientModel, conversion.ToolNames)
		err = consumeKiroStream(searchCtx, resp, stream, func([]apicompat.AnthropicStreamEvent) error { return nil })
		_ = resp.Body.Close()
		metered = true
		input, countErr := estimateKiroRequest(&current)
		if !stream.ProviderUsage && len(stream.Response.Content) > 0 {
			stream.Response.Usage = apicompat.AnthropicUsage{InputTokens: input,
				OutputTokens: estimateKiroOutput(stream.Response.Content)}
		}
		allProviderUsage = allProviderUsage && stream.ProviderUsage
		if err == nil {
			if countErr != nil {
				err = countErr
			} else {
				_, err = stream.Finish(input, estimateKiroOutput(stream.Response.Content))
			}
		}
		u := stream.Response.Usage
		total.InputTokens += u.InputTokens
		total.OutputTokens += u.OutputTokens
		total.CacheCreationInputTokens += u.CacheCreationInputTokens
		total.CacheReadInputTokens += u.CacheReadInputTokens
		totalCredits += stream.Credits
		if err != nil {
			return fail(err)
		}
		if result.FirstTokenMs == nil {
			ms := int(time.Since(start).Milliseconds())
			result.FirstTokenMs = &ms
		}
		last = &stream.Response
		var searchResults []apicompat.AnthropicContentBlock
		clientTools := false
		for _, block := range last.Content {
			if block.Type != "tool_use" {
				continue
			}
			if block.Name != "web_search" {
				clientTools = true
				continue
			}
			if len(calls) >= 5 {
				return fail(errors.New("Kiro search exceeded five calls"))
			}
			var arguments struct {
				Query string `json:"query"`
			}
			if json.Unmarshal(block.Input, &arguments) != nil {
				return fail(errors.New("invalid Kiro search tool arguments"))
			}
			found, err := s.search(searchCtx, account, arguments.Query)
			if err != nil {
				return fail(err)
			}
			calls = append(calls, kiroSearchCall{id: "srvtoolu_" + strings.ReplaceAll(uuid.NewString(), "-", ""), query: arguments.Query, results: found})
			searchResults = append(searchResults, apicompat.AnthropicContentBlock{Type: "tool_result", ToolUseID: block.ID, Content: quoteKiroText(string(found))})
		}
		if len(searchResults) == 0 || clientTools {
			if len(searchResults) > 0 {
				content := make([]apicompat.AnthropicContentBlock, 0, len(last.Content))
				for _, block := range last.Content {
					if block.Type != "tool_use" || block.Name != "web_search" {
						content = append(content, block)
					}
				}
				last.Content = content
			}
			break
		}
		if round == 4 {
			return fail(errors.New("Kiro search exceeded five rounds"))
		}
		assistant, _ := json.Marshal(last.Content)
		results, _ := json.Marshal(searchResults)
		current.Messages = append(current.Messages, apicompat.AnthropicMessage{Role: "assistant", Content: assistant},
			apicompat.AnthropicMessage{Role: "user", Content: results})
	}
	last.Usage = total
	request.searches = calls
	writer := newKiroClientWriter(c, request)
	var err error
	if request.request.Stream {
		err = writer.emit(kiroReplayEvents(last))
		if err == nil {
			err = writer.finish()
		}
	} else {
		err = writer.response(last)
	}
	result.Usage, result.Duration, result.SearchCount = claudeUsageFromKiro(total), time.Since(start), len(calls)
	result.ClientDisconnect = writer.disconnected
	return result, err
}

func quoteKiroText(value string) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func kiroReplayEvents(response *apicompat.AnthropicResponse) []apicompat.AnthropicStreamEvent {
	message := *response
	message.Content = []apicompat.AnthropicContentBlock{}
	message.StopReason, message.Usage = nil, apicompat.AnthropicUsage{}
	events := []apicompat.AnthropicStreamEvent{{Type: "message_start", Message: &message}}
	for i, block := range response.Content {
		index := i
		startBlock := block
		var delta *apicompat.AnthropicDelta
		switch block.Type {
		case "text":
			startBlock.Text = ""
			delta = &apicompat.AnthropicDelta{Type: "text_delta", Text: block.Text}
		case "thinking":
			startBlock.Thinking, startBlock.Signature = "", ""
			delta = &apicompat.AnthropicDelta{Type: "thinking_delta", Thinking: block.Thinking}
		case "tool_use":
			startBlock.Input = json.RawMessage("{}")
			delta = &apicompat.AnthropicDelta{Type: "input_json_delta", PartialJSON: string(block.Input)}
		}
		events = append(events, apicompat.AnthropicStreamEvent{Type: "content_block_start", Index: &index, ContentBlock: &startBlock})
		if delta != nil {
			events = append(events, apicompat.AnthropicStreamEvent{Type: "content_block_delta", Index: &index, Delta: delta})
		}
		if block.Type == "thinking" && block.Signature != "" {
			events = append(events, apicompat.AnthropicStreamEvent{Type: "content_block_delta", Index: &index,
				Delta: &apicompat.AnthropicDelta{Type: "signature_delta", Signature: block.Signature}})
		}
		events = append(events, apicompat.AnthropicStreamEvent{Type: "content_block_stop", Index: &index})
	}
	usage := response.Usage
	return append(events, apicompat.AnthropicStreamEvent{Type: "message_delta", Delta: &apicompat.AnthropicDelta{
		StopReason: apicompat.AnthropicStopReasonString(response.StopReason)}, Usage: &usage}, apicompat.AnthropicStreamEvent{Type: "message_stop"})
}
