package usecases_test

import (
	"context"
	"fmt"
	"log"
	"testing"

	"github.com/fmndantas/payments/internal/db"
	"github.com/fmndantas/payments/internal/dependencies"
	"github.com/fmndantas/payments/internal/usecases"
	"github.com/fmndantas/payments/test"
	"github.com/google/uuid"
)

// TODO: remove duplication with main_test?
func TestHappyPath(t *testing.T) {
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

	_, err = usecases.HandleCheckout(
		tree, uuid.New(), test.IdDestinyAccountAsUuid(), test.IdDestinyAccountAsUuid(),
	)
	if err != nil {
		log.Fatalf("failed to run checkout: %s", err)
	}
	// check if payment was saved correctly
	// check if outbox was saved correctly
}
