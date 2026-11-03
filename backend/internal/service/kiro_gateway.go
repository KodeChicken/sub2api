package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/gin-gonic/gin"
)

type kiroGatewayRequest struct {
	protocol     string
	request      *apicompat.AnthropicRequest
	clientModel  string
	includeUsage bool
	clientTools  apicompat.ResponsesClientToolMapping
	searches     []kiroSearchCall
	compact      bool
}

func parseKiroGatewayRequest(body []byte, protocol string) (*kiroGatewayRequest, error) {
	result := &kiroGatewayRequest{protocol: protocol}
	if err := validateKiroOptions(body); err != nil {
		return nil, err
	}
	switch protocol {
	case "messages":
		result.request = &apicompat.AnthropicRequest{}
		if err := json.Unmarshal(body, result.request); err != nil {
			return nil, errors.New("invalid Messages request")
		}
	case "responses":
		adapted, mapping, err := adaptResponsesClientToolsForAnthropic(body)
		if err != nil {
			return nil, err
		}
		result.clientTools = mapping
		var req apicompat.ResponsesRequest
		if err := json.Unmarshal(adapted, &req); err != nil {
			return nil, errors.New("invalid Responses request")
		}
		if req.PreviousResponseID != "" {
			return nil, errors.New("Kiro requires full conversation input; previous_response_id is not supported")
		}
		for i := range req.Tools {
			if strings.HasPrefix(req.Tools[i].Type, "web_search_preview") {
				req.Tools[i].Type = "web_search"
			}
		}
		result.compact, err = prepareKiroResponsesInput(&req)
		if err != nil {
			return nil, err
		}
		result.request, err = apicompat.ResponsesToAnthropicRequest(&req)
		if err != nil {
			return nil, err
		}
		preserveKiroReasoning(result.request, req.Reasoning)
		if result.compact {
			if err := prepareKiroSummary(result.request); err != nil {
				return nil, err
			}
		}
	case "chat":
		var req apicompat.ChatCompletionsRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, errors.New("invalid Chat Completions request")
		}
		responses, err := apicompat.ChatCompletionsToResponsesWithMinOutputTokens(&req, 1)
		if err != nil {
			return nil, err
		}
		result.request, err = apicompat.ResponsesToAnthropicRequest(responses)
		if err != nil {
			return nil, err
		}
		result.request.Stream = req.Stream
		preserveKiroReasoning(result.request, responses.Reasoning)
		result.includeUsage = req.StreamOptions != nil && req.StreamOptions.IncludeUsage
	default:
		return nil, errors.New("unsupported Kiro client protocol")
	}
	result.clientModel = result.request.Model
	return result, nil
}

func preserveKiroReasoning(request *apicompat.AnthropicRequest, reasoning *apicompat.ResponsesReasoning) {
	if reasoning == nil || reasoning.Effort == "" {
		return
	}
	request.OutputConfig = &apicompat.AnthropicOutputConfig{Effort: reasoning.Effort}
	if reasoning.Effort == "none" {
		request.Thinking = &apicompat.AnthropicThinking{Type: "disabled"}
	} else {
		request.Thinking = &apicompat.AnthropicThinking{Type: "adaptive"}
	}
}

