package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/fmndantas/payments/internal/db"
	"github.com/fmndantas/payments/internal/dependencies"
)

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

type SendEventToPspFn = func(context.Context, db.Outbox) (PspHttpResponse, error)
type DecideNextErrorStatusFn = func(currentAttemptCount int) string

var (
	UNPROCESSED = "unprocessed"
	RETRY       = "retry"
	ERRORED     = "errored"
	SUCCESS     = "success"
)

func ProcessOutboxEvents(
	context context.Context,
	tree *dependencies.Tree,
	nowReference time.Time,
	batchSize int,
	lockToken uuid.UUID,
	sendEventToPsp SendEventToPspFn,
	decideNextErrorStatus DecideNextErrorStatusFn,
) error {
	log.Println("processing outbox events")

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
		[]string{ERRORED, SUCCESS},
	)

	if err != nil {
		return err
	}

	if err = tx.Commit(context); err != nil {
		return err
	}

	log.Printf("outbox events were successfully reserved. Number of events: %d\n", tag.RowsAffected())

	outboxEventsRows, err := tree.DbPool.Query(context, getOutboxEventsByLockToken, lockToken)

	if err != nil {
		return err
	}

	outboxEvents, err := pgx.CollectRows(outboxEventsRows, pgx.RowToStructByName[db.Outbox])

	if err != nil {
		return err
	}

	// TODO: paralellize
	var (
		numberOfErrors  = 0
		aggregatedError error
	)

	for _, outboxEvent := range outboxEvents {
		pspResponse, pspError := sendEventToPsp(context, outboxEvent)
		if persistError := persistEventUpdate(
			context, tree, outboxEvent, pspResponse, pspError, lockToken, nowReference, decideNextErrorStatus,
		); persistError != nil {
			numberOfErrors++
			aggregatedError = errors.Join(persistError, aggregatedError)
		}
	}

	log.Printf("processed %d outbox events. number of errors: %d\n", tag.RowsAffected(), numberOfErrors)

	return aggregatedError
}

type PspHttpResponse struct {
	HttpStatusCode int
	JsonBody       string
}

// This function simulates the PSP response
func SendOutboxEventToPspFake(context context.Context, _ db.Outbox) (PspHttpResponse, error) {
	if context.Err() != nil {
		return PspHttpResponse{}, context.Err()
	}

	randomHttpStatusCode := rand.IntN(100)

	if randomHttpStatusCode > 75 {
		return PspHttpResponse{
			HttpStatusCode: 500,
			JsonBody:       "{ \"error\": \"the server couldn't process the request\" }",
		}, nil
	} else if randomHttpStatusCode > 50 {
		return PspHttpResponse{
			HttpStatusCode: 429,
			JsonBody:       "{ \"error\": \"the server is busy\" }",
		}, nil
	} else if randomHttpStatusCode > 25 {
		return PspHttpResponse{}, errors.New("this is an unexpected error")
	} else {
		return PspHttpResponse{
			HttpStatusCode: 202,
			JsonBody:       fmt.Sprintf("{ \"id_psp_payment\": \"%s\" }", uuid.New().String()),
		}, nil
	}
}

func EventIsErroredWithFiveAttempts(currentAttemptCount int) string {
	if currentAttemptCount+1 >= 5 {
		return ERRORED
	}
	return RETRY
}

func persistEventUpdate(
	context context.Context,
	tree *dependencies.Tree,
	event db.Outbox,
	pspResponse PspHttpResponse,
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

	if pspResponse.HttpStatusCode >= 400 {
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
				pspResponse.HttpStatusCode,
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

	tag, txErr := tx.Exec(
		context,
		markOutboxEventAsRetryCommand,
		nowReference.Add(GetNextTryAt(event.AttemptCount)),
		decideNextErrorStatus(event.AttemptCount),
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
	pspHttpResponse PspHttpResponse,
	event db.Outbox,
	lockToken uuid.UUID,
	nowReference time.Time,
) error {
	tx, txErr := tree.DbPool.Begin(context)

	if txErr != nil {
		return txErr
	}

	defer tx.Rollback(context)

	_, err := tx.Exec(
		context,
		markOutboxEventAsSuccessCommand,
		SUCCESS,
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
