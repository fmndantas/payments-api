package db

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

type DbConfiguration struct {
	Host     string
	Port     int
	Database string
	Username string
	Password string
}

type Outbox struct {
	Id                int64      `db:"id"`
	IdInternalPayment int64      `db:"id_payment"`
	Status            string     `db:"status"`
	NextTryAt         time.Time  `db:"next_try_at"`
	AttemptCount      int        `db:"attempt_count"`
	LockedUntil       *time.Time `db:"locked_until"`
	LockToken         *uuid.UUID `db:"lock_token"`
	CreatedAt         time.Time  `db:"created_at"`
	LastProcessedAt   *time.Time `db:"last_processed_at"`
	LastResult        *string    `db:"last_result"`
}

type Payment struct {
	IdInternal       int64      `db:"id_internal"`
	IdExternal       uuid.UUID  `db:"id_external"`
	IdRequest        uuid.UUID  `db:"id_request"`
	IdSourceAccount  int64      `db:"id_source_account"`
	IdDestinyAccount int64      `db:"id_destiny_account"`
	CreatedAt        time.Time  `db:"created_at"`
	IdPspPayment     *uuid.UUID `db:"id_psp_payment"`
	PspResult        *string    `db:"psp_result"`
}

func (dc DbConfiguration) Dsn() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s",
		dc.Username,
		dc.Password,
		dc.Host,
		dc.Port,
		dc.Database,
	)
}

func CreateLocalConfiguration() DbConfiguration {
	return DbConfiguration{
		Host:     "localhost",
		Port:     5432,
		Database: "payments",
		Username: "postgres",
		Password: "postgres",
	}
}
