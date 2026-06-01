package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/marvinvr/docker-registry-garbage-collector/internal/config"
	registrygc "github.com/marvinvr/docker-registry-garbage-collector/internal/gc"
	"github.com/marvinvr/docker-registry-garbage-collector/internal/logging"
	"github.com/marvinvr/docker-registry-garbage-collector/internal/registry"
	"github.com/marvinvr/docker-registry-garbage-collector/internal/runner"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}

	logger, err := logging.New(cfg.LogLevel)
	if err != nil {
		slog.Error("logging configuration error", "error", err)
		os.Exit(1)
	}
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, err := registry.NewClient(registry.ClientConfig{
		BaseURL:  cfg.RegistryURL,
		Username: cfg.RegistryUsername,
		Password: cfg.RegistryPassword,
		Token:    cfg.RegistryToken,
		Timeout:  cfg.HTTPTimeout,
	})
	if err != nil {
		logger.Error("registry client configuration error", "error", err)
		os.Exit(1)
	}

	jobRunner := runner.New(cfg, client, registrygc.NewExecutor(cfg.RegistryBinary), logger)

	var running atomic.Bool
	runJob := func(trigger string) error {
		if !running.CompareAndSwap(false, true) {
			logger.Warn("skipping run because previous run is still active", "trigger", trigger)
			return nil
		}
		defer running.Store(false)

		started := time.Now()
		err := jobRunner.Run(ctx, trigger)
		if err != nil {
			logger.Error("run failed", "trigger", trigger, "duration", time.Since(started).String(), "error", err)
			return err
		}
		logger.Info("run finished", "trigger", trigger, "duration", time.Since(started).String())
		return nil
	}

	if cfg.RunOnce {
		if err := runJob("run_once"); err != nil {
			os.Exit(1)
		}
		return
	}

	scheduler := cron.New()
	if _, err := scheduler.AddFunc(cfg.CronSchedule, func() {
		_ = runJob("cron")
	}); err != nil {
		logger.Error("invalid cron schedule", "schedule", cfg.CronSchedule, "error", err)
		os.Exit(1)
	}

	if cfg.RunOnStart {
		_ = runJob("startup")
	}

	scheduler.Start()
	logger.Info("scheduler started", "schedule", cfg.CronSchedule)

	<-ctx.Done()
	logger.Info("shutdown requested")
	stopCtx := scheduler.Stop()
	<-stopCtx.Done()
}
