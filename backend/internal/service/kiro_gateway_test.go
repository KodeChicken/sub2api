//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type kiroTestTransport struct {
	HTTPUpstream
	call func(*http.Request) (*http.Response, error)
}

func (t *kiroTestTransport) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return t.call(request)
}

type kiroTestAccounts struct {
	AccountRepository
	mu          sync.Mutex
	account     *Account
	casCalls    int
	beforeCAS   func()
	quarantines int
	errors      int
}

func (r *kiroTestAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account == nil || r.account.ID != id {
		return nil, errors.New("missing account")
	}
	out := *r.account
	out.Credentials, out.Extra = maps.Clone(out.Credentials), maps.Clone(out.Extra)
	return &out, nil
}

func (r *kiroTestAccounts) UpdateKiroOAuthCredentialsIfUnchanged(_ context.Context, id int64, expected map[string]any, proxy *int64, next map[string]any) (bool, error) {
	if r.beforeCAS != nil {
		r.beforeCAS()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.casCalls++
	if r.account.ID != id || !reflect.DeepEqual(expected, r.account.Credentials) || !reflect.DeepEqual(proxy, r.account.ProxyID) {
		return false, nil
	}
	r.account.Credentials = maps.Clone(next)
	return true, nil
}

func (r *kiroTestAccounts) SetKiroOAuthRefreshFailureIfUnchanged(_ context.Context, id int64, expected map[string]any, _ *int64, cooldown *time.Time, _ string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account.ID != id || !reflect.DeepEqual(expected, r.account.Credentials) {
		return false, nil
	}
	if cooldown == nil {
		r.errors++
	} else {
		r.quarantines++
	}
	return true, nil
}

func (r *kiroTestAccounts) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account.ID != id {
		return errors.New("wrong account")
	}
	if r.account.Extra == nil {
		r.account.Extra = map[string]any{}
	}
	for key, value := range updates {
		r.account.Extra[key] = value
	}
	return nil
}

func (r *kiroTestAccounts) SetRateLimited(context.Context, int64, time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.quarantines++
	return nil
}

func (r *kiroTestAccounts) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.quarantines++
	return nil
}

func (r *kiroTestAccounts) SetError(context.Context, int64, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors++
	return nil
}

func kiroTestAccount() *Account {
	return &Account{ID: 42, Name: "kiro-test", Platform: PlatformKiro, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "ksk_example", "model_mapping": map[string]any{"alias": "claude-sonnet-4.6"}}}
}

func kiroTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func kiroTestFrame(t *testing.T, kind, data string) []byte {
	t.Helper()
	var buf bytes.Buffer
	headers := eventstream.Headers{}
	headers.Set(":message-type", eventstream.StringValue("event"))
	headers.Set(":event-type", eventstream.StringValue(kind))
	require.NoError(t, eventstream.NewEncoder().Encode(&buf, eventstream.Message{Headers: headers, Payload: []byte(data)}))
	return buf.Bytes()
}

func kiroTestSuccess(t *testing.T, content string) *http.Response {
	t.Helper()
	text, err := json.Marshal(map[string]string{"content": content})
	require.NoError(t, err)
	body := append(kiroTestFrame(t, "assistantResponseEvent", string(text)),
		kiroTestFrame(t, "metadataEvent", `{"tokenUsage":{"uncachedInputTokens":11,"outputTokens":7,"cacheReadInputTokens":21,"cacheWriteInputTokens":3},"stopReason":"END_TURN"}`)...)
	resp := kiroTestResponse(200, string(body))
	resp.Header.Set("Content-Type", "application/vnd.amazon.eventstream")
	resp.Header.Set("x-amzn-requestid", "kiro-request")
	return resp
}

func TestKiroNativeGatewayAllProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"messages", "responses", "chat"} {
		for _, streaming := range []bool{false, true} {
			t.Run(protocol+map[bool]string{false: "/json", true: "/stream"}[streaming], func(t *testing.T) {
				account := kiroTestAccount()
				repo := &kiroTestAccounts{account: account}
				calls := 0
				transport := &kiroTestTransport{call: func(req *http.Request) (*http.Response, error) {
					calls++
					require.Equal(t, "q.us-east-1.amazonaws.com", req.URL.Host)
					require.Equal(t, "/", req.URL.Path)
					require.Equal(t, "AmazonCodeWhispererStreamingService.GenerateAssistantResponse", req.Header.Get("x-amz-target"))
					require.Equal(t, "Bearer ksk_example", req.Header.Get("Authorization"))
					require.Empty(t, req.Header.Get("anthropic-beta"))
					var request map[string]any
					require.NoError(t, json.NewDecoder(req.Body).Decode(&request))
					require.NotContains(t, request, "messages")
					require.NotContains(t, request, "profileArn")
					current := request["conversationState"].(map[string]any)["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
					require.Equal(t, "claude-sonnet-4.6", current["modelId"])
					require.Equal(t, "KIRO_CLI", current["origin"])
					return kiroTestSuccess(t, "sub2api-ok"), nil
				}}
				service := NewKiroService(repo, transport, NewOAuthRefreshAPI(repo, nil))
				gateway := &GatewayService{kiroService: service}
				input := map[string]any{"model": "alias", "stream": streaming, "max_tokens": 128}
				switch protocol {
				case "messages", "chat":
					input["messages"] = []map[string]string{{"role": "user", "content": "hello"}}
				case "responses":
					input["input"] = "hello"
				}
				body, err := json.Marshal(input)
				require.NoError(t, err)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, bytes.NewReader(body))
				parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "alias", Stream: streaming}
				var result *ForwardResult
				switch protocol {
				case "messages":
					result, err = gateway.Forward(context.Background(), c, account, parsed)
				case "responses":
					result, err = gateway.ForwardAsResponses(context.Background(), c, account, body, parsed)
				case "chat":
					result, err = gateway.ForwardAsChatCompletions(context.Background(), c, account, body, parsed)
				}
				require.NoError(t, err)
				require.Equal(t, 1, calls)
				require.Equal(t, 200, recorder.Code)
				require.Contains(t, recorder.Body.String(), "sub2api-ok")
				require.Equal(t, "alias", result.Model)
				require.Equal(t, "claude-sonnet-4.6", result.UpstreamModel)
				require.Equal(t, 11, result.Usage.InputTokens)
				require.Equal(t, 7, result.Usage.OutputTokens)
				require.Equal(t, 21, result.Usage.CacheReadInputTokens)
				require.Equal(t, 3, result.Usage.CacheCreationInputTokens)
				if streaming {
					terminal := map[string]string{"messages": "message_stop", "responses": "response.completed", "chat": "[DONE]"}[protocol]
					require.Contains(t, recorder.Body.String(), terminal)
				} else {
					require.True(t, json.Valid(recorder.Body.Bytes()))
				}
			})
		}
	}
}

func TestKiroRefreshRotatesAndPreservesCredentials(t *testing.T) {
	for _, method := range []string{"social", "idc", "external_idp"} {
		t.Run(method, func(t *testing.T) {
			account := kiroTestAccount()
			account.Type = AccountTypeOAuth
			account.Credentials = map[string]any{"access_token": "expired", "refresh_token": "refresh",
				"expires_at": time.Now().Add(-time.Hour).Format(time.RFC3339), "auth_method": method,
				"client_id": "client", "client_secret": "secret", "profile_arn": "arn:profile",
				"token_endpoint": "https://login.microsoftonline.com/tenant/oauth2/v2.0/token", "custom_setting": true}
			repo := &kiroTestAccounts{account: account}
			calls := 0
			transport := &kiroTestTransport{call: func(req *http.Request) (*http.Response, error) {
				calls++
				require.Empty(t, req.Header.Get("Authorization"), "refresh tokens must not be sent as inference credentials")
				if method == "external_idp" {
					require.Equal(t, "login.microsoftonline.com", req.URL.Host)
					body, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.Contains(t, string(body), "grant_type=refresh_token")
					return kiroTestResponse(200, `{"access_token":"new","refresh_token":"rotated","expires_in":3600}`), nil
				}
				if method == "social" {
					require.Equal(t, "prod.us-east-1.auth.desktop.kiro.dev", req.URL.Host)
				} else {
					require.Equal(t, "oidc.us-east-1.amazonaws.com", req.URL.Host)
				}
				return kiroTestResponse(200, `{"accessToken":"new","refreshToken":"rotated","expiresIn":3600}`), nil
			}}
			service := NewKiroService(repo, transport, NewOAuthRefreshAPI(repo, nil))
			fresh, token, err := service.AccessToken(context.Background(), account, "")
			require.NoError(t, err)
			require.Equal(t, "new", token)
			require.Equal(t, "rotated", fresh.GetCredential("refresh_token"))
			require.Equal(t, true, fresh.Credentials["custom_setting"])
			require.Equal(t, 1, repo.casCalls)
			_, token, err = service.AccessToken(context.Background(), account, "")
			require.NoError(t, err)
			require.Equal(t, "new", token)
			require.Equal(t, 1, calls, "a stale scheduler account must not trigger a second refresh")
		})
	}
}

