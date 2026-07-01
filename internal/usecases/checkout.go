package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/fmndantas/payments/internal/dependencies"
)

func HandleCheckout(
	t *dependencies.Tree,
	context context.Context,
	idRequest,
	idSourceAccount,
	idDestinyAccount uuid.UUID,
) (uuid.UUID, error) {
	accountsRows, err := t.DbPool.Query(
		context,
		"select id_internal, id_external from account where id_external = $1 or id_external = $2",
		idSourceAccount,
		idDestinyAccount,
	)
	if err != nil {
		return uuid.UUID{}, err
	}
	defer accountsRows.Close()
	var idInternalSourceAccount, idInternalDestinyAccount int
	for accountsRows.Next() {
		var idInternal int
		var idExternal string
		accountsRows.Scan(&idInternal, &idExternal)
		// TODO: improve .String()?
		if idExternal == idSourceAccount.String() {
			idInternalSourceAccount = idInternal
		}
		// TODO: improve .String()?
		if idExternal == idDestinyAccount.String() {
			idInternalDestinyAccount = idInternal
		}
	}
	if idInternalSourceAccount == 0 || idInternalDestinyAccount == 0 {
		return uuid.UUID{}, errors.New("at least one account is missing")
	}
	idExternalPayment, idInternalPayment := uuid.New(), 0
	tx, err := t.DbPool.Begin(context)
	if err != nil {
		return uuid.UUID{}, err
	}
	now := time.Now()
	_ = tx.QueryRow(
		context,
		`insert into payment(id_external, id_request, id_source_account, id_destiny_account, is_pending, created_at)
		values ($1, $2, $3, $4, $5, $6)
		returning id_internal`,
		idExternalPayment, idRequest, idInternalSourceAccount, idInternalDestinyAccount, true, now,
	).Scan(&idInternalPayment)
	_, err = tx.Exec(
		context,
		`insert into outbox (id_payment, is_pending, next_try_at, created_at)
		values ($1, $2, $3, $4)`,
		idInternalPayment, true, now, now,
	)
	defer tx.Rollback(context)
	if err != nil {
		return uuid.UUID{}, err
	}
	if err := tx.Commit(context); err != nil {
		return uuid.UUID{}, err
	}
	return idExternalPayment, nil
}
