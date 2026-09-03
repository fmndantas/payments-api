package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/fmndantas/payments/internal"
	"github.com/fmndantas/payments/internal/db"
	"github.com/fmndantas/payments/internal/dependencies"
	"github.com/fmndantas/payments/internal/psp"
	"github.com/fmndantas/payments/internal/resilience"
	"github.com/fmndantas/payments/internal/usecases/outbox"
)

// TODO: graceful shutdown?
func main() {
	logFile, err := internal.ConfigureLogLevelToFileAndStderr(
		os.Getenv("LOG_FILE"), os.Getenv("LOG_LEVEL"), os.Stderr,
	)

	if err != nil {
		log.Fatal(err)
	}

	defer logFile.Close()

	// FIX: get this from env
	dbConfiguration := db.CreateLocalConfiguration()
	tree, err := dependencies.Initialize(dbConfiguration)

	if err != nil {
		log.Fatalln(err)
		return
	}

	var (
		context = context.Background()
		ch      = make(chan error)
	)

	pspWithCircuitBreaker, isPspCircuitOpen := resilience.CreateCircuitBreaker(
		20,
		psp.SendOutboxEventToPspFake,
		func(output psp.PspOutput) bool {
			return output.HttpResponse.StatusCode >= 500 || output.Error != nil
		},
	)

	go func() {
		for now := range time.Tick(5 * time.Second) {
			ch <- outbox.ProcessOutboxEvents(
				context,
				tree,
				now,
				10,
				uuid.New(),
				isPspCircuitOpen,
				pspWithCircuitBreaker,
				outbox.EventIsErroredWithFiveAttempts,
			)
		}
	}()

	for err := range ch {
		if err != nil {
			log.Println(err)
		}
	}
}
