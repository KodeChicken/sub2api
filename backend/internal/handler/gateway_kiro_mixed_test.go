//go:build unit

package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type kiroMixedHandlerUpstream struct {
	service.HTTPUpstream
	call func(*http.Request) (*http.Response, error)
}

func (u *kiroMixedHandlerUpstream) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.call(r)
}

func TestGatewayKiroMixedMessagesAndCountThroughSelectedGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"json", "sse", "count_tokens"} {
		t.Run(endpoint, func(t *testing.T) {
			groupID := int64(9300)
			group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 1}
			account := &service.Account{
				ID: 9301, Platform: service.PlatformKiro, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true, Concurrency: 1,
				Extra:       map[string]any{"mixed_scheduling": true},
				Credentials: map[string]any{"api_key": "ksk_test", "model_mapping": map[string]any{"claude-sonnet-4.6": "claude-sonnet-4.6"}},
				GroupIDs:    []int64{groupID}, AccountGroups: []service.AccountGroup{{AccountID: 9301, GroupID: groupID}},
			}
			calls := 0
			upstream := &kiroMixedHandlerUpstream{call: func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "Bearer ksk_test", req.Header.Get("Authorization"))
				require.Empty(t, req.Header.Get("x-api-key"))
				require.Empty(t, req.Header.Get("anthropic-beta"))
				var wire map[string]any
				require.NoError(t, json.NewDecoder(req.Body).Decode(&wire))
				require.Contains(t, wire, "conversationState")
				require.NotContains(t, wire, "messages")
				var buffer bytes.Buffer
				for _, event := range []struct{ kind, body string }{
					{"toolUseEvent", `{"toolUseId":"call","name":"read","input":"{\"path\":\"file.txt\"}","stop":true}`},
					{"metadataEvent", `{"tokenUsage":{"uncachedInputTokens":11,"outputTokens":7},"stopReason":"TOOL_USE"}`},
				} {
					headers := eventstream.Headers{}
					headers.Set(":message-type", eventstream.StringValue("event"))
					headers.Set(":event-type", eventstream.StringValue(event.kind))
					require.NoError(t, eventstream.NewEncoder().Encode(&buffer, eventstream.Message{Headers: headers, Payload: []byte(event.body)}))
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(buffer.Bytes()))}, nil
			}}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{account}}, nil, nil, nil, cfg)
			usage := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 2)}
			billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			defer billingCache.Stop()
			gateway := service.NewGatewayService(
				nil, &fakeGroupRepo{group: group}, usage, &compatStreamUsageBillingRepo{}, nil, nil, nil, nil, cfg,
				snapshot, nil, service.NewBillingService(cfg, nil), nil, nil, nil, upstream,
				&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
				service.NewKiroService(nil, upstream, nil),
			)
			handler := &GatewayHandler{
				gatewayService: gateway, billingCacheService: billingCache, cfg: cfg, maxAccountSwitches: 1,
				concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatClaude, 0),
			}
			body, err := json.Marshal(map[string]any{
				"model": "claude-sonnet-4-6-20260514", "stream": endpoint == "sse", "max_tokens": 128,
				"tools":    []any{map[string]any{"name": "read", "input_schema": map[string]any{"type": "object"}}},
				"messages": []any{map[string]any{"role": "user", "content": "read file"}},
			})
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			apiKey := &service.APIKey{ID: 9302, UserID: 9303, GroupID: &groupID, Group: group, User: &service.User{ID: 9303, Concurrency: 10, Balance: 100}}
			c.Set(string(middleware.ContextKeyAPIKey), apiKey)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9303, Concurrency: 10})
			if endpoint == "count_tokens" {
				handler.CountTokens(c)
				require.Zero(t, calls)
				require.Equal(t, "estimate", recorder.Header().Get("X-Sub2api-Usage-Source"))
			} else {
				handler.Messages(c)
				require.Equal(t, 1, calls)
				require.Contains(t, recorder.Body.String(), "tool_use")
				if endpoint == "sse" {
					require.Contains(t, recorder.Body.String(), "message_stop")
				}
				selected, ok := c.Get(opsAccountIDKey)
				require.True(t, ok)
				require.Equal(t, account.ID, selected)
			}
			require.Equal(t, 200, recorder.Code, recorder.Body.String())
		})
	}
}
