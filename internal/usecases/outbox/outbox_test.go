package outbox_test

import (
	"context"
	"fmt"
	"log"
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
	"github.com/fmndantas/payments/internal/usecases/checkout"
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

func sendOutboxEventToPspFakeSuccess(idPspPayment uuid.UUID) outbox.SendEventToPspFn {
	return func(context context.Context, _ db.Outbox) (outbox.PspHttpResponse, error) {
		return outbox.PspHttpResponse{
			HttpStatusCode: 202,
			JsonBody:       fmt.Sprintf("{ \"id_psp_payment\": \"%s\" }", idPspPayment.String()),
		}, nil
	}
}

func sendOutboxEventToPspFakeError() outbox.SendEventToPspFn {
	return func(context context.Context, _ db.Outbox) (outbox.PspHttpResponse, error) {
		return outbox.PspHttpResponse{
			HttpStatusCode: 500,
			JsonBody:       "{ \"error\": \"the server couldn't process the request\" }",
		}, nil
	}
}

func eventIsErroredWithTwoAttempts(currentAttemptCount int) string {
	if currentAttemptCount+1 >= 2 {
		return outbox.ERRORED
	}
	return outbox.RETRY
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
	err = outbox.ProcessOutboxEvents(
		ctx, tree, now.Add(time.Minute), 1, lockToken, sendOutboxEventToPspFakeSuccess(idPspPayment), eventIsErroredWithTwoAttempts,
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
		assert.Equal(t, outbox.SUCCESS, outboxEvent.Status, "outbox.status")
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
	err = outbox.ProcessOutboxEvents(
		ctx, tree, now.Add(time.Minute), 1, lockToken, sendOutboxEventToPspFakeError(), eventIsErroredWithTwoAttempts,
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
		assert.Equal(t, outbox.RETRY, outboxEvent.Status, "outbox.status")
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
	firstSendError := outbox.ProcessOutboxEvents(
		ctx, tree, now.Add(time.Minute), 1, uuid.New(), sendOutboxEventToPspFakeError(), eventIsErroredWithTwoAttempts,
	)
	require.NoError(t, firstSendError, "first send")

	secondSendError := outbox.ProcessOutboxEvents(
		ctx, tree, now.Add(time.Duration(2)*time.Minute), 1, uuid.New(), sendOutboxEventToPspFakeError(), eventIsErroredWithTwoAttempts,
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
		assert.Equal(t, outbox.ERRORED, outboxEvent.Status, "outbox.status")
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
	firstSendError := outbox.ProcessOutboxEvents(
		ctx, tree, now.Add(time.Minute), 1, uuid.New(), sendOutboxEventToPspFakeSuccess(uuid.New()), eventIsErroredWithTwoAttempts,
	)
	require.NoError(t, firstSendError, "first send")
	// act
	for i := range 10 {
		otherSend := outbox.ProcessOutboxEvents(
			ctx, tree, now.Add(time.Duration(i)*time.Minute), 1, uuid.New(), sendOutboxEventToPspFakeSuccess(uuid.New()), eventIsErroredWithTwoAttempts,
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
	assert.Equal(t, outbox.SUCCESS, outboxEvent.Status, "outbox.status")
	assert.Equal(t, 1, outboxEvent.AttemptCount, "outbox.attempt_count")
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
			outbox.ProcessOutboxEvents(ctx, tree, time.Now(), N/numberOfWorkers, uuid.New(), sendOutboxEventToPspFakeSuccess(uuid.New()), eventIsErroredWithTwoAttempts)
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
		assert.Equal(t, outbox.SUCCESS, outboxEvent.Status, "outbox.status")
	}
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
