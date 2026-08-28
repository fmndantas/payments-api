package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/fmndantas/payments/internal/db"
	"github.com/fmndantas/payments/internal/dependencies"
	"github.com/fmndantas/payments/internal/psp"
	"github.com/fmndantas/payments/internal/resilience"
	"github.com/fmndantas/payments/internal/usecases/eventstatus"
)

// Convenience alias; the canonical definition lives in the eventstatus package.
type OutboxStatus = eventstatus.OutboxStatus

var reserveOutboxEventsCommand = `
with selected_outbox_events as (
	select id from outbox
	where status != all($5)
	and locked_until is null
	and lock_token is null 
	and next_try_at < $1
	order by id
	limit $2
	for update skip locked
) 
update outbox
set locked_until = $3, 
    lock_token = $4
from selected_outbox_events
where outbox.id = selected_outbox_events.id;
`

var getOutboxEventsByLockToken = `
select *
from outbox
where outbox.lock_token = $1;
`

var markOutboxEventAsRetryCommand = `
update outbox set 
	next_try_at = $1,
	status = $2,
	attempt_count = attempt_count + 1, 
	last_result = $3,
	last_processed_at = $4,
	locked_until = null,
	lock_token = null
where id = $5 and lock_token = $6;
`

var markOutboxEventAsSuccessCommand = `
update outbox set
	status = $1,
	attempt_count = attempt_count + 1,
	last_result = $2,
	last_processed_at = $3,
	locked_until = null,
	lock_token = null
where id = $4 and lock_token = $5
`

var markPaymentAsSuccessfulCommand = `
update payment set
	id_psp_payment = $1,
	psp_result = $2
where id_internal = $3
`

type DecideNextErrorStatusFn = func(currentAttemptCount int) OutboxStatus
type SendToPsp = resilience.CircuitBreakerHandler[psp.PspInput, psp.PspOutput]

func ProcessOutboxEvents(
	context context.Context,
	tree *dependencies.Tree,
	nowReference time.Time,
	batchSize int,
	lockToken uuid.UUID,
	isPspCircuitOpen func(time.Time) bool,
	sendToPsp SendToPsp,
	decideNextErrorStatus DecideNextErrorStatusFn,
) error {
	if isPspCircuitOpen(nowReference) {
		log.Println("circuit breaker is open; skipping outbox batch")
		return nil
	}

	slog.Info("processing a batch of outbox events", "token", lockToken, "size", batchSize)

	tx, err := tree.DbPool.Begin(context)

	if err != nil {
		return err
	}

	defer tx.Rollback(context)

	tag, err := tx.Exec(
		context,
		reserveOutboxEventsCommand,
		nowReference,
		batchSize,
		nowReference.Add(10*time.Minute),
		lockToken,
		[]OutboxStatus{eventstatus.Errored, eventstatus.Success},
	)

	if err != nil {
		return err
	}

	if err = tx.Commit(context); err != nil {
		return err
	}

	slog.Info("outbox events were reserved", "amount", tag.RowsAffected())

	if tag.RowsAffected() == 0 {
		slog.Info("there's no outbox events for processing. stopping early")
		return nil
	}

	outboxEventsRows, err := tree.DbPool.Query(context, getOutboxEventsByLockToken, lockToken)

	if err != nil {
		return err
	}

	outboxEvents, err := pgx.CollectRows(outboxEventsRows, pgx.RowToStructByName[db.Outbox])

	if err != nil {
		return err
	}

	var (
		numberOfPersistenceErrors = 0
		persistenceErrors         error
		unreservationErrors       error
	)

	slog.Info("processing reserved batch", "amount", len(outboxEvents))

	// TODO: paralellize
	shouldUnreserveBatch := false
	for _, outboxEvent := range outboxEvents {
		slog.Info("processing outbox event", "id", outboxEvent.Id)
		pspOutput := sendToPsp(nowReference, psp.PspInput{Context: context, Outbox: outboxEvent})
		if pspOutput.IsOpen() {
			slog.Error("circuit breaker tripped")
			shouldUnreserveBatch = true
			break
		} else {
			var (
				pspHttp  = pspOutput.RequestResult.HttpResponse
				pspError = pspOutput.RequestResult.Error
			)
			slog.Info(
				"send to psp",
				"status_code", pspHttp.StatusCode,
				"json_body", pspHttp.JsonBody,
			)
			if pspError != nil {
				slog.Info(
					"send to psp",
					"error", pspError.Error(),
				)
			}
			persistenceError := persistEventUpdate(
				context,
				tree,
				outboxEvent,
				pspOutput.RequestResult.HttpResponse,
				pspOutput.RequestResult.Error,
				lockToken,
				nowReference,
				decideNextErrorStatus,
			)
			if persistenceError != nil {
				slog.Error("error when persisting outbox update", "id", outboxEvent.Id, "error", persistenceError.Error())
				numberOfPersistenceErrors++
				persistenceErrors = errors.Join(persistenceError, persistenceErrors)
			} else {
				slog.Info("outbox update was persisted", "id", outboxEvent.Id)
			}
		}
	}

	if shouldUnreserveBatch {
		slog.Info("batch will be unreserved")
		tx, err := tree.DbPool.Begin(context)
		unreservationErrors = errors.Join(unreservationErrors, err)
		tag, err = tx.Exec(
			context,
			"update outbox set lock_token = null, locked_until = null where lock_token = $1",
			lockToken,
		)
		unreservationErrors = errors.Join(unreservationErrors, err)
		if err != nil {
			slog.Error("an error occurred when the batch was unreserved", "error", err.Error())
		}
		if err = tx.Commit(context); err != nil {
			unreservationErrors = errors.Join(unreservationErrors, err)
			tx.Rollback(context)
		}
	}

	return errors.Join(persistenceErrors, unreservationErrors)
}

