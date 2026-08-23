package usecases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"

	"github.com/fmndantas/payments/internal/dependencies"
)

var reserveOutboxEventsCommand = `
with selected_outbox_events as (
	select id from outbox
	where is_pending = true
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
select outbox.id,
	   payment.id_external, 
	   outbox.id_payment,
	   outbox.attempt_count
from payment 
join outbox on payment.id_internal = outbox.id_payment
where outbox.lock_token = $1;
`

var markOutboxEventAsErroredCommand = `
update outbox set 
	next_try_at = $1,
	attempt_count = attempt_count + 1, 
	last_error = $2,
	locked_until = null,
	lock_token = null
where id = $3 and lock_token = $4;
`

var markOutboxEventAsSuccessfulCommand = `
update outbox set
	is_pending = false,	
	processed_at = $1,
	attempt_count = attempt_count + 1,
	locked_until = null,
	lock_token = null
where id = $2 and lock_token = $3
`

var markPaymentAsSuccessfulCommand = `
update payment set
	is_pending = false,
	processed_at = $1,
	id_psp_payment = $2,
	psp_result = $3
where id_internal = $4
`

type OutboxEvent struct {
	IdOutbox          int64
	IdInternalPayment int64
	IdExternalPayment uuid.UUID
	AttemptCount      int
}

type OutboxEventUpdate struct {
	Event     OutboxEvent
	NextTryAt time.Time
}

type SendEventToPspFn = func(context.Context, OutboxEvent) (PspHttpResponse, error)

func ProcessOutboxEvents(
	context context.Context,
	tree *dependencies.Tree,
	nowReference time.Time,
	batchSize int,
	lockToken uuid.UUID,
	sendEventToPspFn SendEventToPspFn,
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

	outboxEvents := make([]OutboxEvent, 0, batchSize)
	for outboxEventsRows.Next() {
		var event OutboxEvent
		if err := outboxEventsRows.Scan(&event.IdOutbox, &event.IdExternalPayment, &event.IdInternalPayment, &event.AttemptCount); err != nil {
			return fmt.Errorf("scan outbox event: %w", err)
		}
		outboxEvents = append(outboxEvents, event)
	}

	outboxEventsRows.Close()
	if err := outboxEventsRows.Err(); err != nil {
		return fmt.Errorf("read outbox events: %w", err)
	}

	// TODO: paralellize
	var (
		numberOfErrors  = 0
		aggregatedError error
	)

	for _, outboxEvent := range outboxEvents {
		pspResponse, pspError := sendEventToPspFn(context, outboxEvent)
		if persistError := persistEventUpdate(context, tree, outboxEvent, pspResponse, pspError, lockToken, nowReference); persistError != nil {
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
func SendOutboxEventToPspFake(context context.Context, _ OutboxEvent) (PspHttpResponse, error) {
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

func persistEventUpdate(
	context context.Context,
	tree *dependencies.Tree,
	event OutboxEvent,
	pspResponse PspHttpResponse,
	pspError error,
	lockToken uuid.UUID,
	nowReference time.Time,
) error {
	if context.Err() != nil {
		return markEventAsErrored(context, tree, event, context.Err(), lockToken, nowReference)
	}

	if pspError != nil {
		return markEventAsErrored(context, tree, event, pspError, lockToken, nowReference)
	}

	if pspResponse.HttpStatusCode >= 400 {
		var pspErrorPayload PspErrorPayload
		unmarshalError := json.Unmarshal([]byte(pspResponse.JsonBody), &pspErrorPayload)
		if unmarshalError != nil {
			return markEventAsErrored(context, tree, event, unmarshalError, lockToken, nowReference)
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
	event OutboxEvent,
	eventError error,
	lockToken uuid.UUID,
	nowReference time.Time,
) error {
	tx, txErr := tree.DbPool.Begin(context)
	if txErr != nil {
		return txErr
	}

	defer tx.Rollback(context)

	tag, txErr := tx.Exec(
		context,
		markOutboxEventAsErroredCommand,
		nowReference.Add(GetNextTryAt(event.AttemptCount)),
		eventError.Error(),
		event.IdOutbox,
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
	event OutboxEvent,
	lockToken uuid.UUID,
	nowReference time.Time,
) error {
	tx, txErr := tree.DbPool.Begin(context)

	if txErr != nil {
		return txErr
	}

	defer tx.Rollback(context)

	_, err := tx.Exec(context, markOutboxEventAsSuccessfulCommand, nowReference, event.IdOutbox, lockToken)

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
		time.Now(),
		pspSuccessPayload.IdPspPayment,
		pspHttpResponse.JsonBody,
		event.IdInternalPayment,
	)

	if err != nil {
		return err
	}

	return tx.Commit(context)
}
