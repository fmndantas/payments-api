package outbox_test

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fmndantas/payments/internal/db"
	"github.com/fmndantas/payments/internal/dependencies"
	"github.com/fmndantas/payments/internal/psp"
	"github.com/fmndantas/payments/internal/resilience"
	"github.com/fmndantas/payments/internal/usecases/checkout"
	"github.com/fmndantas/payments/internal/usecases/eventstatus"
	"github.com/fmndantas/payments/internal/usecases/outbox"
	"github.com/fmndantas/payments/test"
)

var (
	tree *dependencies.Tree
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	dbLocalConfiguration := db.CreateLocalConfiguration()
	container, err, stopPostgres := test.InitializePostgresTestcontainer(dbLocalConfiguration, ctx)
	if err != nil {
		log.Fatalf("failed to start PostgreSQL container: %s", err)
	}
	host, err := container.Host(ctx)
	if err != nil {
		log.Fatalf("failed to get testcontainer host: %s", err)
	}
	port, err := container.MappedPort(ctx, fmt.Sprintf("%d", dbLocalConfiguration.Port))
	if err != nil {
		log.Fatalf("failed to get testcontainer mapped port: %s", err)
	}
	dbTestConfiguration := db.DbConfiguration{
		Host:     host,
		Port:     int(port.Num()),
		Database: dbLocalConfiguration.Database,
		Username: dbLocalConfiguration.Username,
		Password: dbLocalConfiguration.Password,
	}
	tree, err = dependencies.Initialize(dbTestConfiguration)
	if err != nil {
		log.Fatal(err)
	}
	code := m.Run()
	tree = nil
	stopPostgres()
	os.Exit(code)
}

func sendOutboxEventToPspFakeSuccess(idPspPayment uuid.UUID) (outbox.SendToPsp, func(time.Time) bool) {
	doRequest := func(_ psp.PspInput) psp.PspOutput {
		return psp.PspOutput{HttpResponse: psp.PspHttpResponse{
			StatusCode: 202,
			JsonBody:   fmt.Sprintf("{ \"id_psp_payment\": \"%s\" }", idPspPayment.String()),
		}}
	}
	return resilience.CreateCircuitBreaker(
		rand.IntN(1000),
		doRequest,
		func(_ psp.PspOutput) bool { return false },
	)
}

func sendOutboxEventToPspFakeServerError() (outbox.SendToPsp, func(time.Time) bool) {
	doRequest := func(_ psp.PspInput) psp.PspOutput {
		return psp.PspOutput{HttpResponse: psp.PspHttpResponse{
			StatusCode: 500,
			JsonBody:   "{ \"error\": \"the server couldn't process the request\" }",
		}}
	}
	return resilience.CreateCircuitBreaker(
		rand.IntN(1000),
		doRequest,
		func(_ psp.PspOutput) bool { return false },
	)
}

func eventIsErroredWithTwoAttempts(currentAttemptCount int) outbox.OutboxStatus {
	if currentAttemptCount+1 >= 2 {
		return eventstatus.Errored
	}
	return eventstatus.Retry
}

func eventIsErroredWithOneAttempt(currentAttemptCount int) outbox.OutboxStatus {
	return eventstatus.Errored
}

func resetDbState(context context.Context, tree *dependencies.Tree) error {
	tx, err := tree.DbPool.Begin(context)
	if err != nil {
		return err
	}

	defer tx.Rollback(context)

	_, err = tx.Exec(context, "truncate table payment cascade;")

	if err != nil {
		return err
	}

	if err = tx.Commit(context); err != nil {
		return err
	}

	return nil
}

func TestGetNextTryAt(t *testing.T) {
	cases := []struct {
		idCase         string
		attemptCount   int
		expectedResult time.Duration
	}{
		{"attempt 0", 0, time.Duration(0)},
		{"attempt 1", 1, time.Duration(2)},
		{"attempt 2", 2, time.Duration(4)},
		{"attempt 3", 3, time.Duration(8)},
		{"attempt 4", 4, time.Duration(16)},
		{"attempt 5", 5, time.Duration(32)},
		{"attempt 6", 6, time.Duration(32)},
		{"attempt 7", 7, time.Duration(32)},
		{"attempt 100", 100, time.Duration(32)},
	}
	for _, tt := range cases {
		t.Run(tt.idCase, func(t *testing.T) {
			result := outbox.GetNextTryAt(tt.attemptCount)
			assert.Equal(t, tt.expectedResult*time.Second, result)
		})
	}
}

