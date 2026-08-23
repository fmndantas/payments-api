package usecases_test

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
	"github.com/fmndantas/payments/internal/usecases"
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

func sendOutboxEventToPspFakeSuccess(idPspPayment uuid.UUID) usecases.SendEventToPspFn {
	return func(context context.Context, _ usecases.OutboxEvent) (usecases.PspHttpResponse, error) {
		return usecases.PspHttpResponse{
			HttpStatusCode: 202,
			JsonBody:       fmt.Sprintf("{ \"id_psp_payment\": \"%s\" }", idPspPayment.String()),
		}, nil
	}
}

func sendOutboxEventToPspFakeError() usecases.SendEventToPspFn {
	return func(context context.Context, _ usecases.OutboxEvent) (usecases.PspHttpResponse, error) {
		return usecases.PspHttpResponse{
			HttpStatusCode: 500,
			JsonBody:       "{ \"error\": \"the server couldn't process the request\" }",
		}, nil
	}
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

// Every payment will be processed without errors
func TestProcessOutboxEventsSuccess(t *testing.T) {
	// arrange
	ctx := context.Background()
	require.NoError(t, resetDbState(ctx, tree))
	_, err := usecases.HandleCheckout(
		tree,
		ctx,
		uuid.New(),
		test.IdSourceAccountAsUuid(),
		test.IdDestinyAccountAsUuid(),
	)
	require.NoError(t, err)
	var (
		lockToken    = uuid.New()
		now          = time.Now()
		idPspPayment = uuid.New()
	)
	// act
	err = usecases.ProcessOutboxEvents(ctx, tree, now, 1, lockToken, sendOutboxEventToPspFakeSuccess(idPspPayment))
	require.NoError(t, err)
	// assert
	// check outbox rows
	rows, err := tree.DbPool.Query(ctx, "select * from outbox")
	require.NoError(t, err)
	outboxes, err := pgx.CollectRows(rows, pgx.RowToStructByName[db.Outbox])
	require.NoError(t, err)
	assert.Equal(t, 1, len(outboxes))
	for _, outbox := range outboxes {
		assert.False(t, outbox.IsPending, "outbox.is_pending")
		assert.Equal(t, 1, outbox.AttemptCount, "outbox.attempt_count")
		require.Nil(t, outbox.LockToken, "outbox.lock_token")
		require.Nil(t, outbox.LockedUntil, "outbox.locked_until")
	}
	// check payment rows
	rows, err = tree.DbPool.Query(ctx, "select * from payment")
	require.NoError(t, err)
	payments, err := pgx.CollectRows(rows, pgx.RowToStructByName[db.Payment])
	require.NoError(t, err)
	assert.Equal(t, 1, len(payments))
	for _, payment := range payments {
		assert.False(t, payment.IsPending, "payment.is_pending")
		require.NotNil(t, payment.IdPspPayment, "payment.id_psp_payment")
		assert.Equal(t, idPspPayment.String(), payment.IdPspPayment.String(), "payment.id_psp_payment")
		require.NotNil(t, payment.ProcessedAt, "payment.processed_at")
	}
}

func TestProcessOutboxEventsError(t *testing.T) {
	// arrange
	ctx := context.Background()
	require.NoError(t, resetDbState(ctx, tree))
	_, err := usecases.HandleCheckout(
		tree,
		ctx,
		uuid.New(),
		test.IdSourceAccountAsUuid(),
		test.IdDestinyAccountAsUuid(),
	)
	require.NoError(t, err)
	var (
		lockToken = uuid.New()
		now       = time.Now()
	)
	// act
	err = usecases.ProcessOutboxEvents(ctx, tree, now, 1, lockToken, sendOutboxEventToPspFakeError())
	require.NoError(t, err)
	// assert
	// check outbox rows
	rows, err := tree.DbPool.Query(ctx, "select * from outbox")
	require.NoError(t, err)
	outboxes, err := pgx.CollectRows(rows, pgx.RowToStructByName[db.Outbox])
	require.NoError(t, err)
	assert.Equal(t, 1, len(outboxes))
	for _, outbox := range outboxes {
		assert.True(t, outbox.IsPending, "outbox.is_pending")
		assert.Equal(t, 1, outbox.AttemptCount, "outbox.attempt_count")
		require.NotNil(t, outbox.LastError, "outbox.last_error")
		assert.Contains(t, *outbox.LastError, "500", "outbox.last_error")
		assert.Contains(t, *outbox.LastError, "the server couldn't process the request", "outbox.last_error")
		assert.Nil(t, outbox.LockToken, "outbox.lock_token")
		assert.Nil(t, outbox.LockedUntil, "outbox.locked_until")
	}
}

func TestProcessOutboxEvents4Workers(t *testing.T) {
	t.Skip("Expensive test")
	// arrange
	ctx := context.Background()
	require.NoError(t, resetDbState(ctx, tree))
	var (
		N               = 10000
		numberOfWorkers = 10
	)
	for range N {
		_, err := usecases.HandleCheckout(
			tree,
			ctx,
			uuid.New(),
			test.IdSourceAccountAsUuid(),
			test.IdDestinyAccountAsUuid(),
		)
		if err != nil {
			t.Fatalf("%s", err.Error())
		}
	}
	// act
	var wg sync.WaitGroup
	for range numberOfWorkers {
		wg.Go(func() {
			usecases.ProcessOutboxEvents(ctx, tree, time.Now(), N/numberOfWorkers, uuid.New(), sendOutboxEventToPspFakeSuccess(uuid.New()))
		})
	}
	wg.Wait()
	// assert
	// check outbox rows
	rows, err := tree.DbPool.Query(ctx, "select * from outbox order by id")
	require.NoError(t, err)
	outboxes, err := pgx.CollectRows(rows, pgx.RowToStructByName[db.Outbox])
	require.NoError(t, err)
	assert.Equal(t, N, len(outboxes))
	for _, outbox := range outboxes {
		assert.False(t, outbox.IsPending, "outbox.is_pending")
	}
	// check payment rows
	rows, err = tree.DbPool.Query(ctx, "select * from payment")
	require.NoError(t, err)
	payments, err := pgx.CollectRows(rows, pgx.RowToStructByName[db.Payment])
	require.NoError(t, err)
	assert.Equal(t, N, len(payments))
	for _, payment := range payments {
		assert.False(t, payment.IsPending, "payment.is_pending")
	}
}

func TestGetNextTryAt(t *testing.T) {
	cases := []struct {
		idCase         string
		attemptCount   int
		expectedResult time.Duration
	}{
		{"attempt 0", 0, time.Duration(0) * time.Second},
		{"attempt 1", 1, time.Duration(2) * time.Second},
		{"attempt 2", 2, time.Duration(4) * time.Second},
		{"attempt 3", 3, time.Duration(8) * time.Second},
		{"attempt 4", 4, time.Duration(16) * time.Second},
		{"attempt 5", 5, time.Duration(32) * time.Second},
		{"attempt 6", 6, time.Duration(32) * time.Second},
		{"attempt 7", 7, time.Duration(32) * time.Second},
		{"attempt 100", 100, time.Duration(32) * time.Second},
	}
	for _, tt := range cases {
		t.Run(tt.idCase, func(t *testing.T) {
			result := usecases.GetNextTryAt(tt.attemptCount)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}
