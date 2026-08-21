package test

import (
	"context"
	"log"
	"path/filepath"
	"runtime"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/fmndantas/payments/internal/db"
)

func InitializePostgresTestcontainer(dbConfiguration db.DbConfiguration, ctx context.Context) (
	*postgres.PostgresContainer, error, func(),
) {
	container, err := postgres.Run(
		ctx,
		"postgres",
		postgres.WithOrderedInitScripts(
			migrationPath("001_initial_migration.sql"),
		),
		postgres.WithDatabase(dbConfiguration.Database),
		postgres.WithUsername(dbConfiguration.Username),
		postgres.WithPassword(dbConfiguration.Password),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, err, func() {}
	}
	return container, nil, func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Fatalf("failed to terminate container: %s", err)
		}
	}
}

func migrationPath(filename string) string {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("unable to resolve migration path")
	}

	projectRoot := filepath.Dir(filepath.Dir(currentFile))
	return filepath.Join(projectRoot, "migrations", filename)
}
