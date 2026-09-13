package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	temporaryDispatchScanInterval = time.Minute
	temporaryDispatchStaleAfter   = time.Minute
	temporaryDispatchPollBatch    = 20
	temporaryDispatchPollWorkers  = 4
)

type TemporaryDispatchQuotaPlan struct {
	Window          string
	BaselinePercent float64
	TargetPercent   float64
	ResetAt         time.Time
}

type TemporaryDispatchQuotaPreview struct {
	Window      string    `json:"window"`
	UsedPercent float64   `json:"used_percent"`
	ResetAt     time.Time `json:"reset_at"`
}

type temporaryDispatchQuotaFetcher interface {
	QueryRateLimitUsage(ctx context.Context, accountID int64) (*OpenAIQuotaUsage, error)
}

type temporaryDispatchStore interface {
	CreateTemporaryDispatch(ctx context.Context, spec TemporaryDispatchCreateSpec) error
	StopTemporaryDispatchGroups(ctx context.Context, groupIDs []int64) ([]int64, error)
	ObserveTemporaryDispatchQuota(ctx context.Context, accountID int64, window string, usedPercent float64, observedAt time.Time) ([]int64, error)
	CleanupTemporaryDispatches(ctx context.Context, now time.Time, limit int) ([]int64, error)
	ClaimStaleTemporaryDispatchAccounts(ctx context.Context, staleBefore, claimedAt time.Time, limit int) ([]int64, error)
	StopTemporaryDispatchAccount(ctx context.Context, accountID int64) ([]int64, error)
}

type temporaryDispatchQuotaObservation struct {
	accountID int64
	snapshot  *OpenAICodexUsageSnapshot
}

// TemporaryDispatchRuntime owns active quota tasks and the minute-level cleanup
// safety net. Gateway notifications are non-blocking; the scanner repairs any
// dropped signal or interrupted cleanup.
type TemporaryDispatchRuntime struct {
	store       temporaryDispatchStore
	quota       temporaryDispatchQuotaFetcher
	invalidator APIKeyAuthCacheInvalidator

	ctx     context.Context
	cancel  context.CancelFunc
	start   sync.Once
	stop    sync.Once
	wg      sync.WaitGroup
	queue   chan int64
	latest  sync.Map // account id -> *OpenAICodexUsageSnapshot
	pending sync.Map

	unavailableQueue   chan int64
	unavailableReason  sync.Map // account id -> string
	unavailablePending sync.Map
}

func NewTemporaryDispatchRuntime(store temporaryDispatchStore, quota temporaryDispatchQuotaFetcher, invalidator APIKeyAuthCacheInvalidator) *TemporaryDispatchRuntime {
	ctx, cancel := context.WithCancel(context.Background())
	return &TemporaryDispatchRuntime{
		store: store, quota: quota, invalidator: invalidator,
		ctx: ctx, cancel: cancel,
		queue: make(chan int64, 1024), unavailableQueue: make(chan int64, 256),
	}
}

func ProvideTemporaryDispatchRuntime(groupRepo AdminGroupRepository, quota *OpenAIQuotaService, invalidator APIKeyAuthCacheInvalidator) *TemporaryDispatchRuntime {
	store, ok := groupRepo.(temporaryDispatchStore)
	if !ok {
		return nil
	}
	runtime := NewTemporaryDispatchRuntime(store, quota, invalidator)
	runtime.Start()
	return runtime
}

func (r *TemporaryDispatchRuntime) Start() {
	if r == nil || r.store == nil {
		return
	}
	r.start.Do(func() {
		setTemporaryDispatchNotifier(r)
		r.wg.Add(3)
		go r.runNotifications()
		go r.runUnavailableCleanup()
		go r.runScanner()
	})
}

func (r *TemporaryDispatchRuntime) Stop() {
	if r == nil {
		return
	}
	r.stop.Do(func() {
		clearTemporaryDispatchNotifier(r)
		r.cancel()
		r.wg.Wait()
	})
}