func (s *GatewayService) forwardKiro(ctx context.Context, c *gin.Context, account *Account, body []byte, protocol string, parsed *ParsedRequest) (*ForwardResult, error) {
	if s.kiroService == nil {
		return nil, writeKiroGatewayError(c, protocol, http.StatusBadGateway, "api_error", "Kiro service is not configured")
	}
	request, err := parseKiroGatewayRequest(body, protocol)
	if err != nil {
		return nil, writeKiroGatewayError(c, protocol, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	request.request.Model = account.GetMappedModel(request.request.Model)
	beginUpstreamResponseModelObservation(c)
	return s.kiroService.forward(ctx, c, account, request, parsed)
}

func (s *KiroService) openInference(ctx context.Context, account *Account, request *apicompat.AnthropicRequest, namespace string) (*http.Response, *kiro.Conversion, *Account, error) {
	var rejectedToken string
	for attempt := 0; attempt < 2; attempt++ {
		fresh, token, err := s.AccessToken(ctx, account, rejectedToken)
		if err != nil {
			return nil, nil, account, err
		}
		account = fresh
		creds, err := kiro.Parse(account.Credentials, account.Type)
		if err != nil {
			return nil, nil, account, err
		}
		creds, err = s.resolveProfile(ctx, account, creds, token)
		if err != nil {
			return nil, nil, account, err
		}
		conversion, err := kiro.Convert(request, creds, namespace)
		if err != nil {
			return nil, nil, account, &kiroRequestError{err}
		}
		payload, err := json.Marshal(conversion.Request)
		if err != nil {
			return nil, nil, account, err
		}
		path := "/generateAssistantResponse"
		contentType := "application/json"
		if creds.Endpoint == "cli" {
			path, contentType = "/", "application/x-amz-json-1.0"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+creds.RuntimeHost()+path, bytes.NewReader(payload))
		if err != nil {
			return nil, nil, account, err
		}
		creds.Decorate(req, token, false)
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Accept", "application/vnd.amazon.eventstream")
		if creds.Endpoint == "cli" {
			req.Header.Set("x-amz-target", "AmazonCodeWhispererStreamingService.GenerateAssistantResponse")
			req.Header.Set("User-Agent", "aws-sdk-rust/1.3.15 ua/2.1 api/codewhispererstreaming/0.1.14474 os/linux lang/rust/1.92.0 md/appVersion-"+kiro.DefaultVersion+" app/AmazonQ-For-CLI")
		} else {
			req.Header.Set("x-amzn-kiro-agent-mode", "vibe")
		}
		resp, err := s.do(req, account)
		if err != nil {
			if resp != nil && resp.Body != nil {
				resp.Body.Close()
			}
			return nil, nil, account, err
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
				resp.Body.Close()
				return nil, nil, account, errors.New("Kiro returned HTML instead of an event stream")
			}
			return resp, conversion, account, nil
		}
		data, err := readKiroBody(resp, 256<<10)
		if err != nil {
			return nil, nil, account, err
		}
		if attempt == 0 && account.Type == AccountTypeOAuth &&
			(resp.StatusCode == 401 || resp.StatusCode == 403) &&
			bytes.Contains(data, []byte("The bearer token included in the request is invalid")) {
			rejectedToken = token
			continue
		}
		return nil, nil, account, newKiroHTTPError(resp, data)
	}
	return nil, nil, account, errors.New("Kiro token rejected after refresh")
}

type kiroRequestError struct{ error }

func (s *KiroService) forward(ctx context.Context, c *gin.Context, account *Account, request *kiroGatewayRequest, parsed *ParsedRequest) (*ForwardResult, error) {
	start := time.Now()
	namespace := strconv.FormatInt(account.ID, 10)
	if parsed != nil && parsed.SessionContext != nil {
		namespace += ":" + strconv.FormatInt(parsed.SessionContext.APIKeyID, 10)
	}
	// A search round is buffered so internal server tools never escape as client function calls.
	hasSearch := kiroHasSearch(request.request)
	if hasSearch {
		return s.forwardSearch(ctx, c, account, request, parsed, namespace, start)
	}
	upstreamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	resp, conversion, selected, err := s.openInference(upstreamCtx, account, request.request, namespace)
	if err != nil {
		return nil, s.gatewayFailure(ctx, c, selected, request.protocol, err)
	}
	defer resp.Body.Close()
	if parsed != nil && parsed.OnUpstreamAccepted != nil {
		parsed.OnUpstreamAccepted()
	}
	stream := kiro.NewStream(request.clientModel, conversion.ToolNames)
	writer := newKiroClientWriter(c, request)
	result := &ForwardResult{Model: request.clientModel, UpstreamModel: request.request.Model,
		Stream: request.request.Stream, UpstreamHeaders: resp.Header.Clone(), RequestID: kiroRequestID(resp)}
	if result.RequestID != "" {
		c.Header("x-request-id", result.RequestID)
	}
	inputTokens, countErr := estimateKiroRequest(request.request)
	if countErr != nil {
		return nil, writeKiroGatewayError(c, request.protocol, 400, "invalid_request_error", countErr.Error())
	}
	consumeErr := consumeKiroStream(upstreamCtx, resp, stream, func(events []apicompat.AnthropicStreamEvent) error {
		if request.request.Stream && !request.compact {
			if result.FirstTokenMs == nil && len(events) > 0 {
				ms := int(time.Since(start).Milliseconds())
				result.FirstTokenMs = &ms
			}
			return writer.emit(events)
		}
		return nil
	})
	if consumeErr == nil {
		final, err := stream.Finish(inputTokens, estimateKiroOutput(stream.Response.Content))
		consumeErr = err
		if err == nil {
			if request.compact {
				consumeErr = writer.compactResponse(&stream.Response)
			} else if request.request.Stream {
				consumeErr = writer.emit(final)
				if consumeErr == nil {
					consumeErr = writer.finish()
				}
			} else {
				consumeErr = writer.response(&stream.Response)
			}
		}
	}
	if consumeErr != nil && !stream.ProviderUsage && writer.started {
		stream.Response.Usage = apicompat.AnthropicUsage{
			InputTokens: inputTokens, OutputTokens: estimateKiroOutput(stream.Response.Content),
		}
	}
	result.Usage = claudeUsageFromKiro(stream.Response.Usage)
	result.Duration = time.Since(start)
	if request.request.OutputConfig != nil {
		effort := request.request.OutputConfig.Effort
		result.ReasoningEffort = &effort
	}
	s.recordMetering(ctx, selected, stream)
	if consumeErr != nil {
		if ctx.Err() != nil || writer.disconnected {
			result.ClientDisconnect = true
			return result, consumeErr
		}
		if !writer.started {
			// An observed provider usage snapshot is billable even if the stream later fails.
			if stream.ProviderUsage {
				return result, writeKiroGatewayError(c, request.protocol, 502, "upstream_error", "Kiro stream ended before completing the response")
			}
			return nil, &UpstreamFailoverError{StatusCode: 502,
				ResponseBody: []byte(`{"error":"invalid or incomplete Kiro stream"}`)}
		}
		writer.streamError("Kiro stream ended before completing the response")
		return result, consumeErr
	}
	return result, nil
}

func consumeKiroStream(ctx context.Context, resp *http.Response, stream *kiro.Stream, emit func([]apicompat.AnthropicStreamEvent) error) error {
	// Closing the body on cancellation also unblocks a decoder waiting for the next frame.
	stop := context.AfterFunc(ctx, func() { _ = resp.Body.Close() })
	defer stop()
	idle := time.AfterFunc(2*time.Minute, func() { _ = resp.Body.Close() })
	defer idle.Stop()
	decoder := kiro.NewDecoder(resp.Body)
	for {
		event, err := decoder.Next()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		events, err := stream.Consume(event)
		if err != nil {
			return err
		}
		if err := emit(events); err != nil {
			return err
		}
		idle.Reset(2 * time.Minute)
	}
}

func estimateKiroRequest(request *apicompat.AnthropicRequest) (int, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return 0, err
	}
	return estimateAnthropicCountTokensLocally(body)
}

