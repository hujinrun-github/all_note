package repository

import (
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

func InitDB(dbPath string) error {
	var err error
	DB, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return err
	}
	DB.SetMaxOpenConns(1)

	schema, err := os.ReadFile("db/schema.sql")
	if err != nil {
		return err
	}
	_, err = DB.Exec(string(schema))
	return err
}

func SeedDB() error {
	seed, err := os.ReadFile("db/seed.sql")
	if err != nil {
		return err
	}
	_, err = DB.Exec(string(seed))
	return err
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func nowUnix() int64 {
	return time.Now().Unix()
}
