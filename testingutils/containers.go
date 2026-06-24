package containers

import (
	"context"
	"log"
	"path/filepath"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/fmndantas/payments/internal/db"
)

func InitializePostgresTestcontainer(ctx context.Context) (*postgres.PostgresContainer, error, func()) {
	dbConfiguration := db.CreateLocalConfiguration()
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
	return container, err, func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Printf("failed to terminate container: %s", err)
		}
	}
}
