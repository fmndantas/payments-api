package usecases_test

import (
	"context"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fmndantas/payments/internal/db"
	"github.com/fmndantas/payments/internal/dependencies"
	"github.com/fmndantas/payments/internal/usecases"
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
	if err != nil {
		t.Fatalf("error when initializing Postgres Testcontainer: %s", err)
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
	tree, err := dependencies.Initialize(dbTestConfiguration)
	if err != nil {
		log.Fatalf("failed to initialize the dependencies tree: %s", err)
	}
	// act
	idPaymentExternal, err := usecases.HandleCheckout(
		tree, ctx, uuid.New(), test.IdDestinyAccountAsUuid(), test.IdDestinyAccountAsUuid(), time.Now(),
	)
	log.Printf("idPaymentExternal = %s", idPaymentExternal)
	// assert
	if err != nil {
		log.Fatalf("checkout running resulted in error: %s", err)
	}
	// checks if payment was created
	var idPaymentInternal int64
	err = tree.DbPool.QueryRow(
		ctx, "select id_internal from payment where id_external = $1", idPaymentExternal,
	).Scan(&idPaymentInternal)
	if err != nil {
		t.Fatal(err)
	}
	// checks if outbox was created
	var idOutbox int64
	err = tree.DbPool.QueryRow(
		ctx, "select id from outbox where id_payment = $1", idPaymentInternal,
	).Scan(&idOutbox)
	if err != nil {
		t.Fatal(err)
	}
}
