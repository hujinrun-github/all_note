package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/hujinrun/flowspace/internal/migration"
)

func main() {
	sqlitePath := flag.String("sqlite", "", "source SQLite database path")
	postgresURL := flag.String("postgres", os.Getenv("FLOWSPACE_DATABASE_URL"), "target PostgreSQL URL")
	flag.Parse()

	if *sqlitePath == "" {
		fmt.Fprintln(os.Stderr, "-sqlite is required")
		os.Exit(2)
	}
	if *postgresURL == "" {
		fmt.Fprintln(os.Stderr, "-postgres or FLOWSPACE_DATABASE_URL is required")
		os.Exit(2)
	}
	if err := migration.MigrateSQLiteToPostgres(*sqlitePath, *postgresURL); err != nil {
		fmt.Fprintf(os.Stderr, "migration failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("SQLite to PostgreSQL migration completed")
}
