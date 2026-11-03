package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/gin-gonic/gin"
)

func (s *AccountTestService) RefreshKiroAccount(ctx context.Context, account *Account) (*Account, error) {
	if s.kiroService == nil || s.kiroService.refreshAPI == nil {
		return nil, errors.New("Kiro service is not configured")
	}
	result, err := s.kiroService.refreshAPI.RefreshIfNeeded(ctx, account,
		kiroForcedRefresh{KiroService: s.kiroService, rejectedToken: account.GetCredential("access_token")}, 0)
	if err != nil {
		return nil, err
	}
	if result == nil || result.LockHeld || result.Account == nil {
		return nil, errors.New("Kiro token refresh is already in progress; retry shortly")
	}
	return result.Account, nil
}

func (s *AccountTestService) testKiroAccountConnection(c *gin.Context, account *Account, model, prompt string) error {
	if s.kiroService == nil {
		return s.sendErrorAndEnd(c, "Kiro service is not configured")
	}
	if model == "" {
		mapping := account.GetModelMapping()
		if len(mapping) > 0 {
			keys := make([]string, 0, len(mapping))
			for key := range mapping {
				keys = append(keys, key)
			}
			model = dedupeAndSortModelIDs(keys)[0]
		} else {
			model = "claude-sonnet-4.6"
		}
	}
	if prompt == "" {
		prompt = "Reply with sub2api-ok"
	}
	s.sendEvent(c, TestEvent{Type: "test_start", Model: model})
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()
	request := &apicompat.AnthropicRequest{Model: account.GetMappedModel(model), MaxTokens: 128,
		Messages: []apicompat.AnthropicMessage{{Role: "user", Content: quoteKiroText(prompt)}}}
	resp, conversion, _, err := s.kiroService.openInference(ctx, account, request, "admin-test:"+strconv.FormatInt(account.ID, 10))
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	defer resp.Body.Close()
	stream := kiro.NewStream(model, conversion.ToolNames)
	err = consumeKiroStream(ctx, resp, stream, func(events []apicompat.AnthropicStreamEvent) error {
		for _, event := range events {
			if event.Delta != nil && event.Delta.Type == "text_delta" {
				s.sendEvent(c, TestEvent{Type: "content", Text: event.Delta.Text})
			}
		}
		return nil
	})
	if err == nil {
		_, err = stream.Finish(0, estimateKiroOutput(stream.Response.Content))
	}
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

func (s *AccountUsageService) getKiroUsage(ctx context.Context, account *Account, force bool) (*UsageInfo, error) {
	if s.kiroService == nil {
		return nil, errors.New("Kiro service is not configured")
	}
	if s.cache == nil {
		return s.kiroService.Usage(ctx, account)
	}
	if !force {
		if cached, ok := s.cache.antigravityCache.Load(account.ID); ok {
			entry := cached.(*antigravityUsageCache)
			ttl := apiCacheTTL
			if entry.usageInfo.Error != "" {
				ttl = apiErrorCacheTTL
			}
			if time.Since(entry.timestamp) < ttl {
				return entry.usageInfo, nil
			}
		}
	}
	value, err, _ := s.cache.apiFlight.Do("kiro:"+strconv.FormatInt(account.ID, 10), func() (any, error) {
		info, err := s.kiroService.Usage(ctx, account)
		if err != nil {
			info = &UsageInfo{Source: "active", Error: "Kiro quota query failed; verify credentials and account permissions"}
		}
		s.cache.antigravityCache.Store(account.ID, &antigravityUsageCache{usageInfo: info, timestamp: time.Now()})
		return info, nil
	})
	if err != nil {
		return nil, err
	}
	return value.(*UsageInfo), nil
}