func TestKiroRefreshDoesNotOverwriteReauthorization(t *testing.T) {
	account := kiroTestAccount()
	account.Type = AccountTypeOAuth
	account.Credentials = map[string]any{"access_token": "expired", "refresh_token": "old",
		"expires_at": time.Now().Add(-time.Hour).Format(time.RFC3339)}
	repo := &kiroTestAccounts{account: account}
	repo.beforeCAS = func() {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		repo.account.Credentials = map[string]any{"access_token": "admin-new", "refresh_token": "admin-refresh",
			"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339)}
	}
	transport := &kiroTestTransport{call: func(*http.Request) (*http.Response, error) {
		return kiroTestResponse(200, `{"accessToken":"stale-provider-token","refreshToken":"stale-refresh","expiresIn":3600}`), nil
	}}
	service := NewKiroService(repo, transport, NewOAuthRefreshAPI(repo, nil))
	_, token, err := service.AccessToken(context.Background(), account, "")
	require.NoError(t, err)
	require.Equal(t, "admin-new", token)
	require.Equal(t, "admin-refresh", repo.account.GetCredential("refresh_token"))
}

func TestKiroRequestAndCapacityErrorsDoNotQuarantineAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		status      int
		reason      string
		quarantines int
		retry       bool
	}{
		{400, "", 0, false},
		{429, "INSUFFICIENT_MODEL_CAPACITY", 0, true},
		{429, "SERVICE_REQUEST_RATE_EXCEEDED", 1, false},
		{402, "MONTHLY_REQUEST_COUNT", 1, false},
	} {
		account := kiroTestAccount()
		repo := &kiroTestAccounts{account: account}
		service := NewKiroService(repo, nil, nil)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		err := service.gatewayFailure(context.Background(), c, account, "messages",
			&kiroHTTPError{Status: tc.status, Header: make(http.Header), Reason: tc.reason, Body: []byte(`{"error":"test"}`)})
		require.Error(t, err)
		require.Equal(t, tc.quarantines, repo.quarantines, tc.reason)
		if tc.retry {
			var failure *UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.True(t, failure.RequestScopedTransient)
			require.True(t, failure.RetryableOnSameAccount)
		}
	}
}

func TestKiroPartialStreamDoesNotEmitSuccessOrFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"messages", "responses", "chat"} {
		t.Run(protocol, func(t *testing.T) {
			account := kiroTestAccount()
			body := append(kiroTestFrame(t, "assistantResponseEvent", `{"content":"partial"}`), []byte{0, 1}...)
			calls := 0
			service := NewKiroService(nil, &kiroTestTransport{call: func(*http.Request) (*http.Response, error) {
				calls++
				return kiroTestResponse(200, string(body)), nil
			}}, nil)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			request := &kiroGatewayRequest{protocol: protocol, clientModel: "model",
				request: &apicompat.AnthropicRequest{Model: "model", Stream: true,
					Messages: []apicompat.AnthropicMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}}}}
			result, err := service.forward(context.Background(), c, account, request, nil)
			require.Error(t, err)
			require.NotNil(t, result)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover))
			require.Equal(t, 1, calls)
			require.Contains(t, recorder.Body.String(), "partial")
			require.Contains(t, recorder.Body.String(), "upstream_error")
			require.NotContains(t, recorder.Body.String(), "message_stop")
			require.NotContains(t, recorder.Body.String(), "response.completed")
			require.NotContains(t, recorder.Body.String(), "[DONE]")
			require.Positive(t, result.Usage.InputTokens)
			require.Positive(t, result.Usage.OutputTokens)
		})
	}
}

