package dependencies

import (
	"database/sql"

	"github.com/gin-gonic/gin"
)

type Tree struct {
	DB *sql.DB
}

func (tree *Tree) InjectToController(
	originalFn func(*Tree, *gin.Context),
) func(*gin.Context) {
	return func(c *gin.Context) {
		originalFn(tree, c)
	}
}
