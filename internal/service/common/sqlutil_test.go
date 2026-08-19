package common

import (
	"database/sql"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestMapSQLiteErrorTranslatesConstraintViolations(t *testing.T) {
	db, err := sql.Open("sqlite3", "file::memory:?_foreign_keys=1")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, ddl := range []string{
		`CREATE TABLE parent (id TEXT PRIMARY KEY)`,
		`CREATE TABLE child (id TEXT PRIMARY KEY, code TEXT UNIQUE, ref TEXT REFERENCES parent(id))`,
		`INSERT INTO parent (id) VALUES ('p')`,
		`INSERT INTO child (id, code, ref) VALUES ('c', 'x', 'p')`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("exec %q: %v", ddl, err)
		}
	}

	cases := []struct {
		name string
		sql  string
		want error
	}{
		{"primary key", `INSERT INTO child (id, code, ref) VALUES ('c', 'y', 'p')`, ErrDuplicate},
		{"unique", `INSERT INTO child (id, code, ref) VALUES ('c2', 'x', 'p')`, ErrDuplicate},
		{"foreign key", `INSERT INTO child (id, code, ref) VALUES ('c3', 'z', 'missing')`, ErrReferenced},
	}
	for _, tc := range cases {
		_, err := db.Exec(tc.sql)
		if err == nil {
			t.Fatalf("%s: expected a constraint violation", tc.name)
		}
		if mapped := MapSQLiteError(err); !errors.Is(mapped, tc.want) {
			t.Fatalf("%s: MapSQLiteError(%v)=%v want %v", tc.name, err, mapped, tc.want)
		}
	}

	if err := MapSQLiteError(nil); err != nil {
		t.Fatalf("nil should map to nil: %v", err)
	}
}
