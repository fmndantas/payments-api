package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	and next_try_at <= $1
	and (locked_until is null or locked_until <= $1)
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
		slog.Info("circuit breaker is open; skipping outbox batch")
		return nil
	}

	slog.Info("processing a batch of outbox events", "token", lockToken, "size", batchSize)

	tx, err := tree.DbPool.Begin(context)

	if err != nil {
		return fmt.Errorf("open transaction: %w", err)
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
		return fmt.Errorf("reserve outbox command: %w", err)
	}

	if err = tx.Commit(context); err != nil {
		return fmt.Errorf("commit reserve outbox command: %w", err)
	}

	slog.Info("outbox events were reserved", "amount", tag.RowsAffected())

	if tag.RowsAffected() == 0 {
		slog.Info("there's no outbox events for processing. stopping early")
		return nil
	}

	outboxEventsRows, err := tree.DbPool.Query(context, getOutboxEventsByLockToken, lockToken)

	if err != nil {
		return fmt.Errorf("get outbox events by lock token: %w", err)
	}

	outboxEvents, err := pgx.CollectRows(outboxEventsRows, pgx.RowToStructByName[db.Outbox])

	if err != nil {
		return fmt.Errorf("collect outbox event rows: %w", err)
	}

	var (
		errs                 error
		shouldUnreserveBatch = false
	)

	handleErr := func(description string, e error) {
		if e == nil {
			return
		}
		errs = errors.Join(errs, fmt.Errorf("%s: %w", description, e))
	}

	slog.Info("processing reserved batch", "amount", len(outboxEvents))

	// TODO: paralellize
	for _, outboxEvent := range outboxEvents {
		slog.Info("processing outbox event", "id", outboxEvent.Id)
		pspOutput := sendToPsp(nowReference, psp.PspInput{Context: context, Outbox: outboxEvent})
		if pspOutput.IsOpen() {
			slog.Error("circuit breaker tripped")
			shouldUnreserveBatch = true
			break
		} else {
			var (
				requestResult = pspOutput.RequestResult
				pspHttp       = requestResult.HttpResponse
				pspError      = requestResult.Error
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
				handleErr(fmt.Sprintf("outbox[Id=%d]", outboxEvent.Id), persistenceError)
			} else {
				slog.Info("outbox update was persisted", "id", outboxEvent.Id)
			}
		}
	}

	if !shouldUnreserveBatch {
		return errs
	}

	slog.Info("batch will be unreserved")

	tx, txErr := tree.DbPool.Begin(context)
	handleErr("open transaction", txErr)

	if txErr != nil {
		return errs
	}

	defer tx.Rollback(context)

	_, unreserveErr := tx.Exec(
		context,
		"update outbox set lock_token = null, locked_until = null where lock_token = $1",
		lockToken,
	)
	handleErr("unreserve", unreserveErr)

	if unreserveErr != nil {
		slog.Error("an error occurred when the batch was unreserved", "error", unreserveErr.Error())
		return errs
	}

	if commitErr := tx.Commit(context); commitErr != nil {
		handleErr("commit unreserve", commitErr)
	}

	return errs
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
		return MarkEventAsErrored(context, tree, event, context.Err(), lockToken, nowReference, decideNextErrorStatus)
	}

	if pspError != nil {
		return MarkEventAsErrored(context, tree, event, pspError, lockToken, nowReference, decideNextErrorStatus)
	}

	if pspResponse.StatusCode >= 400 {
		var pspErrorPayload PspErrorPayload
		unmarshalError := json.Unmarshal([]byte(pspResponse.JsonBody), &pspErrorPayload)
		if unmarshalError != nil {
			return MarkEventAsErrored(context, tree, event, unmarshalError, lockToken, nowReference, decideNextErrorStatus)
		}

		return MarkEventAsErrored(
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

	return MarkEventAsSuccessful(context, tree, pspResponse, event, lockToken, nowReference)
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

func MarkEventAsErrored(
	context context.Context,
	tree *dependencies.Tree,
	event db.Outbox,
	eventError error,
	lockToken uuid.UUID,
	nowReference time.Time,
	decideNextErrorStatus DecideNextErrorStatusFn,
) error {
	if eventError == nil {
		return fmt.Errorf("event %d was marked for error, but the parameter `eventError` is nil", event.Id)
	}

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
		return fmt.Errorf("number of affected rows (outbox table) != 1 (%d)", tag.RowsAffected())
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

func MarkEventAsSuccessful(
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

	tag, err := tx.Exec(
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

	if tag.RowsAffected() != 1 {
		return fmt.Errorf("number of affected rows (outbox table) != 1 (%d)", tag.RowsAffected())
	}

	var pspSuccessPayload PspSuccessPayload
	if err = json.Unmarshal([]byte(pspHttpResponse.JsonBody), &pspSuccessPayload); err != nil {
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
