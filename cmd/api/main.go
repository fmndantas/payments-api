package main

import (
	"log"
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/fmndantas/payments/internal"
	"github.com/fmndantas/payments/internal/controller"
	"github.com/fmndantas/payments/internal/db"
	"github.com/fmndantas/payments/internal/dependencies"
)

func Initialize(dbConfiguration db.DbConfiguration, testMode bool) (*gin.Engine, error) {
	tree, err := dependencies.Initialize(dbConfiguration)

	if err != nil {
		return nil, err
	}

	if testMode {
		gin.SetMode(gin.TestMode)
	}

	internal.ConfigureLogLevel()

	gin.DisableConsoleColor()

	router := gin.Default()
	router.GET("health", tree.InjectToController(controller.Health))
	router.POST("checkout", tree.InjectToController(controller.Checkout))
	return router, nil
}

func main() {
	// FIX: get this from env
	dbConfiguration := db.CreateLocalConfiguration()
	router, err := Initialize(dbConfiguration, false)

	slog.SetLogLoggerLevel(slog.LevelDebug)

	if err != nil {
		log.Fatalln(err)
		return
	}

	router.Run("localhost:8080")
}
