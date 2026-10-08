package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/vinicosta1/backend-challenge-go/internal/config"
	infraSQS "github.com/vinicosta1/backend-challenge-go/internal/infrastructure/sqs"
	"github.com/vinicosta1/backend-challenge-go/internal/infrastructure/worker"
	"github.com/vinicosta1/backend-challenge-go/internal/transport/httpserver"
)

func registerRuntime(lifecycle fx.Lifecycle, settings config.Config, server *httpserver.Server,
	consumer *infraSQS.Consumer, publisher *infraSQS.Publisher, pending *worker.PendingReference, logger *slog.Logger) {
	var cancelRuntime context.CancelFunc
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			runtimeContext, cancel := context.WithCancel(context.Background())
			cancelRuntime = cancel
			if err := pending.Start(runtimeContext); err != nil {
				cancel()
				return err
			}
			if err := publisher.Start(runtimeContext); err != nil {
				stopStarted(pending.Stop)
				cancel()
				return err
			}
			if err := consumer.Start(runtimeContext); err != nil {
				stopStarted(publisher.Stop, pending.Stop)
				cancel()
				return err
			}
			if err := server.Start(ctx); err != nil {
				stopStarted(consumer.Stop, publisher.Stop, pending.Stop)
				cancel()
				return err
			}
			logger.InfoContext(ctx, "application runtime started")
			return nil
		},
		OnStop: func(ctx context.Context) error {
			shutdown, cancel := boundedContext(ctx, settings.Runtime.ShutdownTimeout)
			defer cancel()
			// Stop new ingress first. Worker Stop methods stop polling before
			// waiting for in-flight work, so no durable work is abandoned early.
			err := errors.Join(server.Stop(shutdown), consumer.Stop(shutdown), publisher.Stop(shutdown), pending.Stop(shutdown))
			if cancelRuntime != nil {
				cancelRuntime()
			}
			if err != nil {
				logger.ErrorContext(ctx, "application runtime stopped with errors", "error", err)
				return err
			}
			logger.InfoContext(ctx, "application runtime stopped")
			return nil
		},
	})
}

func boundedContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= timeout {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

func stopStarted(stops ...func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, stop := range stops {
		_ = stop(ctx)
	}
}
