package application

import (
	"context"

	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

type ReadWagers struct{ repository WagerTransactionRepository }

func NewReadWagers(repository WagerTransactionRepository) *ReadWagers {
	return &ReadWagers{repository: repository}
}

func (u *ReadWagers) FindByID(ctx context.Context, providerID, id string) (*domain.WagerTransaction, error) {
	if providerID == "" {
		return nil, ErrNotFound
	}
	return u.repository.FindByIDAndProvider(ctx, id, providerID)
}

func (u *ReadWagers) FindByExternalID(ctx context.Context, providerID, externalID string) (*domain.WagerTransaction, error) {
	if providerID == "" {
		return nil, ErrNotFound
	}
	return u.repository.FindByProviderAndExternalTransactionID(ctx, providerID, externalID)
}