// status = 'success'
func TestProcessOutboxEventsToSuccess(t *testing.T) {
	// arrange
	var (
		ctx          = context.Background()
		lockToken    = uuid.New()
		now          = time.Now()
		idPspPayment = uuid.New()
	)
	require.NoError(t, resetDbState(ctx, tree))
	_, err := checkout.HandleCheckout(
		tree,
		ctx,
		uuid.New(),
		test.IdSourceAccountAsUuid(),
		test.IdDestinyAccountAsUuid(),
		now,
	)
	require.NoError(t, err)
	// act
	sendToPsp, isOpen := sendOutboxEventToPspFakeSuccess(idPspPayment)
	err = outbox.ProcessOutboxEvents(
		ctx,
		tree,
		now.Add(time.Minute),
		1,
		lockToken,
		isOpen,
		sendToPsp,
		eventIsErroredWithTwoAttempts,
	)
	require.NoError(t, err)
	// assert
	// check outbox rows
	rows, err := tree.DbPool.Query(ctx, "select * from outbox")
	require.NoError(t, err)
	outboxEvents, err := pgx.CollectRows(rows, pgx.RowToStructByName[db.Outbox])
	require.NoError(t, err)
	assert.Equal(t, 1, len(outboxEvents))
	for _, outboxEvent := range outboxEvents {
		assert.Equal(t, eventstatus.Success.String(), outboxEvent.Status, "outbox.status")
		assert.Equal(t, 1, outboxEvent.AttemptCount, "outbox.attempt_count")
		require.Nil(t, outboxEvent.LockToken, "outbox.lock_token")
		require.Nil(t, outboxEvent.LockedUntil, "outbox.locked_until")
	}
	// check payment rows
	rows, err = tree.DbPool.Query(ctx, "select * from payment")
	require.NoError(t, err)
	payments, err := pgx.CollectRows(rows, pgx.RowToStructByName[db.Payment])
	require.NoError(t, err)
	assert.Equal(t, 1, len(payments))
	for _, payment := range payments {
		require.NotNil(t, payment.IdPspPayment, "payment.id_psp_payment")
		assert.Equal(t, idPspPayment.String(), payment.IdPspPayment.String(), "payment.id_psp_payment")
	}
}

// status = 'retry'
func TestProcessOutboxEventsToRetry(t *testing.T) {
	// arrange
	var (
		ctx       = context.Background()
		lockToken = uuid.New()
		now       = time.Now()
	)
	require.NoError(t, resetDbState(ctx, tree))
	_, err := checkout.HandleCheckout(
		tree,
		ctx,
		uuid.New(),
		test.IdSourceAccountAsUuid(),
		test.IdDestinyAccountAsUuid(),
		now,
	)
	require.NoError(t, err)
	// act
	sendToPsp, isOpen := sendOutboxEventToPspFakeServerError()
	err = outbox.ProcessOutboxEvents(
		ctx, tree, now.Add(time.Minute), 1, lockToken, isOpen, sendToPsp, eventIsErroredWithTwoAttempts,
	)
	require.NoError(t, err)
	// assert
	// check outbox rows
	rows, err := tree.DbPool.Query(ctx, "select * from outbox")
	require.NoError(t, err)
	outboxEvents, err := pgx.CollectRows(rows, pgx.RowToStructByName[db.Outbox])
	require.NoError(t, err)
	assert.Equal(t, 1, len(outboxEvents))
	for _, outboxEvent := range outboxEvents {
		assert.Equal(t, eventstatus.Retry.String(), outboxEvent.Status, "outbox.status")
		assert.Equal(t, 1, outboxEvent.AttemptCount, "outbox.attempt_count")
		require.NotNil(t, outboxEvent.LastResult, "outbox.last_result")
		assert.Contains(t, *outboxEvent.LastResult, "500", "outbox.last_result")
		assert.Contains(t, *outboxEvent.LastResult, "the server couldn't process the request", "outbox.last_result")
		assert.Nil(t, outboxEvent.LockToken, "outbox.lock_token")
		assert.Nil(t, outboxEvent.LockedUntil, "outbox.locked_until")
	}
}

