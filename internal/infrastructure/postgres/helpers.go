package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/puddle/v2"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
)

type rowScanner interface {
	Scan(dest ...any) error
}

func wrapQueryError(resource string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", resource, application.ErrNotFound)
	}

	return fmt.Errorf("%s: %w", resource, classifyDatabaseError(err))
}

func classifyDatabaseError(err error) error {
	var networkError net.Error
	var pgError *pgconn.PgError
	unavailable := errors.Is(err, context.DeadlineExceeded) || errors.Is(err, puddle.ErrClosedPool) || errors.As(err, &networkError)
	if errors.As(err, &pgError) {
		unavailable = unavailable || len(pgError.Code) >= 2 && pgError.Code[:2] == "08" ||
			pgError.Code == "53300" || pgError.Code == "57P01" || pgError.Code == "57P02" ||
			pgError.Code == "57P03" || pgError.Code == "40001" || pgError.Code == "40P01"
	}
	if unavailable {
		return fmt.Errorf("%w: %w", application.ErrUnavailable, err)
	}
	return err
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}

	return value
}

func nullableTime(value time.Time, present bool) any {
	if !present {
		return nil
	}

	return value
}

func textValue(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}

	return value.String
}
