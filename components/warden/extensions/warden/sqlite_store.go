package warden

import (
	"database/sql"
	"os"
	"path/filepath"

	// The one place the SQLite engine is named: a pure-Go build of SQLite (no cgo), so the learning database stays
	// the same file the original writes with node:sqlite (WAL, the same schema, json_extract).
	_ "modernc.org/sqlite"
)

// Row is one result row: column name to int64, float64, string, []byte-as-string or nil.
type Row map[string]any

// sqlStore is the part of a SQLite connection the learning module uses (the DatabaseSync subset of
// src/sqlite-adapter.ts). A nil sqlStore is the NOOP database: learning is off.
type sqlStore interface {
	// exec runs one or more statements and reports the rows the last one changed and its last insert row id.
	exec(query string, args ...any) (changes, lastInsertID int64, err error)
	all(query string, args ...any) ([]Row, error)
	// get is the first row, ok false for a miss.
	get(query string, args ...any) (Row, bool, error)
	close() error
}

type sqliteStore struct{ db *sql.DB }

// openSQLite opens the database file, creating its directory (the engine does not), in WAL mode with the original's
// ten second busy timeout. A failure closes the half-open handle and returns the error.
func openSQLite(path string) (sqlStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One connection, like DatabaseSync: PRAGMAs are per connection and statements run one after another.
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA journal_mode = WAL", "PRAGMA busy_timeout = 10000"} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return &sqliteStore{db: db}, nil
}

func (s *sqliteStore) exec(query string, args ...any) (int64, int64, error) {
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, 0, err
	}
	changes, _ := res.RowsAffected()
	id, _ := res.LastInsertId()
	return changes, id, nil
}

func (s *sqliteStore) all(query string, args ...any) ([]Row, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []Row{}
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := Row{}
		for i, c := range cols {
			if b, ok := values[i].([]byte); ok {
				row[c] = string(b)
			} else {
				row[c] = values[i]
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *sqliteStore) get(query string, args ...any) (Row, bool, error) {
	rows, err := s.all(query, args...)
	if err != nil || len(rows) == 0 {
		return nil, false, err
	}
	return rows[0], true, nil
}

func (s *sqliteStore) close() error { return s.db.Close() }
