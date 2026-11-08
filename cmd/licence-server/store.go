package main

// SQLite store: one row per purchase, so a lost key can be re-sent and a
// duplicate capture cannot issue a second key.

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type purchase struct {
	OrderID      string
	CaptureID    string
	Installation string
	Licensee     string
	Email        string
	Key          string
	Amount       string
	Currency     string
	Created      time.Time
}

type store struct{ db *sql.DB }

func openStore(path string) (*store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite writes serialise anyway; this avoids SQLITE_BUSY
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS purchases (
  order_id     TEXT PRIMARY KEY,
  capture_id   TEXT NOT NULL DEFAULT '',
  installation TEXT NOT NULL DEFAULT '',
  licensee     TEXT NOT NULL,
  email        TEXT NOT NULL DEFAULT '',
  licence_key  TEXT NOT NULL,
  amount       TEXT NOT NULL,
  currency     TEXT NOT NULL,
  created      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS purchases_installation ON purchases(installation);
CREATE INDEX IF NOT EXISTS purchases_email ON purchases(email);
`); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &store{db: db}, nil
}

func (s *store) Close() error { return s.db.Close() }

var errNotFound = errors.New("no purchase found")

func (s *store) Put(p purchase) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO purchases
		 (order_id, capture_id, installation, licensee, email, licence_key, amount, currency, created)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		p.OrderID, p.CaptureID, p.Installation, p.Licensee, p.Email, p.Key, p.Amount, p.Currency,
		p.Created.UTC().Format(time.RFC3339))
	return err
}

func (s *store) scan(row *sql.Row) (*purchase, error) {
	var p purchase
	var created string
	err := row.Scan(&p.OrderID, &p.CaptureID, &p.Installation, &p.Licensee, &p.Email, &p.Key, &p.Amount, &p.Currency, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	p.Created, _ = time.Parse(time.RFC3339, created)
	return &p, nil
}

const purchaseCols = `order_id, capture_id, installation, licensee, email, licence_key, amount, currency, created`

func (s *store) ByOrder(orderID string) (*purchase, error) {
	return s.scan(s.db.QueryRow(`SELECT `+purchaseCols+` FROM purchases WHERE order_id = ?`, orderID))
}

// LatestByInstallation finds the most recent key bought from one machine.
func (s *store) LatestByInstallation(installation string) (*purchase, error) {
	if installation == "" {
		return nil, errNotFound
	}
	return s.scan(s.db.QueryRow(`SELECT `+purchaseCols+` FROM purchases WHERE installation = ? ORDER BY created DESC LIMIT 1`, installation))
}

// LatestByEmail finds the most recent key bought with one PayPal email.
func (s *store) LatestByEmail(email string) (*purchase, error) {
	if email == "" {
		return nil, errNotFound
	}
	return s.scan(s.db.QueryRow(`SELECT `+purchaseCols+` FROM purchases WHERE email = ? COLLATE NOCASE ORDER BY created DESC LIMIT 1`, email))
}

func (s *store) Count() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM purchases`).Scan(&n)
	return n, err
}
