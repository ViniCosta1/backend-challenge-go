package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/auth"
	"github.com/vinicosta1/backend-challenge-go/internal/config"
	"github.com/vinicosta1/backend-challenge-go/internal/infrastructure/oidc"
	"github.com/vinicosta1/backend-challenge-go/internal/infrastructure/postgres"
	infraSQS "github.com/vinicosta1/backend-challenge-go/internal/infrastructure/sqs"
	"github.com/vinicosta1/backend-challenge-go/internal/infrastructure/worker"
	"github.com/vinicosta1/backend-challenge-go/internal/observability"
	"github.com/vinicosta1/backend-challenge-go/internal/transport/httpapi"
	"github.com/vinicosta1/backend-challenge-go/internal/transport/httpserver"
)

var configModule = fx.Module("config", fx.Provide(config.Load))

var observabilityModule = fx.Module("observability",
	fx.Provide(newLogger, observability.NewMetrics),
)

var postgresModule = fx.Module("postgres",
	fx.Provide(newPool, newRepositorySet, newTransactionManager),
)

var authModule = fx.Module("auth",
	fx.Provide(fx.Annotate(newAuthenticator, fx.As(new(auth.Authenticator)))),
)

var applicationModule = fx.Module("application",
	fx.Provide(newCreateWallet, newProcessWager, newReadWallets, newReadWagers,
		newProcessWagerMessage, newRetryPendingReferences, newPublishOutbox, newReadiness),
)

var messagingModule = fx.Module("messaging",
	fx.Provide(newSQSConfig, newSQSClient, newQueues,
		fx.Annotate(newEventSender, fx.As(new(application.IntegrationEventPublisher))),
		newConsumer, newPublisher, newPendingReferenceWorker),
)

var httpModule = fx.Module("http",
	fx.Provide(newHTTPHandler, newHTTPServer),
)

// Module composes constructors only. registerRuntime is the single behavior
// invocation and owns the externally visible start/stop ordering.
var Module = fx.Options(
	configModule,
	observabilityModule,
	postgresModule,
	authModule,
	applicationModule,
	messagingModule,
	httpModule,
	fx.WithLogger(func(logger *slog.Logger) fxevent.Logger {
		return &fxevent.SlogLogger{Logger: logger}
	}),
	fx.Invoke(registerRuntime),
)

func New(options ...fx.Option) *fx.App {
	base := []fx.Option{Module, fx.StartTimeout(2 * time.Minute), fx.StopTimeout(2 * time.Minute)}
	return fx.New(append(base, options...)...)
}

func newLogger(settings config.Config) *slog.Logger {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: settings.Runtime.LogLevel}))
	slog.SetDefault(logger)
	return logger
}

func newPool(lifecycle fx.Lifecycle, settings config.Config, logger *slog.Logger) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(settings.Database.URL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	poolConfig.MaxConns = settings.Database.MaxConns
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			startup, cancel := boundedContext(ctx, settings.Runtime.StartupTimeout)
			defer cancel()
			if err := pool.Ping(startup); err != nil {
				return fmt.Errorf("connect PostgreSQL: %w", err)
			}
			logger.InfoContext(ctx, "PostgreSQL pool ready", "max_connections", settings.Database.MaxConns)
			return nil
		},
		OnStop: func(context.Context) error {
			pool.Close()
			logger.Info("PostgreSQL pool closed")
			return nil
		},
	})
	return pool, nil
}

func newTransactionManager(pool *pgxpool.Pool) application.TransactionManager {
	return postgres.NewTransactionManager(pool)
}

func newRepositorySet(pool *pgxpool.Pool) *postgres.RepositorySet {
	return postgres.NewRepositorySet(pool)
}

func newAuthenticator(settings config.Config) (*oidc.Authenticator, error) {
	ctx, cancel := context.WithTimeout(context.Background(), settings.Runtime.StartupTimeout)
	defer cancel()
	return oidc.NewAuthenticator(ctx, settings.OIDC.IssuerURL, settings.OIDC.Audience)
}

func newCreateWallet(transactions application.TransactionManager) *application.CreateWallet {
	return application.NewCreateWallet(transactions, nil)
}

func newProcessWager(transactions application.TransactionManager) *application.ProcessWagerTransaction {
	return application.NewProcessWagerTransaction(transactions, nil)
}

func newReadWallets(repositories *postgres.RepositorySet, logger *slog.Logger) *application.ReadWallets {
	return application.NewReadWallets(repositories.Wallets(), repositories.Ledger(), logger)
}

func newReadWagers(repositories *postgres.RepositorySet) *application.ReadWagers {
	return application.NewReadWagers(repositories.WagerTransactions())
}

func newProcessWagerMessage(transactions application.TransactionManager, settings config.Config) (*application.ProcessWagerMessage, error) {
	return application.NewProcessWagerMessage(transactions, settings.SQS.ConsumerName, nil)
}

func newRetryPendingReferences(transactions application.TransactionManager) *application.RetryPendingReferences {
	return application.NewRetryPendingReferences(transactions, nil)
}

