package domain

import (
	"errors"
	"math"
	"time"
)

var ErrInvalidReferenceRetry = errors.New("invalid reference retry state")

func (w *WagerTransaction) ScheduleReferenceRetry(next time.Time) error {
	if w.status != WagerStatusPendingReference || next.IsZero() ||
		next.Location() != time.UTC || next.Before(w.createdAt) {
		return ErrInvalidReferenceRetry
	}
	w.referenceNextAttemptAt = &next
	w.updatedAt = time.Now().UTC()
	return nil
}

func (w *WagerTransaction) RegisterReferenceAttempt() error {
	if w.status != WagerStatusPendingReference || w.referenceAttempts == math.MaxInt32 {
		return ErrInvalidReferenceRetry
	}
	w.referenceAttempts++
	w.updatedAt = time.Now().UTC()
	return nil
}
