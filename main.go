package main

import (
	"github.com/gin-gonic/gin"

	"github.com/fmndantas/payments/internal/controller"
	"github.com/fmndantas/payments/internal/db"
	"github.com/fmndantas/payments/internal/dependencies"
)

func Initialize(dbConfiguration db.DbConfiguration, testMode bool) *gin.Engine {
	tree := dependencies.Initialize(dbConfiguration)
	if testMode {
		gin.SetMode(gin.TestMode)
	}
	router := gin.Default()
	router.GET("health", tree.InjectToController(controller.Health))
	router.POST("checkout", tree.InjectToController(controller.Checkout))
	return router
}

func main() {
	// FIX: get this from env
	dbConfiguration := db.CreateLocalConfiguration()
	router := Initialize(dbConfiguration, false)
	router.Run("localhost:8080")
}
