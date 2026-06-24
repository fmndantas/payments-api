package dependencies

import (
	"context"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Tree struct {
	DbPool *pgxpool.Pool
}

// FIX: the context used for the pool is not the gin.Context
// TODO: -> Initialize
func InitializeDefault(connectionString string) *Tree {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connectionString)

	if err != nil {
		log.Fatal("Unable to connect to database: ", err)
	}

	if err := pool.Ping(ctx); err != nil {
		log.Fatal("Unable to ping database: ", err)
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
