package postgres

import (
	"testing"

	"github.com/google/uuid"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

func TestLedgerSchemaIsAppendOnlyAndEnforcesFinancialConstraints(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	walletID, playerID := createFundedWallet(t, ctx, pool, 10000)
	var ledgerID, transactionID string
	var direction string
	var amount, before, after int64
	if err := pool.QueryRow(ctx, `SELECT id::text, transaction_id::text, direction, money_amount,
        balance_before, balance_after FROM wallet_ledger_entries WHERE wallet_id=$1`, walletID).
		Scan(&ledgerID, &transactionID, &direction, &amount, &before, &after); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE wallet_ledger_entries SET money_amount=1 WHERE id=$1", ledgerID); err == nil {
		t.Fatal("PostgreSQL allowed UPDATE of append-only ledger")
	}
	if _, err := pool.Exec(ctx, "DELETE FROM wallet_ledger_entries WHERE id=$1", ledgerID); err == nil {
		t.Fatal("PostgreSQL allowed DELETE of append-only ledger")
	}
	var gotDirection string
	var gotAmount, gotBefore, gotAfter int64
	if err := pool.QueryRow(ctx, `SELECT direction, money_amount, balance_before, balance_after
        FROM wallet_ledger_entries WHERE id=$1`, ledgerID).
		Scan(&gotDirection, &gotAmount, &gotBefore, &gotAfter); err != nil {
		t.Fatal(err)
	}
	if gotDirection != direction || gotAmount != amount || gotBefore != before || gotAfter != after {
		t.Fatal("failed mutations changed the original ledger entry")
	}

	loss := executeWager(t, ctx, pool, newWagerInput(t, walletID, playerID, domain.WagerKindLoss, 0))
	invalidEntries := []struct {
		name                        string
		direction                   string
		money, balanceBefore, after int64
	}{
		{"invalid direction", "SIDEWAYS", 100, 100, 200},
		{"zero movement", "CREDIT", 0, 100, 100},
		{"inconsistent credit", "CREDIT", 100, 100, 150},
		{"negative balance", "CREDIT", 100, -1, 99},
	}
	for _, test := range invalidEntries {
		t.Run(test.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, `INSERT INTO wallet_ledger_entries
                (id, wallet_id, transaction_id, direction, money_amount, currency,
                 balance_before, balance_after, created_at)
                VALUES ($1,$2,$3,$4,$5,'BRL',$6,$7,CURRENT_TIMESTAMP)`,
				uuid.NewString(), walletID, loss.TransactionID, test.direction,
				test.money, test.balanceBefore, test.after)
			if err == nil {
				t.Fatal("PostgreSQL accepted an invalid financial ledger entry")
			}
		})
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallet_ledger_entries
        (id, wallet_id, transaction_id, direction, money_amount, currency,
         balance_before, balance_after, created_at)
        VALUES ($1,$2,$3,'CREDIT',10000,'BRL',0,10000,CURRENT_TIMESTAMP)`,
		uuid.NewString(), walletID, transactionID); err == nil {
		t.Fatal("PostgreSQL accepted duplicate wallet/transaction ledger identity")
	}
	assertCount(t, ctx, pool, 0, "SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id=$1", loss.TransactionID)
}

func TestAllMigrationsUpDownUpOnCleanSchema(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	for _, migration := range []string{
		"000003_pending_reference_state.down.sql",
		"000002_wager_result_balance.down.sql",
		"000001_init.down.sql",
	} {
		applyTestMigration(t, ctx, pool, migration)
	}
	var tableCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables
        WHERE table_schema=current_schema() AND table_name IN
        ('wallets','wager_transactions','wallet_ledger_entries','inbox_messages','outbox_events')`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 0 {
		t.Fatalf("full DOWN left %d application tables", tableCount)
	}
	for _, migration := range []string{
		"000001_init.up.sql",
		"000002_wager_result_balance.up.sql",
		"000003_pending_reference_state.up.sql",
	} {
		applyTestMigration(t, ctx, pool, migration)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables
        WHERE table_schema=current_schema() AND table_name IN
        ('wallets','wager_transactions','wallet_ledger_entries','inbox_messages','outbox_events')`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 5 {
		t.Fatalf("second UP recreated %d/5 application tables", tableCount)
	}
	assertCount(t, ctx, pool, 1, `SELECT count(*) FROM pg_trigger
		WHERE tgname='trg_wallet_ledger_entries_append_only'
		  AND tgrelid='wallet_ledger_entries'::regclass
		  AND NOT tgisinternal`)
	if _, err := pool.Exec(ctx, `INSERT INTO wallets
        (id,player_id,currency,balance_amount,version,created_at,updated_at)
        VALUES ($1,$2,'BRL',-1,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, uuid.NewString(), uuid.NewString()); err == nil {
		t.Fatal("recreated schema accepted a negative wallet balance")
	}
}