func newPublishOutbox(transactions application.TransactionManager, publisher application.IntegrationEventPublisher) *application.PublishOutbox {
	return application.NewPublishOutbox(transactions, publisher)
}

func newSQSConfig(settings config.Config) infraSQS.Config {
	return infraSQS.Config{Endpoint: settings.SQS.Endpoint, Region: settings.SQS.Region,
		AccessKey: settings.SQS.AccessKey, SecretKey: settings.SQS.SecretKey, SessionToken: settings.SQS.SessionToken,
		Input:  infraSQS.QueueConfig{Name: settings.SQS.Input.Name, URL: settings.SQS.Input.URL},
		DLQ:    infraSQS.QueueConfig{Name: settings.SQS.DLQ.Name, URL: settings.SQS.DLQ.URL},
		Events: infraSQS.QueueConfig{Name: settings.SQS.Events.Name, URL: settings.SQS.Events.URL}}
}

func newSQSClient(settings config.Config, sqsSettings infraSQS.Config) (*awssqs.Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), settings.Runtime.StartupTimeout)
	defer cancel()
	return infraSQS.NewClient(ctx, sqsSettings)
}

func newQueues(client *awssqs.Client, settings infraSQS.Config, appSettings config.Config) (infraSQS.Queues, error) {
	ctx, cancel := context.WithTimeout(context.Background(), appSettings.Runtime.StartupTimeout)
	defer cancel()
	return infraSQS.ResolveQueues(ctx, client, settings)
}

func newEventSender(client *awssqs.Client, queues infraSQS.Queues) *infraSQS.EventSender {
	return infraSQS.NewEventSender(client, queues.Events)
}

func newConsumer(client *awssqs.Client, queues infraSQS.Queues, processor *application.ProcessWagerMessage,
	logger *slog.Logger, metrics *observability.Metrics, settings config.Config) *infraSQS.Consumer {
	return infraSQS.NewConsumer(client, queues.Input, processor, logger,
		infraSQS.WithConsumerSettings(infraSQS.ConsumerSettings{
			ReceiveWait: settings.SQS.ReceiveWait, VisibilityTimeout: settings.SQS.VisibilityTimeout,
			ProcessingTimeout: settings.SQS.ProcessingTimeout, OperationTimeout: settings.SQS.OperationTimeout,
			IdleDelay: settings.SQS.IdleDelay, MaxReceiveCount: settings.SQS.MaxReceiveCount,
		}), infraSQS.WithConsumerMetrics(metrics))
}

func newPublisher(useCase *application.PublishOutbox, logger *slog.Logger, metrics *observability.Metrics,
	settings config.Config) *infraSQS.Publisher {
	return infraSQS.NewPublisher(useCase, logger,
		infraSQS.WithPublisherSettings(settings.Workers.OutboxPollInterval, settings.Workers.OutboxOperationTimeout),
		infraSQS.WithPublisherMetrics(metrics))
}

func newPendingReferenceWorker(useCase *application.RetryPendingReferences, logger *slog.Logger,
	metrics *observability.Metrics, settings config.Config) (*worker.PendingReference, error) {
	return worker.NewPendingReference(useCase, logger, metrics, worker.PendingReferenceSettings{
		PollInterval:     settings.Workers.PendingReferencePollInterval,
		OperationTimeout: settings.Workers.PendingReferenceOperationTimeout,
		BatchSize:        settings.Workers.PendingReferenceBatchSize,
	})
}

func newReadiness(pool *pgxpool.Pool, client *awssqs.Client, settings infraSQS.Config) (*application.Readiness, error) {
	return application.NewReadiness(
		application.ReadinessCheck{Name: "postgres", Check: pool.Ping},
		application.ReadinessCheck{Name: "sqs", Check: infraSQS.NewReadinessChecker(client, settings).Check},
	)
}

func newHTTPHandler(createWallet *application.CreateWallet, processWager *application.ProcessWagerTransaction,
	wallets *application.ReadWallets, wagers *application.ReadWagers, readiness *application.Readiness,
	authenticator auth.Authenticator, logger *slog.Logger, metrics *observability.Metrics) http.Handler {
	return httpapi.NewHandler(createWallet, processWager, wallets, wagers, readiness, authenticator, logger,
		httpapi.WithObservability(metrics, metrics.Handler()))
}

func newHTTPServer(handler http.Handler, settings config.Config, logger *slog.Logger,
	shutdowner fx.Shutdowner) (*httpserver.Server, error) {
	return httpserver.New(handler, httpserver.Settings{Address: settings.HTTP.Address,
		ReadHeaderTimeout: settings.HTTP.ReadHeaderTimeout, ReadTimeout: settings.HTTP.ReadTimeout,
		WriteTimeout: settings.HTTP.WriteTimeout, IdleTimeout: settings.HTTP.IdleTimeout}, logger,
		func(error) {
			if err := shutdowner.Shutdown(fx.ExitCode(1)); err != nil {
				logger.Error("request Fx shutdown after HTTP failure", "error", err)
			}
		})
}