func TestKiroSearchUsesNativeMCPAndAccumulatesUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"messages", "responses", "chat"} {
		for _, streaming := range []bool{false, true} {
			t.Run(protocol+map[bool]string{false: "/json", true: "/stream"}[streaming], func(t *testing.T) {
				account := kiroTestAccount()
				inferences, searches := 0, 0
				service := NewKiroService(nil, &kiroTestTransport{call: func(req *http.Request) (*http.Response, error) {
					require.Equal(t, "q.us-east-1.amazonaws.com", req.URL.Host)
					if req.URL.Path == "/mcp" {
						searches++
						require.Equal(t, "API_KEY", req.Header.Get("tokentype"))
						return kiroTestResponse(200, `{"jsonrpc":"2.0","result":{"content":[{"type":"text","text":"{\"results\":[{\"title\":\"Example\",\"url\":\"https://example.com\",\"snippet\":\"result\"}]}"}]}}`), nil
					}
					inferences++
					if inferences == 1 {
						data := append(kiroTestFrame(t, "toolUseEvent", `{"toolUseId":"search","name":"web_search","input":"{\"query\":\"test\"}","stop":true}`),
							kiroTestFrame(t, "metadataEvent", `{"tokenUsage":{"uncachedInputTokens":2,"outputTokens":3},"stopReason":"TOOL_USE"}`)...)
						return kiroTestResponse(200, string(data)), nil
					}
					payload, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.Contains(t, string(payload), "example.com")
					return kiroTestSuccess(t, "Search answer"), nil
				}}, nil)
				request := &kiroGatewayRequest{protocol: protocol, clientModel: "model",
					request: &apicompat.AnthropicRequest{Model: "model", Stream: streaming,
						Messages: []apicompat.AnthropicMessage{{Role: "user", Content: json.RawMessage(`"search"`)}},
						Tools:    []apicompat.AnthropicTool{{Type: "web_search_20250305", Name: "web_search"}}}}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
				result, err := service.forward(context.Background(), c, account, request, nil)
				require.NoError(t, err)
				require.Equal(t, 2, inferences)
				require.Equal(t, 1, searches)
				require.Equal(t, 13, result.Usage.InputTokens)
				require.Equal(t, 10, result.Usage.OutputTokens)
				require.Equal(t, 1, result.SearchCount)
				require.Contains(t, recorder.Body.String(), "Search answer")
				if protocol == "messages" {
					require.Contains(t, recorder.Body.String(), "web_search_tool_result")
				} else if protocol == "responses" {
					require.Contains(t, recorder.Body.String(), "web_search_call")
				}
			})
		}
	}
}

func TestKiroCredentialsNeverUseAnthropicTransport(t *testing.T) {
	for _, typ := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		account := &Account{Platform: PlatformKiro, Type: typ}
		require.False(t, anthropicMessagesCredentialAllowed(account))
		req, _, err := (&GatewayService{}).buildUpstreamRequest(context.Background(), nil, account, []byte(`{}`),
			"must-not-leak", "oauth", "model", true, false)
		require.Nil(t, req)
		require.Error(t, err)
		require.False(t, IsUpstreamBillingProbeIdentity(PlatformKiro, typ))
	}
}

