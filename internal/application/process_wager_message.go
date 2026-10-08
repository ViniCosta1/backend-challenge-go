package application

import (
	"context"
	"fmt"

	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

type ProcessWagerMessageInput struct {
	MessageID   string
	PayloadHash string
	Wager       ProcessWagerTransactionInput
}

type ProcessWagerMessageOutput struct {
	InboxReplay bool
	Wager       ProcessWagerTransactionOutput
}

type ProcessWagerMessage struct {
	transactions TransactionManager
	processor    *ProcessWagerTransaction
	consumerName string
}

func NewProcessWagerMessage(transactions TransactionManager, consumerName string, generateID IDGenerator) (*ProcessWagerMessage, error) {
	if consumerName == "" {
		return nil, domain.ErrInboxConsumerNameRequired
	}
	return &ProcessWagerMessage{transactions, NewProcessWagerTransaction(transactions, generateID), consumerName}, nil
}

func (u *ProcessWagerMessage) Execute(ctx context.Context, input ProcessWagerMessageInput) (ProcessWagerMessageOutput, error) {
	message, err := domain.NewInboxMessage(u.consumerName, input.MessageID, input.PayloadHash)
	if err != nil {
		return ProcessWagerMessageOutput{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	var output ProcessWagerMessageOutput
	err = u.transactions.WithinTransaction(ctx, func(repositories TransactionRepositories) error {
		if _, err := repositories.Inbox().CreateIfAbsent(ctx, &message); err != nil {
			return err
		}
		// The INSERT arbitrates concurrent creation; FOR UPDATE serializes
		// recovery of an existing, not-yet-completed Inbox record as well.
		persisted, err := repositories.Inbox().FindByConsumerAndMessageIDForUpdate(ctx, u.consumerName, input.MessageID)
		if err != nil {
			return err
		}
		if persisted.PayloadHash() != input.PayloadHash {
			return ErrInboxPayloadConflict
		}
		if persisted.IsCompleted() {
			output.InboxReplay = true
			return nil
		}
		output.Wager, err = u.processor.executeWithRepositories(ctx, repositories, input.Wager)
		if err != nil {
			return err
		}
		if err := persisted.MarkCompleted(); err != nil {
			return err
		}
		return repositories.Inbox().Complete(ctx, persisted)
	})
	if err != nil {
		return ProcessWagerMessageOutput{}, fmt.Errorf("process wager message: %w", err)
	}
	return output, nil
}
