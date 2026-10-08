package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTP     HTTP
	Database Database
	OIDC     OIDC
	SQS      SQS
	Workers  Workers
	Runtime  Runtime
}

type HTTP struct {
	Address           string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

type Database struct {
	URL      string
	MaxConns int32
}

type OIDC struct {
	IssuerURL string
	Audience  string
}

type Queue struct {
	Name string
	URL  string
}

type SQS struct {
	Endpoint     string
	Region       string
	AccessKey    string
	SecretKey    string
	SessionToken string
	Input        Queue
	DLQ          Queue
	Events       Queue

	ConsumerName      string
	ReceiveWait       time.Duration
	VisibilityTimeout time.Duration
	ProcessingTimeout time.Duration
	OperationTimeout  time.Duration
	IdleDelay         time.Duration
	MaxReceiveCount   int32
}

type Workers struct {
	OutboxPollInterval               time.Duration
	OutboxOperationTimeout           time.Duration
	PendingReferencePollInterval     time.Duration
	PendingReferenceOperationTimeout time.Duration
	PendingReferenceBatchSize        int
}

type Runtime struct {
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
	LogLevel        slog.Level
}

func Load() (Config, error) {
	return load(os.LookupEnv)
}

type lookupEnv func(string) (string, bool)

func load(lookup lookupEnv) (Config, error) {
	var result Config
	var err error
	required := func(name string) string {
		value, ok := lookup(name)
		if !ok || strings.TrimSpace(value) == "" {
			err = errors.Join(err, fmt.Errorf("%s is required", name))
			return ""
		}
		return value
	}
	value := func(name, fallback string) string {
		if current, ok := lookup(name); ok {
			return current
		}
		return fallback
	}
	duration := func(name string, fallback time.Duration) time.Duration {
		raw := value(name, fallback.String())
		parsed, parseErr := time.ParseDuration(raw)
		if parseErr != nil || parsed <= 0 {
			err = errors.Join(err, fmt.Errorf("%s must be a positive duration", name))
			return 0
		}
		return parsed
	}
	integer := func(name string, fallback, minimum, maximum int64) int64 {
		raw := value(name, strconv.FormatInt(fallback, 10))
		parsed, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || parsed < minimum || parsed > maximum {
			err = errors.Join(err, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum))
			return 0
		}
		return parsed
	}

	result.HTTP = HTTP{
		Address:           required("HTTP_ADDR"),
		ReadHeaderTimeout: duration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:       duration("HTTP_READ_TIMEOUT", 15*time.Second),
		WriteTimeout:      duration("HTTP_WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:       duration("HTTP_IDLE_TIMEOUT", 60*time.Second),
	}
	result.Database = Database{
		URL:      required("DATABASE_URL"),
		MaxConns: int32(integer("DATABASE_MAX_CONNS", 20, 1, 1000)),
	}
	result.OIDC = OIDC{IssuerURL: required("OIDC_ISSUER_URL"), Audience: required("OIDC_AUDIENCE")}
	result.SQS = SQS{
		Endpoint:          value("SQS_ENDPOINT_URL", ""),
		Region:            required("AWS_REGION"),
		AccessKey:         value("AWS_ACCESS_KEY_ID", ""),
		SecretKey:         value("AWS_SECRET_ACCESS_KEY", ""),
		SessionToken:      value("AWS_SESSION_TOKEN", ""),
		Input:             Queue{Name: required("SQS_INPUT_QUEUE_NAME"), URL: value("SQS_INPUT_QUEUE_URL", "")},
		DLQ:               Queue{Name: required("SQS_DLQ_QUEUE_NAME"), URL: value("SQS_DLQ_QUEUE_URL", "")},
		Events:            Queue{Name: required("SQS_EVENTS_QUEUE_NAME"), URL: value("SQS_EVENTS_QUEUE_URL", "")},
		ConsumerName:      required("SQS_CONSUMER_NAME"),
		ReceiveWait:       duration("SQS_RECEIVE_WAIT", 20*time.Second),
		VisibilityTimeout: duration("SQS_VISIBILITY_TIMEOUT", 30*time.Second),
		ProcessingTimeout: duration("SQS_PROCESSING_TIMEOUT", 20*time.Second),
		OperationTimeout:  duration("SQS_OPERATION_TIMEOUT", 5*time.Second),
		IdleDelay:         duration("SQS_IDLE_DELAY", time.Second),
		MaxReceiveCount:   int32(integer("SQS_MAX_RECEIVE_COUNT", 5, 1, 1000)),
	}
	result.Workers = Workers{
		OutboxPollInterval:               duration("OUTBOX_POLL_INTERVAL", time.Second),
		OutboxOperationTimeout:           duration("OUTBOX_OPERATION_TIMEOUT", 15*time.Second),
		PendingReferencePollInterval:     duration("PENDING_REFERENCE_POLL_INTERVAL", time.Second),
		PendingReferenceOperationTimeout: duration("PENDING_REFERENCE_OPERATION_TIMEOUT", 15*time.Second),
		PendingReferenceBatchSize:        int(integer("PENDING_REFERENCE_BATCH_SIZE", 10, 1, 100)),
	}
	result.Runtime = Runtime{
		StartupTimeout:  duration("STARTUP_TIMEOUT", 15*time.Second),
		ShutdownTimeout: duration("SHUTDOWN_TIMEOUT", 20*time.Second),
	}

	if (result.SQS.AccessKey == "") != (result.SQS.SecretKey == "") {
		err = errors.Join(err, errors.New("AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY must be provided together"))
	}
	if result.SQS.ReceiveWait > 20*time.Second || result.SQS.ReceiveWait%time.Second != 0 {
		err = errors.Join(err, errors.New("SQS_RECEIVE_WAIT must be whole seconds no greater than 20s"))
	}
	if result.SQS.VisibilityTimeout%time.Second != 0 || result.SQS.VisibilityTimeout > 12*time.Hour {
		err = errors.Join(err, errors.New("SQS_VISIBILITY_TIMEOUT must be whole seconds no greater than 12h"))
	}
	if result.SQS.ProcessingTimeout >= result.SQS.VisibilityTimeout {
		err = errors.Join(err, errors.New("SQS_PROCESSING_TIMEOUT must be shorter than SQS_VISIBILITY_TIMEOUT"))
	}
	if result.Runtime.ShutdownTimeout > 2*time.Minute {
		err = errors.Join(err, errors.New("SHUTDOWN_TIMEOUT must not exceed 2m"))
	}

	level := new(slog.LevelVar)
	if levelErr := level.UnmarshalText([]byte(value("LOG_LEVEL", "info"))); levelErr != nil {
		err = errors.Join(err, fmt.Errorf("LOG_LEVEL: %w", levelErr))
	} else {
		result.Runtime.LogLevel = level.Level()
	}
	if err != nil {
		return Config{}, fmt.Errorf("invalid application configuration: %w", err)
	}
	return result, nil
}
