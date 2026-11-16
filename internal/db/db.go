// Package db — Database Layer from CRUSH/OPENCODE
package db

import "database/sql"

type Database struct {
	conn *sql.DB
}

func NewDatabase(path string) (*Database, error) {
	return &Database{}, nil
}

func (d *Database) SaveSession(data string) error {
	return nil
}

func (d *Database) LoadSession(id string) (string, error) {
	return "", nil
}

func (d *Database) Close() error {
	return nil
}
