package checkout

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/fmndantas/payments/internal/dependencies"
	"github.com/fmndantas/payments/internal/usecases/eventstatus"
)

var (
	ErrorCheckoutAtLeastOneAccountIsMissing   = errors.New("at least one account is missing")
	ErrorCheckoutIdRequestAlreadyWasProcessed = errors.New("this request was already processed")
)

type HandleCheckoutResult struct {
	IdExternalPayment         uuid.UUID
	IdRequestAlreadyProcessed bool
}

func HandleCheckout(
	t *dependencies.Tree,
	ctx context.Context,
	idRequest,
	idSourceAccount,
	idDestinyAccount uuid.UUID,
	nowReference time.Time,
) (HandleCheckoutResult, error) {
	accountsRows, err := t.DbPool.Query(
		ctx,
		"select id_internal, id_external from account where id_external = $1 or id_external = $2",
		idSourceAccount,
		idDestinyAccount,
	)

	handleCheckoutResultNil := HandleCheckoutResult{}

	if err != nil {
		return handleCheckoutResultNil, err
	}

	var idInternalSourceAccount, idInternalDestinyAccount *int
	for accountsRows.Next() {
		var (
			idInternal int
			idExternal string
		)
		accountsRows.Scan(&idInternal, &idExternal)
		if idExternal == idSourceAccount.String() {
			idInternalSourceAccount = &idInternal
		}
		if idExternal == idDestinyAccount.String() {
			idInternalDestinyAccount = &idInternal
		}
		if idInternalSourceAccount != nil && idInternalDestinyAccount != nil {
			break
		}
	}
	
	accountsRows.Close()

	if idInternalSourceAccount == nil || idInternalDestinyAccount == nil {
		return handleCheckoutResultNil, ErrorCheckoutAtLeastOneAccountIsMissing
	}

	idExternalPayment, idInternalPayment := uuid.New(), 0
	createPaymentErr := t.DbPool.QueryRow(
		ctx,
		`insert into payment(id_external, id_request, id_source_account, id_destiny_account, created_at)
		values ($1, $2, $3, $4, $5)
		on conflict (id_request) do nothing
		returning id_internal`,
		idExternalPayment, idRequest, idInternalSourceAccount, idInternalDestinyAccount, nowReference,
	).Scan(&idInternalPayment)

	if createPaymentErr != nil && !errors.Is(createPaymentErr, pgx.ErrNoRows) {
		return handleCheckoutResultNil, createPaymentErr
	}

	if createPaymentErr != nil && errors.Is(createPaymentErr, pgx.ErrNoRows) {
		// The payment wasn't inserted due to a `id_request` conflict
		checkAccountsErr := t.DbPool.QueryRow(
			ctx,
			`select id_external from payment where id_request = $1 and id_source_account = $2 and id_destiny_account = $3`,
			idRequest, idInternalSourceAccount, idInternalDestinyAccount,
		).Scan(&idExternalPayment)

		switch {
		case checkAccountsErr == nil:
			return HandleCheckoutResult{IdExternalPayment: idExternalPayment, IdRequestAlreadyProcessed: true}, nil
		case errors.Is(checkAccountsErr, pgx.ErrNoRows):
			return handleCheckoutResultNil, ErrorCheckoutIdRequestAlreadyWasProcessed
		default:
			return handleCheckoutResultNil, err
		}
	}

	tx, err := t.DbPool.Begin(ctx)

	if err != nil {
		return handleCheckoutResultNil, err
	}

	defer tx.Rollback(ctx)

	_, createOutboxErr := tx.Exec(
		ctx,
		`insert into outbox (id_payment, status, next_try_at, created_at)
		values ($1, $2, $3, $4)`,
		idInternalPayment, eventstatus.Unprocessed, nowReference, nowReference,
	)

	if createOutboxErr != nil {
		return handleCheckoutResultNil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return handleCheckoutResultNil, err
	}

	return HandleCheckoutResult{
		IdExternalPayment:         idExternalPayment,
		IdRequestAlreadyProcessed: false,
	}, nil
}
