// Package job 提供统一的定时任务调度器。
//
// 取代原版 Python 的 9 个手写 asyncio loop。
// 使用 robfig/cron + 交易日历，在交易日特定时间点执行。
package job

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jwcen/argent-go/internal/market"
)

// Scheduler 管理所有定时任务。
type Scheduler struct {
	jobs   []*scheduledJob
	logger *slog.Logger
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type scheduledJob struct {
	name     string
	interval time.Duration
	fn       func(context.Context)
}

func NewScheduler(logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{logger: logger}
}

// Add 注册一个定时任务，按 interval 间隔执行。
func (s *Scheduler) Add(name string, interval time.Duration, fn func(context.Context)) {
	s.jobs = append(s.jobs, &scheduledJob{name: name, interval: interval, fn: fn})
}

// AddTradingDay 注册一个仅在交易日执行的任务。
func (s *Scheduler) AddTradingDay(name string, interval time.Duration, fn func(context.Context)) {
	wrapped := func(ctx context.Context) {
		if !market.IsTradingDay(time.Now()) {
			return
		}
		fn(ctx)
	}
	s.jobs = append(s.jobs, &scheduledJob{name: name, interval: interval, fn: wrapped})
}

// Start 启动所有任务。
func (s *Scheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	for _, job := range s.jobs {
		s.wg.Add(1)
		go s.run(ctx, job)
	}
	s.logger.Info("job scheduler started", "jobs", len(s.jobs))
}

func (s *Scheduler) run(ctx context.Context, job *scheduledJob) {
	defer s.wg.Done()
	ticker := time.NewTicker(job.interval)
	defer ticker.Stop()
	s.logger.Info("job started", "name", job.name, "interval", job.interval)
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("job stopped", "name", job.name)
			return
		case <-ticker.C:
			job.fn(ctx)
		}
	}
}

// Stop 停止所有任务并等待退出。
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	s.logger.Info("job scheduler stopped")
}