func (r *TemporaryDispatchRuntime) PrepareQuotaPlan(ctx context.Context, accountID int64, window string, delta float64) (*TemporaryDispatchQuotaPlan, error) {
	if r == nil || r.quota == nil {
		return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_QUOTA_UNAVAILABLE", "temporary dispatch quota service is unavailable")
	}
	if window == "" {
		window = TemporaryDispatchQuotaWindow5h
	}
	if window != TemporaryDispatchQuotaWindow5h && window != TemporaryDispatchQuotaWindow7d {
		return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_QUOTA_WINDOW", "quota_window must be 5h or 7d")
	}
	if math.IsNaN(delta) || math.IsInf(delta, 0) || delta <= 0 || delta > 100 {
		return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_QUOTA_DELTA", "target_delta_percent must be greater than 0 and at most 100")
	}
	preview, err := r.GetQuotaPreview(ctx, accountID, window)
	if err != nil {
		return nil, err
	}
	used, resetAt := preview.UsedPercent, preview.ResetAt
	target := used + delta
	if target > 100+1e-9 {
		return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_QUOTA_TARGET_EXCEEDED", fmt.Sprintf("current %s usage is %.3f%%; target delta cannot exceed %.3f percentage points", window, used, math.Max(0, 100-used)))
	}
	return &TemporaryDispatchQuotaPlan{Window: window, BaselinePercent: used, TargetPercent: math.Min(target, 100), ResetAt: resetAt}, nil
}

func (r *TemporaryDispatchRuntime) GetQuotaPreview(ctx context.Context, accountID int64, window string) (*TemporaryDispatchQuotaPreview, error) {
	if r == nil || r.quota == nil {
		return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_QUOTA_UNAVAILABLE", "temporary dispatch quota service is unavailable")
	}
	if window == "" {
		window = TemporaryDispatchQuotaWindow5h
	}
	if window != TemporaryDispatchQuotaWindow5h && window != TemporaryDispatchQuotaWindow7d {
		return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_QUOTA_WINDOW", "quota_window must be 5h or 7d")
	}
	usage, err := r.quota.QueryRateLimitUsage(ctx, accountID)
	if err != nil {
		return nil, err
	}
	used, resetAt, ok := quotaWindowFromUsage(usage, window, time.Now().UTC())
	if !ok {
		return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_QUOTA_WINDOW_UNAVAILABLE", fmt.Sprintf("account has no valid %s quota window", window))
	}
	return &TemporaryDispatchQuotaPreview{Window: window, UsedPercent: used, ResetAt: resetAt}, nil
}

func quotaWindowFromUsage(usage *OpenAIQuotaUsage, requested string, now time.Time) (float64, time.Time, bool) {
	if usage == nil || usage.RateLimit == nil {
		return 0, time.Time{}, false
	}
	for _, window := range []*OpenAIRateLimitWindow{usage.RateLimit.PrimaryWindow, usage.RateLimit.SecondaryWindow} {
		if window == nil {
			continue
		}
		kind := TemporaryDispatchQuotaWindow7d
		if window.LimitWindowSeconds > 0 && window.LimitWindowSeconds <= 6*60*60 {
			kind = TemporaryDispatchQuotaWindow5h
		}
		if kind != requested || window.UsedPercent < 0 || math.IsNaN(window.UsedPercent) || math.IsInf(window.UsedPercent, 0) {
			continue
		}
		resetAt := time.Unix(window.ResetAt, 0).UTC()
		if window.ResetAt <= 0 && window.ResetAfterSeconds > 0 {
			resetAt = now.Add(time.Duration(window.ResetAfterSeconds) * time.Second)
		}
		if !resetAt.After(now) {
			return 0, time.Time{}, false
		}
		return window.UsedPercent, resetAt, true
	}
	return 0, time.Time{}, false
}

func (r *TemporaryDispatchRuntime) Create(ctx context.Context, spec TemporaryDispatchCreateSpec) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("temporary dispatch runtime is unavailable")
	}
	return r.store.CreateTemporaryDispatch(ctx, spec)
}

func (r *TemporaryDispatchRuntime) StopGroups(ctx context.Context, groupIDs []int64) error {
	groupIDs, err := r.store.StopTemporaryDispatchGroups(ctx, groupIDs)
	if err != nil {
		return err
	}
	r.invalidate(groupIDs)
	return nil
}

