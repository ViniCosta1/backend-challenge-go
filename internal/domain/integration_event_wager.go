package domain

type WagerTransactionProcessed struct {
	envelope              IntegrationEventEnvelope
	transactionID         string
	walletID              string
	providerID            string
	externalTransactionID string
	kind                  WagerKind
	resultBalance         Money
}

type NewWagerTransactionProcessedParams struct {
	EventID       string
	CorrelationID string
	CausationID   string

	TransactionID         string
	WalletID              string
	ProviderID            string
	ExternalTransactionID string
	Kind                  WagerKind
	ResultBalance         Money
}

func NewWagerTransactionProcessed(
	p NewWagerTransactionProcessedParams,
) (WagerTransactionProcessed, error) {
	if err := validateProcessedEventPayload(p); err != nil {
		return WagerTransactionProcessed{}, err
	}

	envelope, err := newIntegrationEventEnvelope(
		p.EventID,
		IntegrationEventTypeWagerTransactionProcessed,
		p.TransactionID,
		p.CorrelationID,
		p.CausationID,
	)
	if err != nil {
		return WagerTransactionProcessed{}, err
	}

	return WagerTransactionProcessed{
		envelope:              envelope,
		transactionID:         p.TransactionID,
		walletID:              p.WalletID,
		providerID:            p.ProviderID,
		externalTransactionID: p.ExternalTransactionID,
		kind:                  p.Kind,
		resultBalance:         p.ResultBalance,
	}, nil
}

func validateProcessedEventPayload(
	p NewWagerTransactionProcessedParams,
) error {
	if p.TransactionID == "" {
		return ErrIntegrationTransactionIDRequired
	}

	if p.WalletID == "" {
		return ErrIntegrationWalletIDRequired
	}

	if !isValidWagerKind(p.Kind) {
		return ErrInvalidWagerKind
	}

	if p.Kind == WagerKindOpening {
		if p.ProviderID != "" || p.ExternalTransactionID != "" {
			return ErrInvalidIntegrationEventPayload
		}
	} else {
		if p.ProviderID == "" {
			return ErrIntegrationProviderIDRequired
		}
		if p.ExternalTransactionID == "" {
			return ErrIntegrationExternalTransactionIDRequired
		}
	}

	return validateWagerResultBalance(p.ResultBalance, p.ResultBalance)
}

func (e WagerTransactionProcessed) Envelope() IntegrationEventEnvelope {
	return e.envelope
}

func (e WagerTransactionProcessed) TransactionID() string {
	return e.transactionID
}

func (e WagerTransactionProcessed) WalletID() string {
	return e.walletID
}

func (e WagerTransactionProcessed) ProviderID() (string, bool) {
	return e.providerID, e.providerID != ""
}

func (e WagerTransactionProcessed) ExternalTransactionID() (string, bool) {
	return e.externalTransactionID, e.externalTransactionID != ""
}

func (e WagerTransactionProcessed) Kind() WagerKind {
	return e.kind
}

func (e WagerTransactionProcessed) ResultBalance() Money {
	return e.resultBalance
}

type WagerTransactionRejected struct {
	envelope              IntegrationEventEnvelope
	transactionID         string
	walletID              string
	providerID            string
	externalTransactionID string
	kind                  WagerKind
	failureCode           FailureCode
}

type NewWagerTransactionRejectedParams struct {
	EventID       string
	CorrelationID string
	CausationID   string

	TransactionID         string
	WalletID              string
	ProviderID            string
	ExternalTransactionID string
	Kind                  WagerKind
	FailureCode           FailureCode
}

func NewWagerTransactionRejected(
	p NewWagerTransactionRejectedParams,
) (WagerTransactionRejected, error) {
	if err := validateExternalWagerEventIdentity(
		p.TransactionID,
		p.WalletID,
		p.ProviderID,
		p.ExternalTransactionID,
		p.Kind,
	); err != nil {
		return WagerTransactionRejected{}, err
	}

	if p.FailureCode == "" {
		return WagerTransactionRejected{}, ErrFailureCodeRequired
	}

	envelope, err := newIntegrationEventEnvelope(
		p.EventID,
		IntegrationEventTypeWagerTransactionRejected,
		p.TransactionID,
		p.CorrelationID,
		p.CausationID,
	)
	if err != nil {
		return WagerTransactionRejected{}, err
	}

	return WagerTransactionRejected{
		envelope:              envelope,
		transactionID:         p.TransactionID,
		walletID:              p.WalletID,
		providerID:            p.ProviderID,
		externalTransactionID: p.ExternalTransactionID,
		kind:                  p.Kind,
		failureCode:           p.FailureCode,
	}, nil
}

