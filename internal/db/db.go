package db

import (
	"fmt"
)

type DbConfiguration struct {
	Host     string
	Port     int
	Database string
	Username string
	Password string
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
