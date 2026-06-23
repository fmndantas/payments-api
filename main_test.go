package main_test

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/fmndantas/payments/internal/dependencies"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

var tree *dependencies.Tree = nil

func TestMain(m *testing.M) {
	dbName, dbUser, dbPassword := "payments", "postgres", "postgres"
	ctx := context.Background()
	container, err := postgres.Run(
		ctx,
		"postgres",
		postgres.WithOrderedInitScripts(
			filepath.Join(filepath.Join("migrations", "001_initial_migration.sql")),
			filepath.Join(filepath.Join("migrations", "002_create_accounts.sql")),
		),
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPassword),
		postgres.BasicWaitStrategies(),
	)
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Printf("failed to terminate container: %s", err)
		}
	}()
	if err != nil {
		log.Panicf("failed to start PostgreSQL container: %s", err)
	}
	connectionString, err := container.ConnectionString(ctx)
	tree = dependencies.InitializeDefault(connectionString)
	code := m.Run()
	tree = nil
	os.Exit(code)
}

func TestTreeShouldBeInitialized(t *testing.T) {
	if tree == nil {
		t.Error("the tree should be initialized here")
	}
}