func (e WagerTransactionRejected) Envelope() IntegrationEventEnvelope {
	return e.envelope
}

func (e WagerTransactionRejected) TransactionID() string {
	return e.transactionID
}

func (e WagerTransactionRejected) WalletID() string {
	return e.walletID
}

func (e WagerTransactionRejected) ProviderID() string {
	return e.providerID
}

func (e WagerTransactionRejected) ExternalTransactionID() string {
	return e.externalTransactionID
}

func (e WagerTransactionRejected) Kind() WagerKind {
	return e.kind
}

func (e WagerTransactionRejected) FailureCode() FailureCode {
	return e.failureCode
}

type WagerTransactionPendingReference struct {
	envelope                       IntegrationEventEnvelope
	transactionID                  string
	walletID                       string
	providerID                     string
	externalTransactionID          string
	referenceExternalTransactionID string
	kind                           WagerKind
}

type NewWagerTransactionPendingReferenceParams struct {
	EventID       string
	CorrelationID string
	CausationID   string

	TransactionID                  string
	WalletID                       string
	ProviderID                     string
	ExternalTransactionID          string
	ReferenceExternalTransactionID string
	Kind                           WagerKind
}

func NewWagerTransactionPendingReference(
	p NewWagerTransactionPendingReferenceParams,
) (WagerTransactionPendingReference, error) {
	if err := validateExternalWagerEventIdentity(
		p.TransactionID,
		p.WalletID,
		p.ProviderID,
		p.ExternalTransactionID,
		p.Kind,
	); err != nil {
		return WagerTransactionPendingReference{}, err
	}

	if p.ReferenceExternalTransactionID == "" {
		return WagerTransactionPendingReference{}, ErrMissingReference
	}

	envelope, err := newIntegrationEventEnvelope(
		p.EventID,
		IntegrationEventTypeWagerTransactionPendingReference,
		p.TransactionID,
		p.CorrelationID,
		p.CausationID,
	)
	if err != nil {
		return WagerTransactionPendingReference{}, err
	}

	return WagerTransactionPendingReference{
		envelope:                       envelope,
		transactionID:                  p.TransactionID,
		walletID:                       p.WalletID,
		providerID:                     p.ProviderID,
		externalTransactionID:          p.ExternalTransactionID,
		referenceExternalTransactionID: p.ReferenceExternalTransactionID,
		kind:                           p.Kind,
	}, nil
}

func validateExternalWagerEventIdentity(
	transactionID string,
	walletID string,
	providerID string,
	externalTransactionID string,
	kind WagerKind,
) error {
	if transactionID == "" {
		return ErrIntegrationTransactionIDRequired
	}
	if walletID == "" {
		return ErrIntegrationWalletIDRequired
	}
	if providerID == "" {
		return ErrIntegrationProviderIDRequired
	}
	if externalTransactionID == "" {
		return ErrIntegrationExternalTransactionIDRequired
	}
	if !isExternalWagerKind(kind) {
		return ErrInvalidWagerKind
	}

	return nil
}

func (e WagerTransactionPendingReference) Envelope() IntegrationEventEnvelope {
	return e.envelope
}

func (e WagerTransactionPendingReference) TransactionID() string {
	return e.transactionID
}

func (e WagerTransactionPendingReference) WalletID() string {
	return e.walletID
}

func (e WagerTransactionPendingReference) ProviderID() string {
	return e.providerID
}

func (e WagerTransactionPendingReference) ExternalTransactionID() string {
	return e.externalTransactionID
}

func (e WagerTransactionPendingReference) ReferenceExternalTransactionID() string {
	return e.referenceExternalTransactionID
}

func (e WagerTransactionPendingReference) Kind() WagerKind {
	return e.kind
}
