package domain

import "errors"

type IntegrationEventType string

const (
	IntegrationEventTypeWagerTransactionProcessed        IntegrationEventType = "WagerTransactionProcessed"
	IntegrationEventTypeWagerTransactionRejected         IntegrationEventType = "WagerTransactionRejected"
	IntegrationEventTypeWalletBalanceChanged             IntegrationEventType = "WalletBalanceChanged"
	IntegrationEventTypeWagerTransactionPendingReference IntegrationEventType = "WagerTransactionPendingReference"
)

const integrationEventVersion int32 = 1

var ErrInvalidIntegrationEventType = errors.New(
	"invalid integration event type",
)

func isValidIntegrationEventType(eventType IntegrationEventType) bool {
	switch eventType {
	case IntegrationEventTypeWagerTransactionProcessed,
		IntegrationEventTypeWagerTransactionRejected,
		IntegrationEventTypeWalletBalanceChanged,
		IntegrationEventTypeWagerTransactionPendingReference:
		return true

	default:
		return false
	}
}
