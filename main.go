package main

import (
	"github.com/gin-gonic/gin"

	"github.com/fmndantas/payments/internal/controller"
	"github.com/fmndantas/payments/internal/dependencies"
)

func main() {
	// FIXME: make this a env variable
	tree := dependencies.InitializeDefault("postgres://postgres:postgres@localhost:5432/payments")

	router := gin.Default()
	router.GET("health", tree.InjectToController(controller.CheckHealth))
	router.POST("checkout", tree.InjectToController(controller.Checkout))

	router.Run("localhost:8080")
}
