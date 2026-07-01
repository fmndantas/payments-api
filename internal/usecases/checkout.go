package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/fmndantas/payments/internal/dependencies"
)

var (
	ErrorCheckoutAtLeastOneAccountIsMissing   = errors.New("at least one account is missing")
	ErrorCheckoutIdRequestAlreadyWasProcessed = errors.New("this request was already processed")
	emptyUuid                                 = uuid.UUID{}
)

func HandleCheckout(
	t *dependencies.Tree,
	context context.Context,
	idRequest,
	idSourceAccount,
	idDestinyAccount uuid.UUID,
) (uuid.UUID, error) {
	var foo string
	err := t.DbPool.QueryRow(context, "select id_request from payment where id_request = $1", idRequest).Scan(&foo)

	if err == nil {
		return emptyUuid, ErrorCheckoutIdRequestAlreadyWasProcessed
	}

	accountsRows, err := t.DbPool.Query(
		context,
		"select id_internal, id_external from account where id_external = $1 or id_external = $2",
		idSourceAccount,
		idDestinyAccount,
	)

	if err != nil {
		return emptyUuid, err
	}
	defer accountsRows.Close()

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

	if idInternalSourceAccount == nil || idInternalDestinyAccount == nil {
		return emptyUuid, ErrorCheckoutAtLeastOneAccountIsMissing
	}

	tx, err := t.DbPool.Begin(context)

	if err != nil {
		return emptyUuid, err
	}

	now, idExternalPayment, idInternalPayment := time.Now(), uuid.New(), 0
	err = tx.QueryRow(
		context,
		`insert into payment(id_external, id_request, id_source_account, id_destiny_account, is_pending, created_at)
		values ($1, $2, $3, $4, $5, $6)
		returning id_internal`,
		idExternalPayment, idRequest, idInternalSourceAccount, idInternalDestinyAccount, true, now,
	).Scan(&idInternalPayment)

	if err != nil {
		return emptyUuid, err
	}

	_, err = tx.Exec(
		context,
		`insert into outbox (id_payment, is_pending, next_try_at, created_at)
		values ($1, $2, $3, $4)`,
		idInternalPayment, true, now, now,
	)
	defer tx.Rollback(context)

	if err != nil {
		return emptyUuid, err
	}

	if err := tx.Commit(context); err != nil {
		return emptyUuid, err
	}

	return idExternalPayment, nil
}
