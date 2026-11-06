package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/google/uuid"
)

const (
	openAIAutoResetScanInterval  = time.Minute
	openAIAutoResetSnapshotTTL   = openAIProbeCacheTTL
	openAIAutoResetBatchSize     = 100
	openAIAutoResetWorkerCount   = 4
	openAIAutoResetQueueCapacity = 1024
	openAIAutoResetAttemptTTL    = 8 * 24 * time.Hour
	openAIAutoResetLeaderLockKey = "jobs:openai-auto-reset-credit"
	openAIAutoResetExpiryWindow  = 10 * time.Minute
	// 查询阶段失败后的最短重试间隔。调度热路径和扫描都会触发评估，不设间隔时上游
	// 故障期间同一账号会被十几秒重查一次；用卡阶段的失败不受此限，超时后仍立即用
	// 同一幂等键重试。
	openAIAutoResetQueryFailureRetryAfter = time.Minute
	// 调度热路径每次过滤候选都会评估暂停，对同一账号的通知按此冷却合并；
	// 后台每分钟全量扫描兜底，冷却不会让账号漏检。
	openAIAutoResetSchedulerNotifyCooldown = 30 * time.Second
)

const (
	OpenAIAutoResetStatusChecking  = "checking"
	OpenAIAutoResetStatusAvailable = "available"
	OpenAIAutoResetStatusResetting = "resetting"
	OpenAIAutoResetStatusSuccess   = "success"
	OpenAIAutoResetStatusNoCredit  = "no_credit"
	OpenAIAutoResetStatusFailed    = "failed"
)