// status = 'errored'
func TestProcessOutboxEventsToErrored(t *testing.T) {
	// arrange
	var (
		ctx = context.Background()
		now = time.Now()
	)
	require.NoError(t, resetDbState(ctx, tree))
	_, err := checkout.HandleCheckout(
		tree,
		ctx,
		uuid.New(),
		test.IdSourceAccountAsUuid(),
		test.IdDestinyAccountAsUuid(),
		now,
	)
	require.NoError(t, err)
	// act
	sendToPsp, isOpen := sendOutboxEventToPspFakeServerError()
	firstSendError := outbox.ProcessOutboxEvents(
		ctx, tree, now.Add(time.Minute), 1, uuid.New(), isOpen, sendToPsp, eventIsErroredWithTwoAttempts,
	)
	require.NoError(t, firstSendError, "first send")
	secondSendError := outbox.ProcessOutboxEvents(
		ctx, tree, now.Add(time.Duration(2)*time.Minute), 1, uuid.New(), isOpen, sendToPsp, eventIsErroredWithTwoAttempts,
	)
	require.NoError(t, secondSendError, "second send")
	// assert
	// check outbox rows
	rows, err := tree.DbPool.Query(ctx, "select * from outbox")
	require.NoError(t, err)
	outboxEvents, err := pgx.CollectRows(rows, pgx.RowToStructByName[db.Outbox])
	require.NoError(t, err)
	assert.Equal(t, 1, len(outboxEvents))
	for _, outboxEvent := range outboxEvents {
		assert.Equal(t, eventstatus.Errored.String(), outboxEvent.Status, "outbox.status")
		assert.Equal(t, 2, outboxEvent.AttemptCount, "outbox.attempt_count")
		require.NotNil(t, outboxEvent.LastResult, "outbox.last_result")
		assert.Contains(t, *outboxEvent.LastResult, "500", "outbox.last_result")
		assert.Contains(t, *outboxEvent.LastResult, "the server couldn't process the request", "outbox.last_result")
		assert.Nil(t, outboxEvent.LockToken, "outbox.lock_token")
		assert.Nil(t, outboxEvent.LockedUntil, "outbox.locked_until")
	}
}

// final states are 'success' and 'errored'
func TestProcessOutboxEventsDoesNotGetEventsWithFinalStates(t *testing.T) {
	// arrange
	var (
		ctx = context.Background()
		now = time.Now()
	)
	require.NoError(t, resetDbState(ctx, tree))
	_, err := checkout.HandleCheckout(
		tree,
		ctx,
		uuid.New(),
		test.IdSourceAccountAsUuid(),
		test.IdDestinyAccountAsUuid(),
		now,
	)
	require.NoError(t, err)
	sendToPsp, isOpen := sendOutboxEventToPspFakeSuccess(uuid.New())
	firstSendError := outbox.ProcessOutboxEvents(
		ctx, tree, now.Add(time.Minute), 1, uuid.New(), isOpen, sendToPsp, eventIsErroredWithTwoAttempts,
	)
	require.NoError(t, firstSendError, "first send")
	// act
	for i := range 10 {
		otherSend := outbox.ProcessOutboxEvents(
			ctx, tree, now.Add(time.Duration(i)*time.Minute), 1, uuid.New(), isOpen, sendToPsp, eventIsErroredWithTwoAttempts,
		)
		require.NoError(t, otherSend, fmt.Sprintf("other send %d", i))
	}
	// assert
	rows, err := tree.DbPool.Query(ctx, "select * from outbox")
	require.NoError(t, err)
	outboxEvents, err := pgx.CollectRows(rows, pgx.RowToStructByName[db.Outbox])
	require.NoError(t, err)
	assert.Equal(t, 1, len(outboxEvents))
	outboxEvent := outboxEvents[0]
	assert.Equal(t, eventstatus.Success.String(), outboxEvent.Status, "outbox.status")
	assert.Equal(t, 1, outboxEvent.AttemptCount, "outbox.attempt_count")
}

func TestProcessOutboxEventsGetEventsWithExpiredLock(t *testing.T) {
	// arrange
	var (
		ctx = context.Background()
		now = time.Now()
	)
	require.NoError(t, resetDbState(ctx, tree))
	_, err := checkout.HandleCheckout(
		tree,
		ctx,
		uuid.New(),
		test.IdSourceAccountAsUuid(),
		test.IdDestinyAccountAsUuid(),
		now,
	)
	require.NoError(t, err)
	tx, err := tree.DbPool.Begin(ctx)
	require.NoError(t, err)
	// save event in a reserved state, simulating a scenario where a previous processing failed to unreserve the event
	lockToken := uuid.New()
	_, err = tx.Exec(ctx, "update outbox set locked_until = $1, lock_token = $2 where true", now, lockToken)
	require.NoError(t, err)
	err = tx.Commit(ctx)
	require.NoError(t, err)
	sendToPsp, isOpen := sendOutboxEventToPspFakeSuccess(uuid.New())
	// act
	err = outbox.ProcessOutboxEvents(ctx, tree, now.Add(time.Minute), 1, lockToken, isOpen, sendToPsp, eventIsErroredWithTwoAttempts)
	require.NoError(t, err)
	// assert
	rows, err := tree.DbPool.Query(ctx, "select * from outbox")
	require.NoError(t, err)
	outboxEvents, err := pgx.CollectRows(rows, pgx.RowToStructByName[db.Outbox])
	require.NoError(t, err)
	assert.Equal(t, 1, len(outboxEvents))
	outboxEvent := outboxEvents[0]
	assert.Equal(t, 1, outboxEvent.AttemptCount, "outbox.attempt_count")
	assert.Equal(t, eventstatus.Success.String(), outboxEvent.Status, "outbox.status")
}

