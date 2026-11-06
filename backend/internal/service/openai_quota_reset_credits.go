package service

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type openAIRateLimitResetCreditDetailPayload struct {
	ID             string `json:"id,omitempty"`
	CreditID       string `json:"credit_id,omitempty"`
	CreditIDCamel  string `json:"creditId,omitempty"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	ExpiresAtCamel string `json:"expiresAt,omitempty"`
	ResetType      string `json:"reset_type,omitempty"`
	ResetTypeCamel string `json:"resetType,omitempty"`
	Status         string `json:"status,omitempty"`
}

type openAIRateLimitResetCreditDetailsPayload struct {
	AvailableCount        json.RawMessage `json:"available_count,omitempty"`
	AvailableCountCamel   json.RawMessage `json:"availableCount,omitempty"`
	Credits               json.RawMessage `json:"credits,omitempty"`
	RateLimitResetCredits json.RawMessage `json:"rate_limit_reset_credits,omitempty"`
	Items                 json.RawMessage `json:"items,omitempty"`
	Data                  json.RawMessage `json:"data,omitempty"`
	HasMore               json.RawMessage `json:"has_more,omitempty"`
	NextCursor            json.RawMessage `json:"next_cursor,omitempty"`
	TotalCount            json.RawMessage `json:"total_count,omitempty"`
}

type openAIRateLimitResetCreditDetails struct {
	AvailableCount       *int
	AvailableCreditCount int
	CreditListPresent    bool
	Credits              []OpenAIRateLimitResetCreditDetail
	AutoResetCandidates  []openAIAutoResetCreditCandidate
	AutoResetInvalid     bool
}

// openAIAutoResetCreditCandidate 仅在服务内部流转。上游卡 ID 不进入 API DTO、
// 账号 extra 或日志，避免管理端响应扩大敏感标识暴露面。
type openAIAutoResetCreditCandidate struct {
	ID        string
	ExpiresAt string
}

func parseOpenAIRateLimitResetCreditDetails(body []byte) (openAIRateLimitResetCreditDetails, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return openAIRateLimitResetCreditDetails{}, nil
	}

	var rawCredits []*openAIRateLimitResetCreditDetailPayload
	var availableCount *int
	var creditListPresent bool
	autoResetInvalid := false
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &rawCredits); err != nil {
			return openAIRateLimitResetCreditDetails{}, err
		}
		creditListPresent = true
	} else {
		var payload openAIRateLimitResetCreditDetailsPayload
		if err := json.Unmarshal(trimmed, &payload); err != nil {
			return openAIRateLimitResetCreditDetails{}, err
		}
		availableCount = parseOpenAIResetCreditAvailableCount(payload.AvailableCount, payload.AvailableCountCamel)
		for _, raw := range []json.RawMessage{payload.AvailableCount, payload.AvailableCountCamel} {
			if resetCreditCountPresent(raw) && parseOpenAIResetCreditAvailableCount(raw) == nil {
				autoResetInvalid = true
			}
		}
		if left, right := parseOpenAIResetCreditAvailableCount(payload.AvailableCount), parseOpenAIResetCreditAvailableCount(payload.AvailableCountCamel); left != nil && right != nil && *left != *right {
			autoResetInvalid = true
		}
		if resetCreditCountPresent(payload.HasMore) && !bytes.Equal(bytes.TrimSpace(payload.HasMore), []byte("false")) {
			autoResetInvalid = true
		}
		if resetCreditCountPresent(payload.NextCursor) && !bytes.Equal(bytes.TrimSpace(payload.NextCursor), []byte(`""`)) {
			autoResetInvalid = true
		}
		var err error
		rawCredits, creditListPresent, err = firstPresentResetCreditPayload(
			payload.Credits,
			payload.RateLimitResetCredits,
			payload.Items,
			payload.Data,
		)
		if err != nil {
			return openAIRateLimitResetCreditDetails{AvailableCount: availableCount}, err
		}
		if resetCreditCountPresent(payload.TotalCount) {
			total := parseOpenAIResetCreditAvailableCount(payload.TotalCount)
			if total == nil || *total != len(rawCredits) {
				autoResetInvalid = true
			}
		}
	}

	credits := make([]OpenAIRateLimitResetCreditDetail, 0, len(rawCredits))
	autoResetCandidates := make([]openAIAutoResetCreditCandidate, 0, len(rawCredits))
	availableCreditCount := 0
	for _, raw := range rawCredits {
		if raw == nil {
			autoResetInvalid = true
			continue
		}
		resetType := strings.TrimSpace(raw.ResetType)
		if resetType == "" {
			resetType = strings.TrimSpace(raw.ResetTypeCamel)
		}
		if resetType != "" && !strings.EqualFold(resetType, "codex_rate_limits") {
			continue
		}
		if status := strings.TrimSpace(raw.Status); status != "" && !strings.EqualFold(status, "available") {
			continue
		}
		availableCreditCount++
		expiresAt := strings.TrimSpace(raw.ExpiresAt)
		if expiresAt == "" {
			expiresAt = strings.TrimSpace(raw.ExpiresAtCamel)
		}
		if expiresAt != "" {
			credits = append(credits, OpenAIRateLimitResetCreditDetail{ExpiresAt: expiresAt})
		}
		if raw.ExpiresAt != "" && raw.ExpiresAtCamel != "" {
			left, leftErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw.ExpiresAt))
			right, rightErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw.ExpiresAtCamel))
			autoResetInvalid = autoResetInvalid || leftErr != nil || rightErr != nil || !left.Equal(right)
		}
		creditID := ""
		for _, value := range []string{raw.CreditID, raw.CreditIDCamel, raw.ID} {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if creditID != "" && creditID != value {
				autoResetInvalid = true
			}
			creditID = value
		}
		autoResetCandidates = append(autoResetCandidates, openAIAutoResetCreditCandidate{
			ID:        creditID,
			ExpiresAt: expiresAt,
		})
	}
	return openAIRateLimitResetCreditDetails{
		AvailableCount:       availableCount,
		AvailableCreditCount: availableCreditCount,
		CreditListPresent:    creditListPresent,
		Credits:              credits,
		AutoResetCandidates:  autoResetCandidates,
		AutoResetInvalid:     autoResetInvalid,
	}, nil
}

func resetCreditCountPresent(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func parseOpenAIResetCreditAvailableCount(values ...json.RawMessage) *int {
	for _, value := range values {
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			continue
		}

		var count int
		if trimmed[0] == '"' {
			var text string
			if err := json.Unmarshal(trimmed, &text); err != nil {
				continue
			}
			parsed, err := strconv.Atoi(strings.TrimSpace(text))
			if err != nil {
				continue
			}
			count = parsed
		} else if err := json.Unmarshal(trimmed, &count); err != nil {
			continue
		}
		if count >= 0 {
			return &count
		}
	}
	return nil
}

func firstPresentResetCreditPayload(values ...json.RawMessage) ([]*openAIRateLimitResetCreditDetailPayload, bool, error) {
	for _, value := range values {
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			continue
		}
		var credits []*openAIRateLimitResetCreditDetailPayload
		if err := json.Unmarshal(trimmed, &credits); err != nil {
			return nil, false, err
		}
		return credits, true, nil
	}
	return nil, false, nil
}
