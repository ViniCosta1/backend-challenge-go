package domain

import "errors"

var (
	ErrReferenceMismatch     = errors.New("reference business fields do not match")
	ErrReferenceIncompatible = errors.New("reference is not usable for this reversal")
	ErrReferencePending      = errors.New("reference is not terminal yet")
)

// ValidateWinReference validates the optional BET relationship of a WIN.
// Unlike a reversal, the WIN amount is independent from the referenced BET.
func (w *WagerTransaction) ValidateWinReference(reference *WagerTransaction) error {
	if reference == nil || w.kind != WagerKindWin || w.referenceExternalTransactionID == "" {
		return ErrReferenceIncompatible
	}
	if w.providerID != reference.providerID || w.playerID != reference.playerID ||
		w.walletID != reference.walletID || w.roundID != reference.roundID ||
		w.money.currency != reference.money.currency ||
		w.referenceExternalTransactionID != reference.externalTransactionID {
		return ErrReferenceMismatch
	}
	if reference.kind != WagerKindBet {
		return ErrReferenceIncompatible
	}
	if !reference.IsTerminal() {
		return ErrReferencePending
	}
	if reference.status != WagerStatusProcessed {
		return ErrReferenceIncompatible
	}
	return nil
}

// ReversalDirection validates the complete financial relationship. The caller
// resolves the external identity and coordinates persistence and wallet locks.
func (w *WagerTransaction) ReversalDirection(reference *WagerTransaction) (LedgerDirection, error) {
	if reference == nil || (w.kind != WagerKindRefund && w.kind != WagerKindRollback) {
		return "", ErrReferenceIncompatible
	}
	comparison, err := w.money.Compare(reference.money)
	if err != nil || comparison != 0 || w.providerID != reference.providerID ||
		w.playerID != reference.playerID || w.walletID != reference.walletID ||
		w.roundID != reference.roundID ||
		w.referenceExternalTransactionID != reference.externalTransactionID {
		return "", ErrReferenceMismatch
	}
	if (w.kind == WagerKindRefund && reference.kind != WagerKindBet) ||
		(w.kind == WagerKindRollback && reference.kind != WagerKindBet &&
			reference.kind != WagerKindWin && reference.kind != WagerKindRefund) {
		return "", ErrReferenceIncompatible
	}
	if !reference.IsTerminal() {
		return "", ErrReferencePending
	}
	if reference.status != WagerStatusProcessed {
		return "", ErrReferenceIncompatible
	}
	if reference.kind == WagerKindBet {
		return LedgerDirectionCredit, nil
	}
	return LedgerDirectionDebit, nil
}
