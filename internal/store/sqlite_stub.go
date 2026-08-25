//go:build !cgo

package store

import "errors"

type SQLiteStore struct{}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	return nil, errors.New("sqlite store requires CGO; rebuild with CGO_ENABLED=1 or use TM_STORE=json/memory")
}
