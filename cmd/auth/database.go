package main

import (
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
)

func validateAuthDSN(dsn string) error {
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		return errors.New("AUTH_DSN must be a valid MySQL DSN")
	}
	if !config.ParseTime {
		return errors.New("AUTH_DSN must set parseTime=true")
	}
	if config.Loc != time.UTC {
		return errors.New("AUTH_DSN must set loc=UTC")
	}
	return nil
}