// OpenAIAutoResetCreditState 不保存卡 ID 或兑换 ID；尝试指纹和到期时间用于重启后
// 继续原请求，只有确认消费或自然过期后才能评估下一张卡。
type OpenAIAutoResetCreditState struct {
	Status            string `json:"status"`
	TriggerWindow     string `json:"trigger_window,omitempty"`
	AvailableCount    int    `json:"available_count"`
	CheckedAt         string `json:"checked_at,omitempty"`
	LastResultAt      string `json:"last_result_at,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
	AttemptCycleHash  string `json:"attempt_cycle_hash,omitempty"`
	AttemptCreditHash string `json:"attempt_credit_hash,omitempty"`
	ExpiryInitialized bool   `json:"expiry_initialized,omitempty"`
	AttemptExpiresAt  string `json:"attempt_expires_at,omitempty"`
	AttemptConsumed   bool   `json:"attempt_consumed,omitempty"`
	// "unknown" holds indefinitely; a trusted deadline only releases after a complete fresh read.
	UnresolvedEarliestUntil string `json:"unresolved_earliest_until,omitempty"`
}

type openAIAutoResetLocker interface {
	TryLockOpenAIAutoResetCredit(context.Context, int64) (release func(), acquired bool, err error)
}

type openAIAutoResetQuota interface {
	QueryUsage(ctx context.Context, accountID int64) (*OpenAIQuotaUsage, error)
	CacheResetCreditsSnapshot(ctx context.Context, accountID int64, credits *OpenAIRateLimitResetCredits) error
	CachePostResetSnapshot(ctx context.Context, accountID int64, usage *OpenAIQuotaUsage) error
	ResetCreditTargeted(ctx context.Context, accountID int64, creditID, redeemRequestID string) (*OpenAIQuotaResetResult, error)
}

type openAIAutoResetContextKey struct{}

func withOpenAIAutoResetContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIAutoResetContextKey{}, true)
}

func isOpenAIAutoResetContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(openAIAutoResetContextKey{}).(bool)
	return value
}

type openAIAutoResetRecovery interface {
	RecoverAccountState(ctx context.Context, accountID int64, options AccountRecoveryOptions) (*SuccessfulTestRecoveryResult, error)
}

// OpenAIQuotaAutoResetService 通过小型去重队列承接实时信号，并用分钟扫描补偿
// 重启、漏事件和多实例读取；真正消费仍由 PostgreSQL 幂等记录串行化。
type OpenAIQuotaAutoResetService struct {
	accountRepo          AccountRepository
	carpoolSubscriptions *SubscriptionService
	quota                openAIAutoResetQuota
	recoverer            openAIAutoResetRecovery
	idempotency          *IdempotencyCoordinator
	audit                *AuditLogService
	settings             *SettingService
	leaderLock           LeaderLockCache

	ctx     context.Context
	cancel  context.CancelFunc
	queue   chan int64
	pending sync.Map
	owner   string
	start   sync.Once
	stop    sync.Once
	wg      sync.WaitGroup
	now     func() time.Time
}

const carpoolQuotaSnapshotInterval = 10 * time.Minute

func (s *OpenAIQuotaAutoResetService) SetCarpoolSubscriptionService(subscriptionService *SubscriptionService) {
	if s != nil {
		s.carpoolSubscriptions = subscriptionService
	}
}

func NewOpenAIQuotaAutoResetService(
	accountRepo AccountRepository,
	quota openAIAutoResetQuota,
	recoverer openAIAutoResetRecovery,
	idempotency *IdempotencyCoordinator,
	audit *AuditLogService,
	settings *SettingService,
	leaderLock LeaderLockCache,
) *OpenAIQuotaAutoResetService {
	ctx, cancel := context.WithCancel(context.Background())
	return &OpenAIQuotaAutoResetService{
		accountRepo: accountRepo,
		quota:       quota,
		recoverer:   recoverer,
		idempotency: idempotency,
		audit:       audit,
		settings:    settings,
		leaderLock:  leaderLock,
		ctx:         ctx,
		cancel:      cancel,
		queue:       make(chan int64, openAIAutoResetQueueCapacity),
		owner:       uuid.NewString(),
		now:         time.Now,
	}
}

func (s *OpenAIQuotaAutoResetService) Start() {
	if s == nil || s.accountRepo == nil || s.quota == nil || s.idempotency == nil {
		return
	}
	s.start.Do(func() {
		setOpenAIAutoResetNotifier(s)
		for range openAIAutoResetWorkerCount {
			s.wg.Add(1)
			go s.runWorker()
		}
		s.wg.Add(1)
		go s.runScanner()
	})
}

func (s *OpenAIQuotaAutoResetService) Stop() {
	if s == nil {
		return
	}
	s.stop.Do(func() {
		clearOpenAIAutoResetNotifier(s)
		s.cancel()
		s.wg.Wait()
	})
}

// Notify 是请求热路径的非阻塞入口。同一账号尚在队列时只保留一个任务；队列
// 满时丢弃本次信号，分钟扫描仍会补偿，因此不会反向拖慢网关请求。
func (s *OpenAIQuotaAutoResetService) Notify(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	if _, loaded := s.pending.LoadOrStore(accountID, struct{}{}); loaded {
		return
	}
	select {
	case <-s.ctx.Done():
		s.pending.Delete(accountID)
	case s.queue <- accountID:
	default:
		s.pending.Delete(accountID)
		slog.Warn("openai_auto_reset_queue_full", "account_id", accountID)
	}
}

func (s *OpenAIQuotaAutoResetService) runWorker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case accountID := <-s.queue:
			ctx, cancel := context.WithTimeout(s.ctx, 50*time.Second)
			if err := s.evaluateAccount(ctx, accountID); err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("openai_auto_reset_evaluate_failed", "account_id", accountID, "error_code", infraerrors.Reason(err))
			}
			cancel()
			s.pending.Delete(accountID)
		}
	}
}

func (s *OpenAIQuotaAutoResetService) runScanner() {
	defer s.wg.Done()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-s.ctx.Done():
		return
	case <-timer.C:
		s.scanEnabledAccounts(s.ctx)
	}
	ticker := time.NewTicker(openAIAutoResetScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.scanEnabledAccounts(s.ctx)
		}
	}
}

func (s *OpenAIQuotaAutoResetService) scanEnabledAccounts(ctx context.Context) {
	release, scan := s.tryAcquireScanLock(ctx)
	if !scan {
		return
	}
	if release != nil {
		defer release()
	}
	carpoolAccountIDs := map[int64]struct{}{}
	if s.carpoolSubscriptions != nil {
		var err error
		carpoolAccountIDs, err = s.carpoolSubscriptions.CarpoolAccountIDs(ctx)
		if err != nil {
			slog.Warn("carpool_subscription_account_scan_failed", "error", err)
		}
	}
	for page := 1; ; page++ {
		accounts, pageInfo, err := s.accountRepo.ListWithFilters(ctx, pagination.PaginationParams{
			Page: page, PageSize: openAIAutoResetBatchSize,
		}, PlatformOpenAI, AccountTypeOAuth, StatusActive, "", 0, "")
		if err != nil {
			slog.Warn("openai_auto_reset_scan_failed", "page", page, "error", err)
			return
		}
		for i := range accounts {
			account := &accounts[i]
			_, hasCarpoolSubscriptions := carpoolAccountIDs[account.ID]
			config := ResolveOpenAIAutoResetCreditConfig(account)
			if hasCarpoolSubscriptions || (account.IsActive() && account.Schedulable &&
				(config.Enabled || (config.ExpiryEnabled && openAIAutoResetExpiryNeedsEvaluation(account.Extra, s.currentTime())))) {
				s.Notify(account.ID)
			}
		}
		if len(accounts) < openAIAutoResetBatchSize || pageInfo == nil || page >= pageInfo.Pages {
			return
		}
	}
}

// Redis 锁异常时允许重复扫描，避免协调设施故障导致所有实例同时停止补偿；
// 消费唯一性由数据库幂等记录负责，扫描锁只用于削减重复查询。
func (s *OpenAIQuotaAutoResetService) tryAcquireScanLock(ctx context.Context) (func(), bool) {
	if s.leaderLock == nil {
		return func() {}, true
	}
	ok, err := s.leaderLock.TryAcquireLeaderLock(ctx, openAIAutoResetLeaderLockKey, s.owner, 55*time.Second)
	if err != nil {
		slog.Warn("openai_auto_reset_leader_lock_unavailable", "error", err)
		return func() {}, true
	}
	if !ok {
		return nil, false
	}
	return func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.leaderLock.ReleaseLeaderLock(releaseCtx, openAIAutoResetLeaderLockKey, s.owner)
	}, true
}

type openAIAutoResetAssessment struct {
	triggerWindow string
	resetReached  bool
	pauseReached  bool
	utilization5h float64
	utilization7d float64
	threshold5h   float64
	threshold7d   float64
}

func (s *OpenAIQuotaAutoResetService) evaluateAccount(ctx context.Context, accountID int64) error {
	ctx = withOpenAIAutoResetContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.idempotency == nil || s.idempotency.repo == nil {
		return ErrIdempotencyStoreUnavail
	}
	locker, ok := s.idempotency.repo.(openAIAutoResetLocker)
	if !ok {
		return ErrIdempotencyStoreUnavail
	}
	release, acquired, err := locker.TryLockOpenAIAutoResetCredit(ctx, accountID)
	if err != nil {
		return ErrIdempotencyStoreUnavail.WithCause(err)
	}
	if !acquired {
		return nil
	}
	defer release()

	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil || account == nil {
		return err
	}
	if account.IsShadow() {
		if account.ParentAccountID != nil {
			s.Notify(*account.ParentAccountID)
		}
		return nil
	}
	if account.IsActive() && s.carpoolSubscriptions != nil {
		if err := s.checkCarpoolQuotaSnapshot(ctx, account); err != nil {
			slog.Warn("carpool_subscription_snapshot_check_failed", "account_id", accountID, "error", err)
		}
	}
	config := ResolveOpenAIAutoResetCreditConfig(account)
	if (!config.Enabled && !config.ExpiryEnabled) || !account.IsActive() || !account.Schedulable {
		return nil
	}

	now := s.currentTime()
	assessment := s.assessExtra(account, config, now)
	state := openAIAutoResetStateFromExtra(account.Extra)
	if state != nil && state.AttemptCreditHash != "" && !openAIAutoResetAttemptConsumed(state) {
		record, err := s.idempotency.repo.GetByScopeAndKeyHash(ctx, "openai_auto_reset_credit",
			HashIdempotencyKey(openAIAutoResetStableKey(accountID, state)))
		if err != nil {
			return ErrIdempotencyStoreUnavail.WithCause(err)
		}
		// Only the account-lock owner may resume an interrupted, expired lease.
		if record != nil && record.Status == IdempotencyStatusProcessing && record.LockedUntil != nil && !record.LockedUntil.After(time.Now()) {
			retryAt := time.Now()
			if err := s.idempotency.repo.MarkFailedRetryable(ctx, record.ID, "OPENAI_AUTO_RESET_INTERRUPTED", retryAt, retryAt.Add(openAIAutoResetAttemptTTL)); err != nil {
				return ErrIdempotencyStoreUnavail.WithCause(err)
			}
		}
		if record != nil && record.Status == IdempotencyStatusSucceeded {
			value, err := s.idempotency.decodeStoredResponse(record.ResponseBody)
			if err != nil {
				return ErrIdempotencyStoreUnavail.WithCause(err)
			}
			result := decodeOpenAIAutoResetConsumeResult(value)
			if result.CreditHash != "" && result.CreditHash != state.AttemptCreditHash {
				return s.failState(ctx, accountID, state, "OPENAI_AUTO_RESET_TARGET_UNCONFIRMED", nil)
			}
			if result.CreditHash == state.AttemptCreditHash {
				state.AttemptConsumed = true
				state.Status = OpenAIAutoResetStatusResetting
			}
		}
	}
	if openAIAutoResetPostProcessPending(state) {
		return s.finishConsumedCredit(ctx, accountID, state, assessment, openAIAutoResetConsumeResult{})
	}
	if openAIAutoResetUnresolvedEarliestHeld(state, now) {
		return nil
	}
	// 达到用卡阈值本应立即查询以便用卡；但 10 分钟内已确认无卡时，重查不会改变结论，
	// 只会让调度热路径的通知把同一账号的上游额度接口打到十几秒一次。
	needsQuery := config.Enabled && (openAIAutoResetSnapshotStale(account.Extra, now) ||
		(assessment.resetReached && !openAIAutoResetNoCreditConfirmed(state, now)))
	if config.Enabled && assessment.pauseReached && !assessment.resetReached {
		needsQuery = needsQuery || state == nil || state.Status == OpenAIAutoResetStatusChecking || state.Status == OpenAIAutoResetStatusFailed || openAIAutoResetStateStale(state, now)
	}
	expiryCheck := config.ExpiryEnabled && openAIAutoResetExpiryNeedsEvaluation(account.Extra, now)
	pendingAttempt := state != nil && state.AttemptCreditHash != "" && !openAIAutoResetAttemptConsumed(state)
	unresolvedEarliest := state != nil && state.UnresolvedEarliestUntil != ""
	needsQuery = needsQuery || expiryCheck || pendingAttempt || unresolvedEarliest
	if needsQuery && openAIAutoResetQueryFailureBackoffActive(state, now) {
		needsQuery = false
	}
	if !needsQuery {
		if config.Enabled && !assessment.pauseReached && state != nil && state.TriggerWindow != "" && state.AttemptCreditHash == "" {
			state.TriggerWindow = ""
			state.ErrorCode = ""
			state.CheckedAt = now.UTC().Format(time.RFC3339)
			if state.AvailableCount > 0 {
				state.Status = OpenAIAutoResetStatusAvailable
			} else {
				state.Status = OpenAIAutoResetStatusNoCredit
			}
			return s.persistState(ctx, accountID, state)
		}
		return nil
	}

	checking := &OpenAIAutoResetCreditState{
		Status:         OpenAIAutoResetStatusChecking,
		TriggerWindow:  assessment.triggerWindow,
		AvailableCount: stateAvailableCount(state),
		CheckedAt:      now.UTC().Format(time.RFC3339),
	}
	copyOpenAIAutoResetAttempt(checking, state)
	if expiryCheck && !assessment.resetReached {
		checking.TriggerWindow = "expiry"
	}
	if !pendingAttempt && !unresolvedEarliest {
		checking.UnresolvedEarliestUntil = "unknown"
		if !openAIAutoResetAttemptConsumed(state) {
			if expiresAt, err := openAIAutoResetCachedExpiry(account.Extra, now); err == nil && !expiresAt.IsZero() {
				checking.UnresolvedEarliestUntil = expiresAt.UTC().Format(time.RFC3339Nano)
			}
		}
	}
	if err := s.persistState(ctx, accountID, checking); err != nil {
		return err
	}

	usage, err := s.quota.QueryUsage(ctx, accountID)
	if err != nil || usage == nil {
		if !unresolvedEarliest {
			checking.UnresolvedEarliestUntil = ""
		}
		return s.failState(ctx, accountID, checking, "RESET_CREDIT_QUERY_FAILED", err)
	}
	if usage.RateLimitResetCredits == nil || !usage.autoResetDetailsComplete {
		return s.failState(ctx, accountID, checking, "RESET_CREDIT_DETAILS_UNAVAILABLE", nil)
	}
	if err := validateOpenAIAutoResetCandidates(usage.autoResetCandidates, usage.RateLimitResetCredits.AvailableCount); err != nil {
		return s.failState(ctx, accountID, checking, infraerrors.Reason(err), err)
	}
	available := usage.RateLimitResetCredits.AvailableCount
	earliest, expiryErr := openAIAutoResetSnapshotExpiry(usage.RateLimitResetCredits, now)
	if expiryErr != nil {
		return s.failState(ctx, accountID, checking, "OPENAI_AUTO_RESET_CREDIT_EXPIRY_INVALID", expiryErr)
	}
	if err := s.persistFreshUsage(ctx, accountID, usage, now); err != nil {
		if !unresolvedEarliest {
			checking.UnresolvedEarliestUntil = ""
		}
		return s.failState(ctx, accountID, checking, "USAGE_SNAPSHOT_WRITE_FAILED", err)
	}
	checking.ExpiryInitialized = checking.ExpiryInitialized || config.ExpiryEnabled

	// 查询期间管理员可能关闭开关；消费前重新读取账号，确保尚未发出的任务可取消。
	account, err = s.accountRepo.GetByID(ctx, accountID)
	if err != nil || account == nil {
		return err
	}
	config = ResolveOpenAIAutoResetCreditConfig(account)
	if (!config.Enabled && !config.ExpiryEnabled) || !account.IsActive() || !account.Schedulable {
		return nil
	}
	now = s.currentTime()
	if pendingAttempt {
		for _, candidate := range usage.autoResetCandidates {
			if shortOpenAIAutoResetHash(candidate.ID) == state.AttemptCreditHash {
				state.AttemptExpiresAt = candidate.ExpiresAt
				checking.AttemptExpiresAt = candidate.ExpiresAt
				break
			}
		}
		if openAIAutoResetAttemptExpired(state, now) {
			clearOpenAIAutoResetAttempt(state)
			clearOpenAIAutoResetAttempt(checking)
			pendingAttempt = false
		}
	}
	assessment = s.assessUsage(usage, account, config, now)
	if config.ExpiryEnabled && openAIAutoResetIsExpiring(earliest, now) {
		assessment.resetReached = true
		assessment.triggerWindow = "expiry"
	}
	if pendingAttempt {
		assessment.resetReached = assessment.resetReached ||
			(state.TriggerWindow != "expiry" && config.Enabled)
		if assessment.triggerWindow == "" {
			assessment.triggerWindow = state.TriggerWindow
		}
	}
	checking.AvailableCount = available
	checking.TriggerWindow = assessment.triggerWindow
	checking.Status = OpenAIAutoResetStatusAvailable
	checking.ErrorCode = ""
	if available <= 0 || earliest.IsZero() {
		checking.Status = OpenAIAutoResetStatusNoCredit
	}
	if !assessment.resetReached {
		if pendingAttempt {
			if _, err := selectOpenAIAutoResetCandidate(usage.autoResetCandidates, available, state, now); err != nil {
				return s.failState(ctx, accountID, checking, infraerrors.Reason(err), err)
			}
		}
		checking.UnresolvedEarliestUntil = ""
		return s.persistState(ctx, accountID, checking)
	}
	candidate, selectErr := selectOpenAIAutoResetCandidate(usage.autoResetCandidates, available, state, now)
	if selectErr != nil {
		if infraerrors.Reason(selectErr) == "OPENAI_AUTO_RESET_NO_CREDIT" && !pendingAttempt {
			checking.ErrorCode = "NO_RESET_CREDIT"
			checking.UnresolvedEarliestUntil = ""
			return s.persistState(ctx, accountID, checking)
		}
		if !pendingAttempt && !earliest.IsZero() {
			checking.UnresolvedEarliestUntil = earliest.UTC().Format(time.RFC3339Nano)
		}
		return s.failState(ctx, accountID, checking, infraerrors.Reason(selectErr), selectErr)
	}
	creditHash := shortOpenAIAutoResetHash(candidate.ID)
	resetting := checking
	if resetting.AttemptCreditHash != creditHash {
		clearOpenAIAutoResetAttempt(resetting)
	}
	resetting.Status = OpenAIAutoResetStatusResetting
	resetting.UnresolvedEarliestUntil = ""
	resetting.AttemptCreditHash = creditHash
	resetting.AttemptExpiresAt = candidate.ExpiresAt
	if resetting.AttemptConsumed {
		return s.finishConsumedCredit(ctx, accountID, resetting, assessment, openAIAutoResetConsumeResult{})
	}
	stableKey := openAIAutoResetStableKey(accountID, resetting)
	redeemRequestID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(stableKey)).String()
	if err := s.persistState(ctx, accountID, resetting); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	result, err := s.idempotency.Execute(ctx, IdempotencyExecuteOptions{
		Scope:          "openai_auto_reset_credit",
		ActorScope:     fmt.Sprintf("account:%d", accountID),
		Method:         http.MethodPost,
		Route:          "/system/openai/reset-credit/auto",
		IdempotencyKey: stableKey,
		Payload: map[string]any{
			"account_id":  accountID,
			"credit_hash": creditHash,
			"cycle_hash":  resetting.AttemptCycleHash,
		},
		TTL:              openAIAutoResetAttemptTTL,
		RequireKey:       true,
		ExecutionTimeout: 30 * time.Second,
	}, func(execCtx context.Context) (any, error) {
		account, err := s.accountRepo.GetByID(execCtx, accountID)
		if err != nil {
			return nil, err
		}
		config := ResolveOpenAIAutoResetCreditConfig(account)
		if account == nil || !account.IsActive() || !account.Schedulable || execCtx.Err() != nil {
			return nil, infraerrors.Conflict("OPENAI_AUTO_RESET_CANCELLED", "automatic reset was disabled before execution")
		}
		currentTime := s.currentTime()
		expiresAt, err := time.Parse(time.RFC3339Nano, candidate.ExpiresAt)
		if err != nil || !expiresAt.After(currentTime) {
			return nil, infraerrors.Conflict("OPENAI_AUTO_RESET_CREDIT_EXPIRED", "the target reset credit has expired")
		}
		enabled := s.assessUsage(usage, account, config, currentTime).resetReached ||
			(config.ExpiryEnabled && openAIAutoResetIsExpiring(expiresAt, currentTime)) ||
			(pendingAttempt && state.TriggerWindow != "expiry" && config.Enabled)
		if !enabled {
			return nil, infraerrors.Conflict("OPENAI_AUTO_RESET_CANCELLED", "automatic reset was disabled before execution")
		}
		resetResult, resetErr := s.quota.ResetCreditTargeted(execCtx, accountID, candidate.ID, redeemRequestID)
		if resetErr != nil {
			return nil, resetErr
		}
		if resetResult == nil || resetResult.Credit == nil || resetResult.Credit.ID != candidate.ID ||
			(!strings.EqualFold(resetResult.Code, "ok") && !strings.EqualFold(resetResult.Code, "success")) {
			return nil, infraerrors.Conflict("OPENAI_AUTO_RESET_TARGET_UNCONFIRMED", "the consumed reset credit could not be confirmed")
		}
		resetting.AttemptConsumed = true
		checkpointCtx, cancelCheckpoint := context.WithTimeout(context.WithoutCancel(execCtx), 5*time.Second)
		checkpointErr := s.persistState(checkpointCtx, accountID, resetting)
		cancelCheckpoint()
		if checkpointErr != nil {
			// Keep the successful callback result so the idempotency record remains a second durable checkpoint.
			slog.Warn("openai_auto_reset_consumed_checkpoint_failed", "account_id", accountID, "error", checkpointErr)
		}
		// 幂等表只保存脱敏结果，避免上游返回的卡 ID 被持久化到响应体列。
		return openAIAutoResetConsumeResult{Code: resetResult.Code, WindowsReset: resetResult.WindowsReset, CreditHash: creditHash}, nil
	})
	if err != nil {
		// 另一个实例已持有同一周期的兑换时保持 resetting，等待下一轮读取同一
		// 幂等结果；不能把并发冲突误报成上游消费失败，更不能改选下一张卡。
		reason := infraerrors.Reason(err)
		if reason == infraerrors.Reason(ErrIdempotencyInProgress) || reason == infraerrors.Reason(ErrIdempotencyRetryBackoff) {
			return nil
		}
		s.recordAudit(accountID, assessment, available, "failed", 0, infraerrors.Reason(err))
		return s.failState(ctx, accountID, resetting, infraerrors.Reason(err), err)
	}

	consumeResult := decodeOpenAIAutoResetConsumeResult(result.Data)
	if consumeResult.CreditHash != creditHash {
		return s.failState(ctx, accountID, resetting, "OPENAI_AUTO_RESET_TARGET_UNCONFIRMED", nil)
	}
	resetting.AttemptConsumed = true
	if err := s.persistState(ctx, accountID, resetting); err != nil {
		return err
	}
	return s.finishConsumedCredit(ctx, accountID, resetting, assessment, consumeResult)
}

func (s *OpenAIQuotaAutoResetService) finishConsumedCredit(ctx context.Context, accountID int64, state *OpenAIAutoResetCreditState, assessment openAIAutoResetAssessment, consumeResult openAIAutoResetConsumeResult) error {
	available := state.AvailableCount
	stableKey := openAIAutoResetStableKey(accountID, state)
	if s.carpoolSubscriptions != nil {
		carpoolCtx, cancelCarpool := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
		carpoolResult, resetErr := s.carpoolSubscriptions.ResetCarpoolSubscriptionQuotas(carpoolCtx, accountID, CarpoolResetSourceAutoCard, stableKey)
		cancelCarpool()
		if resetErr != nil {
			slog.Error("carpool_subscription_quota_reset_after_auto_card_failed", "account_id", accountID, "error", resetErr)
			return s.failState(ctx, accountID, state, "CARPOOL_QUOTA_RESET_FAILED", resetErr)
		} else if carpoolResult != nil && carpoolResult.AffectedCount > 0 {
			slog.Info("carpool_subscription_quota_reset_after_auto_card", "account_id", accountID, "affected_count", carpoolResult.AffectedCount)
		}
	}
	postCtx, cancelPost := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	post := RunOpenAIQuotaResetPostProcess(postCtx, accountID, s.quota, s.recoverer, s.accountRepo.GetByID)
	cancelPost()
	if !post.AccountStateRecovered || post.WarningCode != "" {
		code := post.WarningCode
		if code == "" {
			code = OpenAIQuotaResetWarningAccountRecoveryFailed
		}
		s.recordAudit(accountID, assessment, available, "recovery_failed", consumeResult.WindowsReset, code)
		return s.failState(ctx, accountID, state, code, nil)
	}

	successAt := s.currentTime().UTC().Format(time.RFC3339Nano)
	success := *state
	success.Status = OpenAIAutoResetStatusSuccess
	success.AvailableCount = max(0, available-1)
	success.CheckedAt = successAt
	success.LastResultAt = successAt
	success.ErrorCode = ""
	if post.Quota != nil && post.Quota.RateLimitResetCredits != nil {
		success.AvailableCount = post.Quota.RateLimitResetCredits.AvailableCount
		success.ExpiryInitialized = success.ExpiryInitialized || ResolveOpenAIAutoResetCreditConfig(post.Account).ExpiryEnabled
	}
	if err := s.persistState(ctx, accountID, &success); err != nil {
		return err
	}
	s.recordAudit(accountID, assessment, available, "success", consumeResult.WindowsReset, "")
	slog.Info("openai_auto_reset_credit_success",
		"account_id", accountID,
		"trigger_window", assessment.triggerWindow,
		"threshold_5h", assessment.threshold5h,
		"threshold_7d", assessment.threshold7d,
		"utilization_5h", assessment.utilization5h,
		"utilization_7d", assessment.utilization7d,
		"windows_reset", consumeResult.WindowsReset,
	)
	return nil
}

func (s *OpenAIQuotaAutoResetService) checkCarpoolQuotaSnapshot(ctx context.Context, account *Account) error {
	if s == nil || account == nil || s.carpoolSubscriptions == nil {
		return nil
	}
	count, err := s.carpoolSubscriptions.PreviewCarpoolSubscriptionQuotaReset(ctx, account.ID)
	if err != nil || count == 0 {
		return err
	}
	if !carpoolQuotaSnapshotStale(account.Extra, time.Now()) {
		return nil
	}
	observedAt := time.Now()
	usage, err := s.quota.QueryUsage(ctx, account.ID)
	if err != nil {
		return err
	}
	if usage == nil {
		return errors.New("openai quota query returned an empty result")
	}
	if _, err := s.carpoolSubscriptions.ObserveCarpoolQuotaUsage(ctx, account.ID, usage, observedAt); err != nil {
		return err
	}
	return s.accountRepo.UpdateExtra(ctx, account.ID, buildOpenAIAutoResetUsageUpdates(usage, observedAt))
}

func carpoolQuotaSnapshotStale(extra map[string]any, now time.Time) bool {
	state := carpoolQuotaResetStateFromExtra(extra)
	if state == nil {
		return true
	}
	checkedAt, ok := parseCarpoolResetTime(state.CheckedAt)
	if !ok {
		return true
	}
	interval := carpoolQuotaSnapshotInterval
	if _, ok := parseCarpool7DResetCandidate(state.Baseline[carpoolForcedResetCandidateExtraKey]); ok {
		interval = carpoolForcedResetConfirmDelay
	}
	return now.Sub(checkedAt) >= interval
}

type openAIAutoResetConsumeResult struct {
	Code         string `json:"code"`
	WindowsReset int    `json:"windows_reset"`
	CreditHash   string `json:"credit_hash,omitempty"`
}

func decodeOpenAIAutoResetConsumeResult(value any) openAIAutoResetConsumeResult {
	if typed, ok := value.(openAIAutoResetConsumeResult); ok {
		return typed
	}
	raw, _ := json.Marshal(value)
	var decoded openAIAutoResetConsumeResult
	_ = json.Unmarshal(raw, &decoded)
	return decoded
}

func (s *OpenAIQuotaAutoResetService) assessExtra(account *Account, config OpenAIAutoResetCreditConfig, now time.Time) openAIAutoResetAssessment {
	utilization5h, _ := resolveOpenAIQuotaUtilization(account.Extra, "5h", now)
	utilization7d, _ := resolveOpenAIQuotaUtilization(account.Extra, "7d", now)
	return s.buildAssessment(account, config, utilization5h, utilization7d)
}

func (s *OpenAIQuotaAutoResetService) assessUsage(usage *OpenAIQuotaUsage, account *Account, config OpenAIAutoResetCreditConfig, now time.Time) openAIAutoResetAssessment {
	updates := buildOpenAIAutoResetUsageUpdates(usage, now)
	utilization5h := readOpenAIQuotaUsedPercent(updates, "5h") / 100
	utilization7d := readOpenAIQuotaUsedPercent(updates, "7d") / 100
	return s.buildAssessment(account, config, utilization5h, utilization7d)
}

func (s *OpenAIQuotaAutoResetService) buildAssessment(account *Account, config OpenAIAutoResetCreditConfig, utilization5h, utilization7d float64) openAIAutoResetAssessment {
	assessment := openAIAutoResetAssessment{
		utilization5h: utilization5h,
		utilization7d: utilization7d,
		threshold5h:   config.Threshold5h,
		threshold7d:   config.Threshold7d,
	}
	if !config.Enabled {
		return assessment
	}
	reset5h := utilization5h >= config.Threshold5h
	reset7d := utilization7d >= config.Threshold7d
	assessment.resetReached = reset5h || reset7d
	assessment.triggerWindow = joinOpenAIAutoResetWindows(reset5h, reset7d)

	pause5h, pause7d := resolveOpenAIQuotaAutoPauseThresholds(context.Background(), account)
	if s.settings != nil {
		pause5h, pause7d = resolveOpenAIQuotaAutoPauseThresholds(
			withOpenAIQuotaAutoPauseSettings(context.Background(), s.settings.GetOpenAIQuotaAutoPauseSettings(context.Background())),
			account,
		)
	}
	pauseReached5h := !resolveAccountExtraBool(account.Extra, "auto_pause_5h_disabled") && pause5h > 0 && utilization5h >= pause5h
	pauseReached7d := !resolveAccountExtraBool(account.Extra, "auto_pause_7d_disabled") && pause7d > 0 && utilization7d >= pause7d
	assessment.pauseReached = pauseReached5h || pauseReached7d || assessment.resetReached
	if assessment.triggerWindow == "" {
		assessment.triggerWindow = joinOpenAIAutoResetWindows(pauseReached5h, pauseReached7d)
	}
	return assessment
}

func joinOpenAIAutoResetWindows(fiveHour, sevenDay bool) string {
	switch {
	case fiveHour && sevenDay:
		return "5h+7d"
	case fiveHour:
		return "5h"
	case sevenDay:
		return "7d"
	default:
		return ""
	}
}

func buildOpenAIAutoResetUsageUpdates(usage *OpenAIQuotaUsage, now time.Time) map[string]any {
	if usage == nil || usage.RateLimit == nil {
		return nil
	}
	rateLimit := usage.RateLimit
	snapshot := &OpenAICodexUsageSnapshot{UpdatedAt: now.UTC().Format(time.RFC3339)}
	applyWindow := func(window *OpenAIRateLimitWindow, primary bool) {
		if window == nil {
			return
		}
		used := window.UsedPercent
		resetAfter := int(window.ResetAfterSeconds)
		windowMinutes := int(window.LimitWindowSeconds / 60)
		if primary {
			snapshot.PrimaryUsedPercent = &used
			snapshot.PrimaryResetAfterSeconds = &resetAfter
			snapshot.PrimaryWindowMinutes = &windowMinutes
		} else {
			snapshot.SecondaryUsedPercent = &used
			snapshot.SecondaryResetAfterSeconds = &resetAfter
			snapshot.SecondaryWindowMinutes = &windowMinutes
		}
	}
	applyWindow(rateLimit.PrimaryWindow, true)
	applyWindow(rateLimit.SecondaryWindow, false)
	return buildCodexUsageExtraUpdates(snapshot, now)
}

func (s *OpenAIQuotaAutoResetService) persistFreshUsage(ctx context.Context, accountID int64, usage *OpenAIQuotaUsage, now time.Time) error {
	updates := buildOpenAIAutoResetUsageUpdates(usage, now)
	if len(updates) > 0 {
		if err := s.accountRepo.UpdateExtra(ctx, accountID, updates); err != nil {
			return err
		}
	}
	return s.quota.CacheResetCreditsSnapshot(ctx, accountID, usage.RateLimitResetCredits)
}

func validateOpenAIAutoResetCandidates(candidates []openAIAutoResetCreditCandidate, available int) error {
	if available < 0 || len(candidates) != available {
		return infraerrors.Conflict("OPENAI_AUTO_RESET_CREDIT_DETAILS_INCOMPLETE", "reset credit details are incomplete")
	}
	ids := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		if _, err := time.Parse(time.RFC3339Nano, candidate.ExpiresAt); err != nil {
			return infraerrors.Conflict("OPENAI_AUTO_RESET_CREDIT_EXPIRY_INVALID", "reset credit expiration is invalid")
		}
		if candidate.ID != "" && ids[candidate.ID] {
			return infraerrors.Conflict("OPENAI_AUTO_RESET_CREDIT_DETAILS_INCOMPLETE", "reset credit ids are duplicated")
		}
		ids[candidate.ID] = true
	}
	return nil
}

func selectOpenAIAutoResetCandidate(candidates []openAIAutoResetCreditCandidate, available int, previous *OpenAIAutoResetCreditState, now time.Time) (openAIAutoResetCreditCandidate, error) {
	if err := validateOpenAIAutoResetCandidates(candidates, available); err != nil {
		return openAIAutoResetCreditCandidate{}, err
	}
	sorted := append([]openAIAutoResetCreditCandidate(nil), candidates...)
	sort.Slice(sorted, func(i, j int) bool {
		left, _ := time.Parse(time.RFC3339Nano, sorted[i].ExpiresAt)
		right, _ := time.Parse(time.RFC3339Nano, sorted[j].ExpiresAt)
		if left.Equal(right) {
			return sorted[i].ID < sorted[j].ID
		}
		return left.Before(right)
	})
	for len(sorted) > 0 {
		expiresAt, _ := time.Parse(time.RFC3339Nano, sorted[0].ExpiresAt)
		if expiresAt.After(now) {
			break
		}
		sorted = sorted[1:]
	}
	pending := previous != nil && previous.AttemptCreditHash != "" &&
		!openAIAutoResetAttemptConsumed(previous) && !openAIAutoResetAttemptExpired(previous, now)
	if pending && (len(sorted) == 0 || shortOpenAIAutoResetHash(sorted[0].ID) != previous.AttemptCreditHash) {
		return openAIAutoResetCreditCandidate{}, infraerrors.Conflict("OPENAI_AUTO_RESET_ORIGINAL_CREDIT_UNAVAILABLE", "the original reset credit cannot be confirmed; refusing to switch credits")
	}
	if len(sorted) == 0 {
		return openAIAutoResetCreditCandidate{}, infraerrors.Conflict("OPENAI_AUTO_RESET_NO_CREDIT", "no reset credit is available")
	}
	if strings.TrimSpace(sorted[0].ID) == "" {
		return openAIAutoResetCreditCandidate{}, infraerrors.Conflict("OPENAI_AUTO_RESET_CREDIT_ID_MISSING", "the earliest reset credit has no official id")
	}
	return sorted[0], nil
}

func openAIAutoResetStableKey(accountID int64, state *OpenAIAutoResetCreditState) string {
	key := fmt.Sprintf("oarc:%d:%s", accountID, state.AttemptCreditHash)
	// Already-started attempts retain the old identity across usage-window changes.
	if state.AttemptCycleHash != "" {
		key += ":" + state.AttemptCycleHash
	}
	return key
}

func (s *OpenAIQuotaAutoResetService) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func openAIAutoResetAttemptConsumed(state *OpenAIAutoResetCreditState) bool {
	return state != nil && state.AttemptCreditHash != "" && (state.AttemptConsumed || state.Status == OpenAIAutoResetStatusSuccess)
}

func openAIAutoResetPostProcessPending(state *OpenAIAutoResetCreditState) bool {
	return openAIAutoResetAttemptConsumed(state) &&
		(state.Status == OpenAIAutoResetStatusResetting || state.Status == OpenAIAutoResetStatusFailed)
}

func openAIAutoResetUnresolvedEarliestHeld(state *OpenAIAutoResetCreditState, now time.Time) bool {
	if state == nil || state.UnresolvedEarliestUntil == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, state.UnresolvedEarliestUntil)
	return err != nil || expiresAt.After(now)
}

func openAIAutoResetAttemptExpired(state *OpenAIAutoResetCreditState, now time.Time) bool {
	if state == nil {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, state.AttemptExpiresAt)
	return err == nil && !expiresAt.After(now)
}

func clearOpenAIAutoResetAttempt(state *OpenAIAutoResetCreditState) {
	state.AttemptCreditHash = ""
	state.AttemptCycleHash = ""
	state.AttemptExpiresAt = ""
	state.AttemptConsumed = false
}

func openAIAutoResetSnapshotExpiry(credits *OpenAIRateLimitResetCredits, now time.Time) (time.Time, error) {
	if credits == nil || credits.AvailableCount < 0 || len(credits.Credits) != credits.AvailableCount {
		return time.Time{}, errors.New("reset credit snapshot is incomplete")
	}
	var earliest time.Time
	for _, credit := range credits.Credits {
		expiresAt, err := time.Parse(time.RFC3339Nano, credit.ExpiresAt)
		if err != nil {
			return time.Time{}, err
		}
		if expiresAt.After(now) && (earliest.IsZero() || expiresAt.Before(earliest)) {
			earliest = expiresAt
		}
	}
	return earliest, nil
}

func openAIAutoResetIsExpiring(expiresAt, now time.Time) bool {
	remaining := expiresAt.Sub(now)
	return remaining > 0 && remaining <= openAIAutoResetExpiryWindow
}

func openAIAutoResetExpiryNeedsEvaluation(extra map[string]any, now time.Time) bool {
	state := openAIAutoResetStateFromExtra(extra)
	if openAIAutoResetPostProcessPending(state) {
		return true
	}
	if openAIAutoResetUnresolvedEarliestHeld(state, now) {
		return false
	}
	if openAIAutoResetQueryFailureBackoffActive(state, now) {
		return false
	}
	if state == nil || !state.ExpiryInitialized || state.UnresolvedEarliestUntil != "" ||
		(state.AttemptCreditHash != "" && !openAIAutoResetAttemptConsumed(state)) {
		return true
	}
	expiresAt, err := openAIAutoResetCachedExpiry(extra, now)
	return err == nil && openAIAutoResetIsExpiring(expiresAt, now)
}

func openAIAutoResetCachedExpiry(extra map[string]any, now time.Time) (time.Time, error) {
	raw, err := json.Marshal(extra[openaiQuotaResetCreditsKey])
	if err != nil {
		return time.Time{}, err
	}
	var credits *OpenAIRateLimitResetCredits
	if err := json.Unmarshal(raw, &credits); err != nil {
		return time.Time{}, err
	}
	return openAIAutoResetSnapshotExpiry(credits, now)
}

func shortOpenAIAutoResetHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

func openAIAutoResetSnapshotStale(extra map[string]any, now time.Time) bool {
	if len(extra) == 0 {
		return true
	}
	raw, ok := extra["codex_usage_updated_at"]
	if !ok {
		return true
	}
	updatedAt, err := parseTime(fmt.Sprint(raw))
	return err != nil || now.Sub(updatedAt) >= openAIAutoResetSnapshotTTL
}

func openAIAutoResetStateFromExtra(extra map[string]any) *OpenAIAutoResetCreditState {
	if len(extra) == 0 {
		return nil
	}
	raw, ok := extra[OpenAIAutoResetCreditStateExtraKey]
	if !ok || raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var state OpenAIAutoResetCreditState
	if err := json.Unmarshal(encoded, &state); err != nil || state.Status == "" {
		return nil
	}
	return &state
}

// openAIAutoResetNoCreditConfirmed 报告账号在状态有效期内已被确认无卡。
func openAIAutoResetNoCreditConfirmed(state *OpenAIAutoResetCreditState, now time.Time) bool {
	return state != nil && state.Status == OpenAIAutoResetStatusNoCredit && !openAIAutoResetStateStale(state, now)
}

// openAIAutoResetQueryFailureBackoffActive 报告查询阶段刚失败、仍在重试间隔内。
// 只覆盖查询、快照写入、卡明细缺失三类失败；用卡阶段的失败需要立即按原幂等键重试。
func openAIAutoResetQueryFailureBackoffActive(state *OpenAIAutoResetCreditState, now time.Time) bool {
	if state == nil || state.Status != OpenAIAutoResetStatusFailed {
		return false
	}
	switch state.ErrorCode {
	case "RESET_CREDIT_QUERY_FAILED", "USAGE_SNAPSHOT_WRITE_FAILED", "RESET_CREDIT_DETAILS_UNAVAILABLE":
	default:
		return false
	}
	failedAt, err := time.Parse(time.RFC3339, state.LastResultAt)
	if err != nil {
		return false
	}
	elapsed := now.Sub(failedAt)
	return elapsed >= 0 && elapsed < openAIAutoResetQueryFailureRetryAfter
}

func openAIAutoResetStateStale(state *OpenAIAutoResetCreditState, now time.Time) bool {
	if state == nil || state.CheckedAt == "" {
		return true
	}
	checkedAt, err := time.Parse(time.RFC3339, state.CheckedAt)
	return err != nil || now.Sub(checkedAt) >= openAIAutoResetSnapshotTTL
}

func stateAvailableCount(state *OpenAIAutoResetCreditState) int {
	if state == nil {
		return 0
	}
	return state.AvailableCount
}

func copyOpenAIAutoResetAttempt(target, source *OpenAIAutoResetCreditState) {
	if target == nil || source == nil {
		return
	}
	target.AttemptCycleHash = source.AttemptCycleHash
	target.AttemptCreditHash = source.AttemptCreditHash
	target.AttemptExpiresAt = source.AttemptExpiresAt
	target.AttemptConsumed = openAIAutoResetAttemptConsumed(source)
	target.ExpiryInitialized = source.ExpiryInitialized
	target.UnresolvedEarliestUntil = source.UnresolvedEarliestUntil
}

func (s *OpenAIQuotaAutoResetService) persistState(ctx context.Context, accountID int64, state *OpenAIAutoResetCreditState) error {
	if state == nil {
		return nil
	}
	return s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{OpenAIAutoResetCreditStateExtraKey: state})
}

func (s *OpenAIQuotaAutoResetService) failState(ctx context.Context, accountID int64, state *OpenAIAutoResetCreditState, code string, cause error) error {
	if state == nil {
		state = &OpenAIAutoResetCreditState{}
	}
	if strings.TrimSpace(code) == "" {
		code = "OPENAI_AUTO_RESET_FAILED"
	}
	state.Status = OpenAIAutoResetStatusFailed
	state.ErrorCode = code
	state.LastResultAt = s.currentTime().UTC().Format(time.RFC3339Nano)
	if err := s.persistState(ctx, accountID, state); err != nil {
		return err
	}
	slog.Warn("openai_auto_reset_credit_failed",
		"account_id", accountID,
		"trigger_window", state.TriggerWindow,
		"available_count", state.AvailableCount,
		"error_code", code,
	)
	if cause != nil {
		return cause
	}
	return infraerrors.Conflict(code, "automatic reset credit operation failed")
}

func (s *OpenAIQuotaAutoResetService) recordAudit(accountID int64, assessment openAIAutoResetAssessment, available int, resultCode string, windowsReset int, errorCode string) {
	if s.audit == nil {
		return
	}
	statusCode := http.StatusOK
	if resultCode != "success" {
		statusCode = http.StatusConflict
	}
	s.audit.Record(&AuditLog{
		ActorEmail: "system",
		ActorRole:  "system",
		AuthMethod: "system",
		Action:     "system.openai.reset_credit.auto",
		Method:     "SYSTEM",
		Path:       fmt.Sprintf("/system/openai/accounts/%d/auto-reset-credit", accountID),
		StatusCode: statusCode,
		Extra: map[string]any{
			"account_id":      accountID,
			"trigger_window":  assessment.triggerWindow,
			"threshold_5h":    assessment.threshold5h,
			"threshold_7d":    assessment.threshold7d,
			"utilization_5h":  assessment.utilization5h,
			"utilization_7d":  assessment.utilization7d,
			"available_count": available,
			"result_code":     resultCode,
			"windows_reset":   windowsReset,
			"error_code":      errorCode,
		},
	})
}

var openAIAutoResetNotifierRegistry struct {
	sync.RWMutex
	service *OpenAIQuotaAutoResetService
}

func setOpenAIAutoResetNotifier(service *OpenAIQuotaAutoResetService) {
	openAIAutoResetNotifierRegistry.Lock()
	openAIAutoResetNotifierRegistry.service = service
	openAIAutoResetNotifierRegistry.Unlock()
}

func clearOpenAIAutoResetNotifier(service *OpenAIQuotaAutoResetService) {
	openAIAutoResetNotifierRegistry.Lock()
	if openAIAutoResetNotifierRegistry.service == service {
		openAIAutoResetNotifierRegistry.service = nil
	}
	openAIAutoResetNotifierRegistry.Unlock()
}

func notifyOpenAIAutoReset(accountID int64) {
	openAIAutoResetNotifierRegistry.RLock()
	service := openAIAutoResetNotifierRegistry.service
	openAIAutoResetNotifierRegistry.RUnlock()
	if service != nil {
		service.Notify(accountID)
	}
}

// NotifyOpenAIAutoResetCredit 供额度查询入口发送轻量信号；不执行同步上游请求。
func NotifyOpenAIAutoResetCredit(accountID int64) {
	notifyOpenAIAutoReset(accountID)
}

// openAIAutoResetSchedulerNotifiedAt 记录调度热路径最近一次为某账号发出通知的时间。
var openAIAutoResetSchedulerNotifiedAt sync.Map // accountID(int64) -> time.Time

// notifyOpenAIAutoResetFromScheduler 供调度候选过滤使用。候选过滤按请求逐账号执行，
// 每条通知都会让后台读一次账号，不做冷却时无卡或待用卡的账号会持续占满后台协程。
func notifyOpenAIAutoResetFromScheduler(accountID int64) {
	notifyOpenAIAutoResetFromSchedulerAt(accountID, time.Now())
}

func notifyOpenAIAutoResetFromSchedulerAt(accountID int64, now time.Time) bool {
	if accountID <= 0 {
		return false
	}
	if last, ok := openAIAutoResetSchedulerNotifiedAt.Load(accountID); ok {
		if lastAt, ok := last.(time.Time); ok {
			elapsed := now.Sub(lastAt)
			if elapsed >= 0 && elapsed < openAIAutoResetSchedulerNotifyCooldown {
				return false
			}
		}
	}
	openAIAutoResetSchedulerNotifiedAt.Store(accountID, now)
	notifyOpenAIAutoReset(accountID)
	return true
}
