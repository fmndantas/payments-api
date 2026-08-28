package dependencies

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fmndantas/payments/internal/db"
)

type Tree struct {
	DbPool *pgxpool.Pool
}

type slogQueryTracer struct{}

func (slogQueryTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	slog.Debug("query", "sql", data.SQL, "args", data.Args)
	return ctx
}

func (slogQueryTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	if data.Err != nil {
		slog.Error("query failed", "err", data.Err, "command_tag", data.CommandTag.String())
		return
	}
	slog.Debug("query done", "command_tag", data.CommandTag.String(), "rows_affected", data.CommandTag.RowsAffected())
}

// FIX: the context used for the pool is not the gin.Context
func Initialize(dbConfiguration db.DbConfiguration) (*Tree, error) {
	ctx := context.Background()

	config, err := pgxpool.ParseConfig(dbConfiguration.Dsn())

	if err != nil {
		return nil, fmt.Errorf("unable to parse pool configuration: %s", err)
	}

	config.ConnConfig.Tracer = slogQueryTracer{}

	pool, err := pgxpool.NewWithConfig(ctx, config)

	if err != nil {
		return nil, fmt.Errorf("unable to connect to database: %s", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("unable to ping database: %s", err)
	}

	slog.Info("connected to database successfully", "dsn", dbConfiguration.Dsn())

	return &Tree{
		DbPool: pool,
	}, nil
}

func (tree *Tree) InjectToController(
	originalFn func(*Tree, *gin.Context),
) gin.HandlerFunc {
	return func(c *gin.Context) {
		originalFn(tree, c)
	}
}
