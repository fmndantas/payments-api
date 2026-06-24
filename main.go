package main

import (
	"github.com/gin-gonic/gin"

	"github.com/fmndantas/payments/internal/controller"
	"github.com/fmndantas/payments/internal/dependencies"
)

func Initialize(connectionString string, testMode bool) *gin.Engine {
	tree := dependencies.InitializeDefault(connectionString)
	if testMode {
		gin.SetMode(gin.TestMode)
	}
	router := gin.Default()
	router.GET("health", tree.InjectToController(controller.CheckHealth))
	router.POST("checkout", tree.InjectToController(controller.Checkout))
	return router
}

func main() {
	// FIX: make this a env variable
	router := Initialize("postgres://postgres:postgres@localhost:5432/payments", false)
	router.Run("localhost:8080")
}