func EventIsErroredWithFiveAttempts(currentAttemptCount int) OutboxStatus {
	if currentAttemptCount+1 >= 5 {
		return eventstatus.Errored
	}
	return eventstatus.Retry
}

func persistEventUpdate(
	context context.Context,
	tree *dependencies.Tree,
	event db.Outbox,
	pspResponse psp.PspHttpResponse,
	pspError error,
	lockToken uuid.UUID,
	nowReference time.Time,
	decideNextErrorStatus DecideNextErrorStatusFn,
) error {
	if context.Err() != nil {
		return markEventAsErrored(context, tree, event, context.Err(), lockToken, nowReference, decideNextErrorStatus)
	}

	if pspError != nil {
		return markEventAsErrored(context, tree, event, pspError, lockToken, nowReference, decideNextErrorStatus)
	}

	if pspResponse.StatusCode >= 400 {
		var pspErrorPayload PspErrorPayload
		unmarshalError := json.Unmarshal([]byte(pspResponse.JsonBody), &pspErrorPayload)
		if unmarshalError != nil {
			return markEventAsErrored(context, tree, event, unmarshalError, lockToken, nowReference, decideNextErrorStatus)
		}

		return markEventAsErrored(
			context,
			tree,
			event,
			fmt.Errorf(
				"{ \"http_status_code\": %d, \"error\": \"%s\" }",
				pspResponse.StatusCode,
				pspErrorPayload.Error,
			),
			lockToken,
			nowReference,
			decideNextErrorStatus,
		)
	}

	return markEventAsSuccessful(context, tree, pspResponse, event, lockToken, nowReference)
}

func GetNextTryAt(attemptCount int) time.Duration {
	waitTime := time.Duration(0)
	switch attemptCount {
	case 0:
		waitTime = time.Duration(0)
	case 1:
		waitTime = time.Duration(2)
	case 2:
		waitTime = time.Duration(4)
	case 3:
		waitTime = time.Duration(8)
	case 4:
		waitTime = time.Duration(16)
	default:
		waitTime = time.Duration(32)
	}
	return waitTime * time.Second
}

func markEventAsErrored(
	context context.Context,
	tree *dependencies.Tree,
	event db.Outbox,
	eventError error,
	lockToken uuid.UUID,
	nowReference time.Time,
	decideNextErrorStatus DecideNextErrorStatusFn,
) error {
	tx, txErr := tree.DbPool.Begin(context)

	if txErr != nil {
		return txErr
	}

	defer tx.Rollback(context)

	var (
		nextErrorStatus = decideNextErrorStatus(event.AttemptCount)
		nextTryAt       = nowReference.Add(GetNextTryAt(event.AttemptCount))
	)

	slog.Info(
		"marking outbox as an error",
		"id", event.Id,
		"status", nextErrorStatus,
		"next_try_at", nextTryAt,
	)

	tag, txErr := tx.Exec(
		context,
		markOutboxEventAsRetryCommand,
		nextTryAt,
		nextErrorStatus,
		eventError.Error(),
		nowReference,
		event.Id,
		lockToken,
	)

	if txErr != nil {
		return txErr
	}

	if tag.RowsAffected() != 1 {
		tx.Rollback(context)
		return fmt.Errorf("number of rows affected by mark was error was != 1: %d", tag.RowsAffected())
	}

	if txErr = tx.Commit(context); txErr != nil {
		return txErr
	}

	return nil
}

type PspSuccessPayload struct {
	IdPspPayment string `json:"id_psp_payment" binding:"required"`
}

type PspErrorPayload struct {
	Error string `json:"error" binding:"required"`
}

func markEventAsSuccessful(
	context context.Context,
	tree *dependencies.Tree,
	pspHttpResponse psp.PspHttpResponse,
	event db.Outbox,
	lockToken uuid.UUID,
	nowReference time.Time,
) error {
	tx, txErr := tree.DbPool.Begin(context)

	if txErr != nil {
		return txErr
	}

	defer tx.Rollback(context)

	slog.Info(
		"marking outbox as success",
		"id", event.Id,
		"status", eventstatus.Success,
	)

	_, err := tx.Exec(
		context,
		markOutboxEventAsSuccessCommand,
		eventstatus.Success,
		pspHttpResponse.JsonBody,
		nowReference,
		event.Id,
		lockToken,
	)

	if err != nil {
		return err
	}

	var pspSuccessPayload PspSuccessPayload
	err = json.Unmarshal([]byte(pspHttpResponse.JsonBody), &pspSuccessPayload)

	if err != nil {
		return err
	}

	_, err = tx.Exec(
		context,
		markPaymentAsSuccessfulCommand,
		pspSuccessPayload.IdPspPayment,
		pspHttpResponse.JsonBody,
		event.IdInternalPayment,
	)

	if err != nil {
		return err
	}

	return tx.Commit(context)
}