func (r *TemporaryDispatchRuntime) Notify(accountID int64, snapshot *OpenAICodexUsageSnapshot) {
	if r == nil || accountID <= 0 || snapshot == nil {
		return
	}
	r.latest.Store(accountID, snapshot)
	if _, loaded := r.pending.LoadOrStore(accountID, struct{}{}); loaded {
		return
	}
	select {
	case <-r.ctx.Done():
		r.pending.Delete(accountID)
	case r.queue <- accountID:
	default:
		r.pending.Delete(accountID)
	}
}

// NotifyAccountUnavailable removes every override targeting an account after
// the request path has proved that the account is no longer usable. The
// operation is asynchronous so routing can immediately resume the original
// group pool without adding database latency to the hot path.
func (r *TemporaryDispatchRuntime) NotifyAccountUnavailable(accountID int64, reason string) {
	if r == nil || accountID <= 0 {
		return
	}
	r.unavailableReason.Store(accountID, reason)
	if _, loaded := r.unavailablePending.LoadOrStore(accountID, struct{}{}); loaded {
		return
	}
	select {
	case <-r.ctx.Done():
		r.unavailablePending.Delete(accountID)
	case r.unavailableQueue <- accountID:
	default:
		r.unavailablePending.Delete(accountID)
	}
}

func (r *TemporaryDispatchRuntime) runNotifications() {
	defer r.wg.Done()
	for {
		select {
		case <-r.ctx.Done():
			return
		case accountID := <-r.queue:
			r.pending.Delete(accountID)
			value, _ := r.latest.LoadAndDelete(accountID)
			snapshot, _ := value.(*OpenAICodexUsageSnapshot)
			if snapshot != nil {
				r.observeSnapshot(accountID, snapshot)
			}
		}
	}
}

func (r *TemporaryDispatchRuntime) runUnavailableCleanup() {
	defer r.wg.Done()
	for {
		select {
		case <-r.ctx.Done():
			return
		case accountID := <-r.unavailableQueue:
			r.unavailablePending.Delete(accountID)
			reasonValue, _ := r.unavailableReason.LoadAndDelete(accountID)
			reason, _ := reasonValue.(string)
			ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
			groupIDs, err := r.store.StopTemporaryDispatchAccount(ctx, accountID)
			cancel()
			if err != nil {
				slog.Warn("temporary_dispatch_account_cleanup_failed", "account_id", accountID, "reason", reason, "error", err)
				continue
			}
			if len(groupIDs) > 0 {
				slog.Info("temporary_dispatch_ended", "reason", reason, "account_id", accountID, "group_ids", groupIDs)
			}
			r.invalidate(groupIDs)
		}
	}
}

func (r *TemporaryDispatchRuntime) observeSnapshot(accountID int64, snapshot *OpenAICodexUsageSnapshot) {
	normalized := snapshot.Normalize()
	if normalized == nil {
		return
	}
	observedAt := codexSnapshotBaseTime(snapshot, time.Now().UTC())
	for _, item := range []struct {
		window string
		used   *float64
	}{{TemporaryDispatchQuotaWindow5h, normalized.Used5hPercent}, {TemporaryDispatchQuotaWindow7d, normalized.Used7dPercent}} {
		if item.used == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
		groupIDs, err := r.store.ObserveTemporaryDispatchQuota(ctx, accountID, item.window, *item.used, observedAt)
		cancel()
		if err != nil {
			slog.Warn("temporary_dispatch_quota_observe_failed", "account_id", accountID, "window", item.window, "error", err)
			continue
		}
		r.invalidate(groupIDs)
	}
}

func (r *TemporaryDispatchRuntime) runScanner() {
	defer r.wg.Done()
	r.scanOnce()
	ticker := time.NewTicker(temporaryDispatchScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.scanOnce()
		}
	}
}

