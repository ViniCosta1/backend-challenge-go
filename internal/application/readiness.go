package application

import (
	"context"
	"errors"
)

type ReadinessCheck struct {
	Name  string
	Check func(context.Context) error
}

type ReadinessResult struct {
	Ready  bool
	Checks map[string]string
}

// Checks are supplied by composition: PostgreSQL now, SQS when implemented.
type Readiness struct{ checks []ReadinessCheck }

func NewReadiness(checks ...ReadinessCheck) (*Readiness, error) {
	if len(checks) == 0 {
		return nil, errors.New("at least one readiness check is required")
	}
	names := make(map[string]bool, len(checks))
	for _, check := range checks {
		if check.Name == "" || check.Check == nil || names[check.Name] {
			return nil, errors.New("invalid readiness check")
		}
		names[check.Name] = true
	}
	return &Readiness{checks: append([]ReadinessCheck(nil), checks...)}, nil
}

func (u *Readiness) Execute(ctx context.Context) ReadinessResult {
	result := ReadinessResult{Ready: true, Checks: make(map[string]string, len(u.checks))}
	for _, check := range u.checks {
		status := "up"
		if err := check.Check(ctx); err != nil {
			status = "down"
			result.Ready = false
		}
		result.Checks[check.Name] = status
	}
	return result
}
