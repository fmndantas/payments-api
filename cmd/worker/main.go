package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"

	"github.com/fmndantas/payments/internal/db"
	"github.com/fmndantas/payments/internal/dependencies"
)

var reserveOutboxEventsCommand = `
with selected_outbox_events as (
	select id from outbox
	where is_pending = true
	and next_try_at <= $1
	and id_target_worker is null
	limit $2
) 
update outbox
set id_target_worker = $3
from selected_outbox_events
where outbox.id = selected_outbox_events.id;
`

var getOutboxEventsByIdWorker = `
select outbox.id,
	   payment.id_external, 
	   outbox.id_payment,
	   outbox.attempt_count
from payment 
join outbox on payment.id_internal = outbox.id_payment
where outbox.id_target_worker = $1;
`

var markOutboxEventAsErroredCommand = `
update outbox set 
	next_try_at = $1,
	attempt_count = attempt_count + 1, 
	last_error = $2,
	id_target_worker = null
where id = $3;
`

var markOutboxEventAsSuccessfulCommand = `
update outbox set
	is_pending = false,	
	processed_at = $1,
	attempt_count = attempt_count + 1,
	id_target_worker = null
where id = $2
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

func processOutboxEvents(
	context context.Context,
	tree *dependencies.Tree,
	now time.Time,
	batchSize int,
	idWorker uuid.UUID,
) error {
	log.Println("processing outbox events")

	tx, err := tree.DbPool.Begin(context)

	if err != nil {
		return err
	}

	tag, err := tx.Exec(context, reserveOutboxEventsCommand, now, batchSize, idWorker)
	defer tx.Rollback(context)

	if err != nil {
		return err
	}

	if err = tx.Commit(context); err != nil {
		return err
	}

	log.Printf("outbox events were successfully reserved. Number of events: %d\n", tag.RowsAffected())

	outboxEventsRows, err := tree.DbPool.Query(context, getOutboxEventsByIdWorker, idWorker)

	if err != nil {
		return err
	}

	outboxEvents := make([]OutboxEvent, 0)
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
	numberOfErrors := 0
	for _, outboxEvent := range outboxEvents {
		pspHttpResponse, sendErr := sendOutboxEventToPsp(context, outboxEvent)
		if persistErr := persistEventUpdate(context, tree, outboxEvent, pspHttpResponse, sendErr); persistErr != nil {
			numberOfErrors++
		}
	}

	log.Printf("processed %d outbox events. number of errors: %d\n", tag.RowsAffected(), numberOfErrors)

	// TODO: makes sense to return always nil? 
	return nil
}

type PspHttpResponse struct {
	HttpStatusCode int
	JsonBody       string
}

// This function simulates the PSP response
func sendOutboxEventToPsp(context context.Context, _ OutboxEvent) (PspHttpResponse, error) {
	if context.Err() != nil {
		return PspHttpResponse{}, context.Err()
	}

	randomHttpStatusCode := rand.IntN(100)

	if randomHttpStatusCode > 75 {
		return PspHttpResponse{
			HttpStatusCode: 500,
			JsonBody:       "{ \"error\": \"the server couldn't process the request\"}",
		}, nil
	} else if randomHttpStatusCode > 50 {
		return PspHttpResponse{
			HttpStatusCode: 429,
			JsonBody:       "{\"error\": \"the server is busy\"}",
		}, nil
	} else if randomHttpStatusCode > 25 {
		return PspHttpResponse{}, errors.New("This is an unexpected error")
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
	pspHttpResponse PspHttpResponse,
	err error,
) error {
	if context.Err() != nil {
		return markEventAsErrored(context, tree, event, context.Err())
	}

	if err != nil {
		return markEventAsErrored(context, tree, event, err)
	}

	if pspHttpResponse.HttpStatusCode >= 400 {
		err := fmt.Errorf(
			"after sent event to psp: %d; body: \"%v\"",
			pspHttpResponse.HttpStatusCode,
			pspHttpResponse.JsonBody,
		)
		return markEventAsErrored(context, tree, event, err)
	}

	return markEventAsSuccessful(context, tree, pspHttpResponse, event)
}

func markEventAsErrored(context context.Context, tree *dependencies.Tree, event OutboxEvent, err error) error {
	tx, txErr := tree.DbPool.Begin(context)
	defer tx.Rollback(context)

	if txErr != nil {
		return txErr
	}

	// FIX: instead of time.Now(), add exponential_backoff to next_try_at
	tag, txErr := tx.Exec(context, markOutboxEventAsErroredCommand, time.Now(), err.Error(), event.IdOutbox)

	if txErr != nil {
		return txErr
	}

	if tag.RowsAffected() != 0 {
		tx.Rollback(context)
		return fmt.Errorf("number of rows affected by mark was error was != 1: %d", tag.RowsAffected())
	}

	if txErr = tx.Commit(context); txErr != nil {
		return txErr
	}

	return nil
}

func markEventAsSuccessful(context context.Context, tree *dependencies.Tree, pspHttpResponse PspHttpResponse, event OutboxEvent) error {
	tx, txErr := tree.DbPool.Begin(context)
	defer tx.Rollback(context)

	if txErr != nil {
		return txErr
	}

	_, err := tx.Exec(context, markOutboxEventAsSuccessfulCommand, time.Now(), event.IdOutbox)

	if err != nil {
		return err
	}

	// FIX: add errored case where id_psp_payment is not present in pspResult.JsonBody

	_, err = tx.Exec(
		context,
		markPaymentAsSuccessfulCommand,
		time.Now(),
		uuid.New(), // FIX: uuid.New() -> value returned by the PSP
		pspHttpResponse.JsonBody,
		event.IdInternalPayment,
	)

	if err != nil {
		return nil
	}

	err = tx.Commit(context)

	if err != nil {
		return err
	}

	return nil
}

// TODO: graceful shutdown?
func main() {
	idWorker := uuid.New()

	log.SetFlags(0)
	log.SetPrefix(fmt.Sprintf("[worker - %s] ", idWorker.String()))

	// FIX: get this from env
	dbConfiguration := db.CreateLocalConfiguration()
	tree, err := dependencies.Initialize(dbConfiguration)

	if err != nil {
		log.Fatalln(err)
		return
	}

	context := context.Background()

	ch := make(chan error)

	go func() {
		for now := range time.Tick(5 * time.Second) {
			ch <- processOutboxEvents(context, tree, now, 10, idWorker)
		}
	}()

	for err := range ch {
		if err != nil {
			log.Println(err)
		}
	}
}