func TestProcessOutboxEventsWithCircuitBreaker(t *testing.T) {
	// arrange
	var (
		ctx = context.Background()
		now = time.Now()
	)
	require.NoError(t, resetDbState(ctx, tree))
	for range 100 {
		_, err := checkout.HandleCheckout(
			tree, ctx, uuid.New(), test.IdSourceAccountAsUuid(), test.IdDestinyAccountAsUuid(), now,
		)
		require.NoError(t, err, "checkout")
	}
	doRequest := func(_ psp.PspInput) psp.PspOutput {
		return psp.PspOutput{HttpResponse: psp.PspHttpResponse{
			StatusCode: 500,
			JsonBody:   "{ \"error\": \"the server couldn't process the request\" }",
		}}
	}
	sendToPsp, isOpen := resilience.CreateCircuitBreaker(
		25,
		doRequest,
		func(_ psp.PspOutput) bool { return true },
	)
	// act
	err := outbox.ProcessOutboxEvents(
		ctx,
		tree,
		now.Add(time.Minute),
		50,
		uuid.New(),
		isOpen,
		sendToPsp,
		eventIsErroredWithOneAttempt,
	)
	// assert
	require.NoError(t, err)
	rows, err := tree.DbPool.Query(ctx, "select * from outbox")
	require.NoError(t, err)
	outboxEvents, err := pgx.CollectRows(rows, pgx.RowToStructByName[db.Outbox])
	require.NoError(t, err)
	assert.Equal(t, 100, len(outboxEvents))
	var (
		unprocessedCount = 0
		erroredCount     = 0
	)
	for _, outboxEvent := range outboxEvents {
		require.Condition(t, func() bool {
			return outboxEvent.Status == eventstatus.Unprocessed.String() || outboxEvent.Status == eventstatus.Errored.String()
		}, "status should be unprocessed or errored")
		if outboxEvent.Status == eventstatus.Unprocessed.String() {
			unprocessedCount++
		} else {
			erroredCount++
		}
	}
	assert.Equal(t, 25, erroredCount, "errored count")
	assert.Equal(t, 75, unprocessedCount, "unprocessed count")
	for _, outboxEvent := range outboxEvents {
		assert.Nil(t, outboxEvent.LockToken, "outbox.lock_token")
		assert.Nil(t, outboxEvent.LockedUntil, "outbox.locked_until")
	}
}

func TestProcessOutboxEvents4Workers(t *testing.T) {
	t.Skip("Expensive test")
	// arrange
	var (
		N               = 10000
		numberOfWorkers = 10
		ctx             = context.Background()
		now             = time.Now()
	)
	require.NoError(t, resetDbState(ctx, tree))
	for range N {
		_, err := checkout.HandleCheckout(
			tree,
			ctx,
			uuid.New(),
			test.IdSourceAccountAsUuid(),
			test.IdDestinyAccountAsUuid(),
			now,
		)
		if err != nil {
			t.Fatalf("%s", err.Error())
		}
	}
	// act
	var wg sync.WaitGroup
	for range numberOfWorkers {
		wg.Go(func() {
			sendToPsp, isOpen := sendOutboxEventToPspFakeSuccess(uuid.New())
			outbox.ProcessOutboxEvents(
				ctx,
				tree,
				time.Now(),
				N/numberOfWorkers,
				uuid.New(),
				isOpen,
				sendToPsp,
				eventIsErroredWithTwoAttempts,
			)
		})
	}
	wg.Wait()
	// assert
	// check outbox rows
	rows, err := tree.DbPool.Query(ctx, "select * from outbox order by id")
	require.NoError(t, err)
	outboxEvents, err := pgx.CollectRows(rows, pgx.RowToStructByName[db.Outbox])
	require.NoError(t, err)
	assert.Equal(t, N, len(outboxEvents))
	for _, outboxEvent := range outboxEvents {
		assert.Equal(t, eventstatus.Success.String(), outboxEvent.Status, "outbox.status")
	}
}