func estimateKiroOutput(blocks []apicompat.AnthropicContentBlock) int {
	total := 0
	for _, block := range blocks {
		total += utf8.RuneCountInString(block.Text) + utf8.RuneCountInString(block.Thinking) + utf8.RuneCount(block.Input)
	}
	// shortcut: no official Kiro tokenizer, use a labeled estimate only when provider tokenUsage is absent.
	return (total + 2) / 3
}

func claudeUsageFromKiro(usage apicompat.AnthropicUsage) ClaudeUsage {
	return ClaudeUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
		CacheReadInputTokens: usage.CacheReadInputTokens, CacheCreationInputTokens: usage.CacheCreationInputTokens}
}

func kiroRequestID(resp *http.Response) string {
	return firstNonEmpty(resp.Header.Get("x-amzn-requestid"), resp.Header.Get("x-amzn-request-id"), resp.Header.Get("x-request-id"))
}

func (s *KiroService) recordMetering(ctx context.Context, account *Account, stream *kiro.Stream) {
	if s.accountRepo == nil || account == nil || account.ID <= 0 {
		return
	}
	source := "estimate"
	if stream.ProviderUsage {
		source = "provider"
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = s.accountRepo.UpdateExtra(cleanup, account.ID, map[string]any{
		"kiro_last_usage_source": source, "kiro_last_request_credits": stream.Credits,
		"kiro_last_metered_at": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *KiroService) gatewayFailure(ctx context.Context, c *gin.Context, account *Account, protocol string, err error) error {
	var requestErr *kiroRequestError
	if errors.As(err, &requestErr) {
		return writeKiroGatewayError(c, protocol, 400, "invalid_request_error", requestErr.Error())
	}
	var upstreamErr *kiroHTTPError
	if !errors.As(err, &upstreamErr) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &UpstreamFailoverError{StatusCode: 502,
			Reason:           "kiro_credential_or_transport",
			ClientStatusCode: 502, ClientMessage: "Kiro credentials or upstream transport are unavailable"}
	}
	reason, status := upstreamErr.Reason, upstreamErr.Status
	failover := &UpstreamFailoverError{StatusCode: status, ResponseBody: upstreamErr.Body,
		ResponseHeaders: upstreamErr.Header}
	if s.accountRepo != nil && account != nil {
		switch {
		case reason == "TEMPORARILY_SUSPENDED":
			_ = s.accountRepo.SetError(ctx, account.ID, "Kiro account suspended; contact upstream support")
		case status == 401:
			_ = s.accountRepo.SetTempUnschedulable(ctx, account.ID, time.Now().Add(5*time.Minute), "Kiro credential rejected; reauthorize account")
		case status == 403 && (reason == "MONTHLY_REQUEST_COUNT" || reason == "OVERAGE_REQUEST_LIMIT_EXCEEDED"),
			status == 402:
			until := time.Date(time.Now().UTC().Year(), time.Now().UTC().Month()+1, 1, 0, 0, 0, 0, time.UTC)
			_ = s.accountRepo.SetRateLimited(ctx, account.ID, until)
		case status == 429:
			switch reason {
			case "USER_REQUEST_RATE_EXCEEDED", "CREDIT_CONSUMPTION_RATE_EXCEEDED", "SERVICE_REQUEST_RATE_EXCEEDED":
				delay := 60 * time.Second
				if seconds, err := strconv.Atoi(upstreamErr.Header.Get("Retry-After")); err == nil && seconds > 0 {
					delay = min(time.Duration(seconds)*time.Second, time.Hour)
				}
				_ = s.accountRepo.SetRateLimited(ctx, account.ID, time.Now().Add(delay))
			default:
				failover.RequestScopedTransient = true
				failover.RetryableOnSameAccount = true
				failover.SameAccountRetryMax = 1
				failover.SameAccountRetryDelay = time.Second
			}
		}
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: PlatformKiro, AccountID: account.ID, AccountName: account.Name,
		UpstreamStatusCode: status, UpstreamRequestID: upstreamErr.Header.Get("x-amzn-requestid"), Kind: "kiro_upstream",
		Message: "Kiro upstream request failed", Detail: reason})
	if status == 400 || status == 413 || status == 422 {
		return writeKiroGatewayError(c, protocol, status, "invalid_request_error", "Kiro rejected the request; verify model support, context size and tool history")
	}
	if status == 401 || status == 402 || status == 403 || status == 429 || status >= 500 {
		return failover
	}
	return writeKiroGatewayError(c, protocol, 502, "upstream_error", "Kiro upstream request failed")
}

func writeKiroGatewayError(c *gin.Context, protocol string, status int, kind, message string) error {
	MarkResponseCommitted(c)
	if protocol == "messages" {
		c.JSON(status, gin.H{"type": "error", "error": gin.H{"type": kind, "message": message}})
	} else {
		c.JSON(status, gin.H{"error": gin.H{"type": kind, "message": message, "param": nil, "code": nil}})
	}
	return errors.New(message)
}

type kiroClientWriter struct {
	c               *gin.Context
	request         *kiroGatewayRequest
	responseState   *apicompat.AnthropicEventToResponsesState
	chatState       *apicompat.ResponsesEventToChatState
	toolRestorer    *apicompat.ResponsesClientToolStreamRestorer
	started         bool
	disconnected    bool
	searchesWritten bool
}

func newKiroClientWriter(c *gin.Context, request *kiroGatewayRequest) *kiroClientWriter {
	state := apicompat.NewAnthropicEventToResponsesState()
	state.Model = request.clientModel
	state.PreserveThinkingSignatures = true
	chat := apicompat.NewResponsesEventToChatState()
	chat.Model, chat.IncludeUsage = request.clientModel, request.includeUsage
	return &kiroClientWriter{c: c, request: request, responseState: state, chatState: chat,
		toolRestorer: apicompat.NewResponsesClientToolStreamRestorer(request.clientTools)}
}

func (w *kiroClientWriter) write(data string) error {
	if !w.started {
		w.c.Header("Content-Type", "text/event-stream")
		w.c.Header("Cache-Control", "no-cache")
		w.c.Header("X-Accel-Buffering", "no")
		w.c.Status(200)
		w.started = true
	}
	if _, err := io.WriteString(w.c.Writer, data); err != nil {
		w.disconnected = true
		return err
	}
	w.c.Writer.Flush()
	return nil
}

func (w *kiroClientWriter) emit(events []apicompat.AnthropicStreamEvent) error {
	for i := range events {
		if events[i].Type == "message_start" && len(w.request.searches) > 0 && !w.searchesWritten {
			w.searchesWritten = true
			if err := w.emit(events[i : i+1]); err != nil {
				return err
			}
			if err := w.emitSearches(); err != nil {
				return err
			}
			continue
		}
		if w.request.protocol == "messages" {
			if events[i].Index != nil {
				index := *events[i].Index + 2*len(w.request.searches)
				events[i].Index = &index
			}
			data, err := apicompat.ResponsesAnthropicEventToSSE(events[i])
			if err != nil {
				return err
			}
			if err := w.write(data); err != nil {
				return err
			}
		} else {
			for _, event := range apicompat.AnthropicEventToResponsesEvents(&events[i], w.responseState) {
				if err := w.responseEvent(event); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (w *kiroClientWriter) responseEvent(event apicompat.ResponsesStreamEvent) error {
	if w.request.protocol == "responses" {
		for _, restored := range w.toolRestorer.Restore(event) {
			data, err := apicompat.ResponsesEventToSSE(restored)
			if err != nil {
				return err
			}
			if err := w.write(data); err != nil {
				return err
			}
		}
		return nil
	}
	for _, chunk := range apicompat.ResponsesEventToChatChunks(&event, w.chatState) {
		data, err := apicompat.ChatChunkToSSE(chunk)
		if err != nil {
			return err
		}
		if err := w.write(data); err != nil {
			return err
		}
	}
	return nil
}

func (w *kiroClientWriter) finish() error {
	if w.request.protocol == "messages" {
		return nil
	}
	for _, event := range apicompat.FinalizeAnthropicResponsesStream(w.responseState) {
		if err := w.responseEvent(event); err != nil {
			return err
		}
	}
	if w.request.protocol == "chat" {
		for _, chunk := range apicompat.FinalizeResponsesChatStream(w.chatState) {
			data, err := apicompat.ChatChunkToSSE(chunk)
			if err != nil {
				return err
			}
			if err := w.write(data); err != nil {
				return err
			}
		}
		return w.write("data: [DONE]\n\n")
	}
	return nil
}

func (w *kiroClientWriter) response(response *apicompat.AnthropicResponse) error {
	if w.request.protocol == "messages" {
		withSearch := *response
		withSearch.Content = append(w.searchBlocks(), response.Content...)
		w.c.JSON(200, &withSearch)
		return nil
	}
	result := apicompat.AnthropicToResponsesResponse(response)
	if w.request.protocol == "chat" {
		w.c.JSON(200, apicompat.ResponsesToChatCompletions(result, w.request.clientModel))
		return nil
	}
	outputs := make([]apicompat.ResponsesOutput, 0, len(w.request.searches)+len(result.Output))
	for _, search := range w.request.searches {
		outputs = append(outputs, kiroSearchOutput(search))
	}
	result.Output = append(outputs, result.Output...)
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	data, _, err = apicompat.RestoreResponsesClientToolPayload(data, w.request.clientTools)
	if err != nil {
		return err
	}
	w.c.Data(200, "application/json", data)
	return nil
}

func kiroSearchOutput(search kiroSearchCall) apicompat.ResponsesOutput {
	return apicompat.ResponsesOutput{ID: search.id, Type: "web_search_call", Status: "completed",
		Action: &apicompat.WebSearchAction{Type: "search", Query: search.query}}
}

func (w *kiroClientWriter) searchBlocks() []apicompat.AnthropicContentBlock {
	var blocks []apicompat.AnthropicContentBlock
	for _, search := range w.request.searches {
		var payload struct {
			Results []struct {
				Title string `json:"title"`
				URL   string `json:"url"`
			} `json:"results"`
		}
		_ = json.Unmarshal(search.results, &payload)
		results := make([]map[string]string, 0, len(payload.Results))
		for _, result := range payload.Results {
			results = append(results, map[string]string{"type": "web_search_result", "title": result.Title,
				"url": result.URL, "encrypted_content": ""})
		}
		content, _ := json.Marshal(results)
		input, _ := json.Marshal(map[string]string{"query": search.query})
		blocks = append(blocks, apicompat.AnthropicContentBlock{Type: "server_tool_use", ID: search.id, Name: "web_search", Input: input},
			apicompat.AnthropicContentBlock{Type: "web_search_tool_result", ToolUseID: search.id, Content: content})
	}
	return blocks
}

func (w *kiroClientWriter) emitSearches() error {
	if w.request.protocol == "messages" {
		for i, block := range w.searchBlocks() {
			index := i
			for _, event := range []apicompat.AnthropicStreamEvent{
				{Type: "content_block_start", Index: &index, ContentBlock: &block},
				{Type: "content_block_stop", Index: &index},
			} {
				data, err := apicompat.ResponsesAnthropicEventToSSE(event)
				if err != nil {
					return err
				}
				if err := w.write(data); err != nil {
					return err
				}
			}
		}
	} else if w.request.protocol == "responses" {
		for _, search := range w.request.searches {
			item := kiroSearchOutput(search)
			index := w.responseState.OutputIndex
			for _, kind := range []string{"response.output_item.added", "response.output_item.done"} {
				w.responseState.SequenceNumber++
				event := apicompat.ResponsesStreamEvent{Type: kind, SequenceNumber: w.responseState.SequenceNumber,
					OutputIndex: index, Item: &item}
				if err := w.responseEvent(event); err != nil {
					return err
				}
			}
			w.responseState.Outputs = append(w.responseState.Outputs, item)
			w.responseState.OutputIndex++
		}
	}
	return nil
}

func (w *kiroClientWriter) streamError(message string) {
	event := gin.H{"type": "error", "error": gin.H{"type": "upstream_error", "message": message}}
	data, _ := json.Marshal(event)
	if w.request.protocol == "chat" {
		_ = w.write("data: " + string(data) + "\n\n")
	} else {
		_ = w.write("event: error\ndata: " + string(data) + "\n\n")
	}
	MarkResponseCommitted(w.c)
}
