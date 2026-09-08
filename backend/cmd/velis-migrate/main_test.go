package main

import (
	"strings"
	"testing"
)

func TestMigrationDatabaseURLPinsMetadataSchema(t *testing.T) {
	got, err := migrationDatabaseURL("postgres://velis:velis@localhost:5432/velis?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "search_path=public") || !strings.Contains(got, "sslmode=disable") {
		t.Fatalf("got=%q", got)
	}
}
