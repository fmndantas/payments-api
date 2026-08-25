package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/fmndantas/payments/internal/db"
	"github.com/fmndantas/payments/internal/dependencies"
	"github.com/fmndantas/payments/internal/usecases/outbox"
)

// TODO: graceful shutdown?
func main() {
	idWorker := uuid.New()

	log.SetFlags(0)
	log.SetPrefix(fmt.Sprintf("[worker - %s] ", idWorker.String()))

	// FIX: get this from env
	dbConfiguration := db.CreateLocalConfiguration()
	tree, err := dependencies.Initialize(dbConfiguration)

	if err != nil {
		log.Fatalln(err)
		return
	}

	context := context.Background()

	ch := make(chan error)

	go func() {
		for now := range time.Tick(30 * time.Second) {
			ch <- outbox.ProcessOutboxEvents(
				context, tree, now, 10, uuid.New(), outbox.SendOutboxEventToPspFake, outbox.EventIsErroredWithFiveAttempts,
			)
		}
	}()

	for err := range ch {
		if err != nil {
			log.Println(err)
		}
	}
}