func TestKiroQuotaUsesOnlyKnownActiveCredits(t *testing.T) {
	now := time.Unix(1000, 0)
	info, err := parseKiroUsage([]byte(`{
		"nextDateReset":4000,
		"subscriptionInfo":{"subscriptionTitle":"KIRO PRO"},
		"usageBreakdownList":[
			{"resourceType":"OTHER","currentUsage":0,"usageLimit":9999},
			{"resourceType":"CREDIT","currentUsageWithPrecision":50.5,"usageLimit":100,
			 "currentOveragesWithPrecision":900,
			 "freeTrialInfo":{"freeTrialStatus":"ACTIVE","freeTrialExpiry":2000,"currentUsage":20,"usageLimit":50},
			 "bonuses":[{"status":"ACTIVE","expiresOn":2000,"currentUsage":10,"usageLimit":25},
			 {"status":"ACTIVE","expiresOn":900,"currentUsage":0,"usageLimit":500}]}
		]}`), now)
	require.NoError(t, err)
	require.InDelta(t, 80.5/175*100, info.ThirtyDay.Utilization, 0.001)
	require.InDelta(t, 94.5, info.AICredits[0].Amount, 0.001)
	require.Equal(t, int64(4000), info.ThirtyDay.ResetsAt.Unix())
	for _, body := range []string{`{"usageBreakdownList":[]}`, `{"usageBreakdownList":[{"usageLimit":100}]}`} {
		info, err := parseKiroUsage([]byte(body), now)
		require.NoError(t, err)
		require.Nil(t, info.ThirtyDay, "unknown usage is not a zero-usage observation")
	}
}

func TestKiroRejectsUnsupportedOptionsInsteadOfDroppingThem(t *testing.T) {
	for _, tc := range []struct{ protocol, body string }{
		{"messages", `{"model":"m","messages":[{"role":"user","content":"hi"}],"output_config":{"format":{"type":"json_schema","schema":{"type":"object"}}}}`},
		{"responses", `{"model":"m","input":"hi","text":{"format":{"type":"json_schema"}}}`},
		{"chat", `{"model":"m","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_object"}}`},
		{"responses", `{"model":"m","input":"hi","previous_response_id":"resp_previous"}`},
		{"responses", `{"model":"m","input":[{"type":"compaction_trigger"},{"role":"user","content":"hi"}]}`},
		{"responses", `{"model":"m","input":[{"type":"compaction","encrypted_content":"foreign"},{"role":"user","content":"hi"}]}`},
		{"messages", `{"model":"m","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"tool","name":"read"}}`},
	} {
		_, err := parseKiroGatewayRequest([]byte(tc.body), tc.protocol)
		require.Error(t, err, tc.body)
	}
	for _, protocol := range []string{"responses", "chat"} {
		body := `{"model":"gpt-5.6-sol","input":"hi","reasoning":{"effort":"xhigh"}}`
		if protocol == "chat" {
			body = `{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"xhigh"}`
		}
		request, err := parseKiroGatewayRequest([]byte(body), protocol)
		require.NoError(t, err)
		require.False(t, request.request.Stream)
		require.Equal(t, "xhigh", request.request.OutputConfig.Effort)
	}
}

func TestKiroCompactionRoundTripAndTruncation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, streaming := range []bool{false, true} {
		for _, reason := range []string{"END_TURN", "MAX_TOKENS", "MODEL_CONTEXT_WINDOW_EXCEEDED", "CONTENT_FILTERED"} {
			t.Run(map[bool]string{false: "json", true: "stream"}[streaming]+"/"+reason, func(t *testing.T) {
				account := kiroTestAccount()
				service := NewKiroService(nil, &kiroTestTransport{call: func(req *http.Request) (*http.Response, error) {
					data, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.Contains(t, string(data), "compact continuation summary")
					require.NotContains(t, string(data), "toolSpecification")
					resp := kiroTestSuccess(t, "Summary of prior work.")
					raw, err := io.ReadAll(resp.Body)
					require.NoError(t, err)
					raw = append(raw, kiroTestFrame(t, "metadataEvent", `{"stopReason":"`+reason+`"}`)...)
					resp.Body = io.NopCloser(bytes.NewReader(raw))
					return resp, nil
				}}, nil)
				body, err := json.Marshal(map[string]any{"model": "model", "stream": streaming, "input": []map[string]any{
					{"role": "user", "content": "history"}, {"role": "assistant", "content": "previous answer"}, {"type": "compaction_trigger"},
				}})
				require.NoError(t, err)
				request, err := parseKiroGatewayRequest(body, "responses")
				require.NoError(t, err)
				require.True(t, request.compact)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				result, err := service.forward(context.Background(), c, account, request, nil)
				require.NoError(t, err)
				require.Equal(t, 11, result.Usage.InputTokens)
				if reason == "END_TURN" {
					require.Contains(t, recorder.Body.String(), `"type":"compaction"`)
					if !streaming {
						var response apicompat.ResponsesResponse
						require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
						input, err := json.Marshal([]map[string]any{
							{"type": "compaction", "encrypted_content": response.Output[0].EncryptedContent},
							{"role": "user", "content": "continue"},
						})
						require.NoError(t, err)
						req := &apicompat.ResponsesRequest{Input: input}
						compact, err := prepareKiroResponsesInput(req)
						require.NoError(t, err)
						require.False(t, compact)
						require.Contains(t, string(req.Input), "Summary of prior work.")
					}
				} else {
					require.NotContains(t, recorder.Body.String(), `"type":"compaction"`)
					require.NotContains(t, recorder.Body.String(), "response.completed")
				}
				if !streaming {
					require.True(t, json.Valid(recorder.Body.Bytes()))
				}
			})
		}
	}
}

