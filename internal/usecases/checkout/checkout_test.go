package checkout_test

import (
	"context"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fmndantas/payments/internal/db"
	"github.com/fmndantas/payments/internal/dependencies"
	"github.com/fmndantas/payments/internal/usecases/checkout"
	"github.com/fmndantas/payments/test"
)

// TODO: remove duplication with main_test?
func TestHappyPath(t *testing.T) {
	// arrange
	ctx := context.Background()
	dbLocalConfiguration := db.CreateLocalConfiguration()
	container, err, stopPostgres := test.InitializePostgresTestcontainer(
		dbLocalConfiguration, ctx,
	)
	defer stopPostgres()
	require.NoErrorf(t, err, "error when initializing Postgres Testcontainer: %s")
	host, err := container.Host(ctx)
	require.NoErrorf(t, err, "failed to get testcontainer host: %s")
	port, err := container.MappedPort(ctx, fmt.Sprintf("%d", dbLocalConfiguration.Port))
	require.NoErrorf(t, err, "failed to get testcontainer mapped port: %s")
	dbTestConfiguration := db.DbConfiguration{
		Host:     host,
		Port:     int(port.Num()),
		Database: dbLocalConfiguration.Database,
		Username: dbLocalConfiguration.Username,
		Password: dbLocalConfiguration.Password,
	}
	tree, err := dependencies.Initialize(dbTestConfiguration)
	require.NoErrorf(t, err, "failed to initialize the dependencies tree: %s")
	// act
	idPaymentExternal, err := checkout.HandleCheckout(
		tree, ctx, uuid.New(), test.IdDestinyAccountAsUuid(), test.IdDestinyAccountAsUuid(), time.Now(),
	)
	log.Printf("idPaymentExternal = %s", idPaymentExternal)
	// assert
	require.NoErrorf(t, err, "checkout running resulted in error: %s")
	// checks if payment was created
	var idPaymentInternal int64
	err = tree.DbPool.QueryRow(
		ctx, "select id_internal from payment where id_external = $1", idPaymentExternal,
	).Scan(&idPaymentInternal)
	require.NoError(t, err)
	// checks if outbox was created
	var idOutbox int64
	err = tree.DbPool.QueryRow(
		ctx, "select id from outbox where id_payment = $1", idPaymentInternal,
	).Scan(&idOutbox)
	require.NoError(t, err)
	assert.Positive(t, idOutbox, "idOutbox should be positive")
}
