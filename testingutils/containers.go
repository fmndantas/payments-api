package containers

import (
	"context"
	"log"
	"path/filepath"

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
			filepath.Join(filepath.Join("migrations", "001_initial_migration.sql")),
			filepath.Join(filepath.Join("migrations", "002_create_accounts.sql")),
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
