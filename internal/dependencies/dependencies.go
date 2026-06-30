package dependencies

import (
	"context"
	"log"

	"github.com/fmndantas/payments/internal/db"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Tree struct {
	DbPool *pgxpool.Pool
}

// FIX: the context used for the pool is not the gin.Context
func Initialize(dbConfiguration db.DbConfiguration) *Tree {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbConfiguration.Dsn())

	if err != nil {
		log.Fatalf("Unable to connect to database: %s", err)
	}

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("Unable to ping database: %s", err)
	}

	log.Println("Connected to database successfully")

	return &Tree{
		DbPool: pool,
	}
}

func (tree *Tree) InjectToController(
	originalFn func(*Tree, *gin.Context),
) gin.HandlerFunc {
	return func(c *gin.Context) {
		originalFn(tree, c)
	}
}