func (r *TemporaryDispatchRuntime) scanOnce() {
	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
	groupIDs, err := r.store.CleanupTemporaryDispatches(ctx, now, 200)
	cancel()
	if err != nil {
		slog.Warn("temporary_dispatch_cleanup_failed", "error", err)
	} else {
		r.invalidate(groupIDs)
	}
	if r.quota == nil {
		return
	}
	claimCtx, claimCancel := context.WithTimeout(r.ctx, 5*time.Second)
	accountIDs, err := r.store.ClaimStaleTemporaryDispatchAccounts(claimCtx, now.Add(-temporaryDispatchStaleAfter), now, temporaryDispatchPollBatch)
	claimCancel()
	if err != nil {
		slog.Warn("temporary_dispatch_quota_claim_failed", "error", err)
		return
	}
	r.pollClaimedAccounts(accountIDs, now)
}

func (r *TemporaryDispatchRuntime) pollClaimedAccounts(accountIDs []int64, now time.Time) {
	workers := temporaryDispatchPollWorkers
	if len(accountIDs) < workers {
		workers = len(accountIDs)
	}
	if workers == 0 {
		return
	}
	jobs := make(chan int64)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for accountID := range jobs {
				usageCtx, usageCancel := context.WithTimeout(r.ctx, 25*time.Second)
				usage, queryErr := r.quota.QueryRateLimitUsage(usageCtx, accountID)
				usageCancel()
				if queryErr != nil {
					slog.Warn("temporary_dispatch_quota_poll_failed", "account_id", accountID, "error", queryErr)
					continue
				}
				r.observeUsage(accountID, usage, now)
			}
		}()
	}
	for _, accountID := range accountIDs {
		select {
		case <-r.ctx.Done():
			close(jobs)
			wg.Wait()
			return
		case jobs <- accountID:
		}
	}
	close(jobs)
	wg.Wait()
}

func (r *TemporaryDispatchRuntime) observeUsage(accountID int64, usage *OpenAIQuotaUsage, now time.Time) {
	for _, window := range []string{TemporaryDispatchQuotaWindow5h, TemporaryDispatchQuotaWindow7d} {
		used, _, ok := quotaWindowFromUsage(usage, window, now)
		if !ok {
			continue
		}
		ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
		groupIDs, err := r.store.ObserveTemporaryDispatchQuota(ctx, accountID, window, used, now)
		cancel()
		if err != nil {
			slog.Warn("temporary_dispatch_quota_poll_persist_failed", "account_id", accountID, "window", window, "error", err)
			continue
		}
		r.invalidate(groupIDs)
	}
}

func (r *TemporaryDispatchRuntime) invalidate(groupIDs []int64) {
	if r == nil || r.invalidator == nil {
		return
	}
	for _, groupID := range groupIDs {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		r.invalidator.InvalidateAuthCacheByGroupID(ctx, groupID)
		cancel()
	}
}

var temporaryDispatchNotifierRegistry struct {
	sync.RWMutex
	runtime *TemporaryDispatchRuntime
}

func setTemporaryDispatchNotifier(runtime *TemporaryDispatchRuntime) {
	temporaryDispatchNotifierRegistry.Lock()
	temporaryDispatchNotifierRegistry.runtime = runtime
	temporaryDispatchNotifierRegistry.Unlock()
}

func clearTemporaryDispatchNotifier(runtime *TemporaryDispatchRuntime) {
	temporaryDispatchNotifierRegistry.Lock()
	if temporaryDispatchNotifierRegistry.runtime == runtime {
		temporaryDispatchNotifierRegistry.runtime = nil
	}
	temporaryDispatchNotifierRegistry.Unlock()
}

func notifyTemporaryDispatchQuota(accountID int64, snapshot *OpenAICodexUsageSnapshot) {
	temporaryDispatchNotifierRegistry.RLock()
	runtime := temporaryDispatchNotifierRegistry.runtime
	temporaryDispatchNotifierRegistry.RUnlock()
	if runtime != nil {
		runtime.Notify(accountID, snapshot)
	}
}

func notifyTemporaryDispatchAccountUnavailable(accountID int64, reason string) {
	temporaryDispatchNotifierRegistry.RLock()
	runtime := temporaryDispatchNotifierRegistry.runtime
	temporaryDispatchNotifierRegistry.RUnlock()
	if runtime != nil {
		runtime.NotifyAccountUnavailable(accountID, reason)
	}
}