func TestKiroEnterpriseProfileUsedByRESTAndInvalidatedOnReauthorization(t *testing.T) {
	account := kiroTestAccount()
	account.Type = AccountTypeOAuth
	account.Credentials = map[string]any{"auth_method": "idc", "client_id": "client", "client_secret": "secret",
		"refresh_token": "refresh", "access_token": "valid", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339)}
	repo := &kiroTestAccounts{account: account}
	discoveries := 0
	service := NewKiroService(repo, &kiroTestTransport{call: func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/":
			discoveries++
			require.Contains(t, req.Header.Get("x-amz-target"), "ListAvailableProfiles")
			return kiroTestResponse(200, `{"profiles":[{"arn":"arn:aws:enterprise"}]}`), nil
		case "/ListAvailableModels":
			require.Equal(t, "arn:aws:enterprise", req.URL.Query().Get("profileArn"))
			return kiroTestResponse(200, `{"models":[{"modelId":"native-model","modelName":"Native Model","tokenLimits":{"maxInputTokens":12345,"maxOutputTokens":678}}]}`), nil
		default:
			require.Equal(t, "/getUsageLimits", req.URL.Path)
			require.Equal(t, "arn:aws:enterprise", req.URL.Query().Get("profileArn"))
			return kiroTestResponse(200, `{"usageBreakdownList":[{"resourceType":"CREDIT","usageLimit":100,"currentUsage":1}]}`), nil
		}
	}}, nil)
	models, body, err := service.Models(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, []string{"native-model"}, models)
	_, metadata, err := extractUpstreamModelCatalog(body, false)
	require.NoError(t, err)
	require.Equal(t, int64(12345), metadata["native-model"].ContextWindow)
	require.Equal(t, "Native Model", metadata["native-model"].DisplayName)
	_, err = service.Usage(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, 1, discoveries)
	repo.account.Credentials["refresh_token"] = "reauthorized"
	_, err = service.Usage(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, 2, discoveries)
	require.Empty(t, repo.account.GetCredential("profile_arn"), "discovery cache must not rewrite credentials")
}

func TestKiroCancellationClosesBlockedBody(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		resp := &http.Response{Body: reader}
		done <- consumeKiroStream(ctx, resp, kiro.NewStream("model", nil), func([]apicompat.AnthropicStreamEvent) error { return nil })
	}()
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("Kiro decoder did not unblock on cancellation")
	}
}

func TestKiroResponsesSearchDeclarationAndHistory(t *testing.T) {
	request, err := parseKiroGatewayRequest([]byte(`{
		"model":"native-model",
		"tools":[{"type":"web_search_preview"}],
		"input":[
			{"role":"user","content":"search"},
			{"type":"web_search_call","id":"srvtoolu_previous","status":"completed","action":{"type":"search","query":"previous query"}},
			{"role":"assistant","content":"previous answer"},
			{"role":"user","content":"continue"}
		]
	}`), "responses")
	require.NoError(t, err)
	require.True(t, kiroHasSearch(request.request))
	converted, err := kiro.Convert(request.request, kiro.Credentials{}, "test")
	require.NoError(t, err)
	require.Contains(t, converted.Request.ConversationState.History[1].Assistant.Content, "previous query")
}
