// Package store keeps TivoDesk's working data in a SQLite file: the
// download queue, and the last Now Playing list read from each TiVo.
// Settings live in the configuration file and keys in the credential
// store; neither is kept here.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // the database driver

	"tivodesk/internal/togo"
)

// The states a job moves through.
const (
	Queued    = "queued"
	Running   = "running"
	Done      = "done"
	Failed    = "failed"
	Cancelled = "cancelled"
)

// Job is one recording to download, and how that is going.
type Job struct {
	ID        int64          `json:"id"`
	TSN       string         `json:"tsn"`
	TiVoName  string         `json:"tivoName"`
	Recording togo.Recording `json:"recording"`
	// TS is true for a transport stream, false for a program stream.
	TS bool `json:"ts"`
	// Decrypt is true if the recording is decrypted as it downloads, and
	// saved as an ordinary video file rather than a .tivo file.
	Decrypt bool `json:"decrypt"`

	State string `json:"state"`
	// Error is why the job failed, in words fit for the user.
	Error string `json:"error"`
	// Path is where the finished file is, or will be.
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"` // received so far
	// SyncLosses counts damaged packets in a finished transport stream.
	SyncLosses int `json:"syncLosses"`
	Attempts   int `json:"attempts"`

	Created  time.Time `json:"created"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`

	// Rate is the current speed in bytes per second. It is not stored.
	Rate float64 `json:"rate"`
}

// IsFinished reports whether the job needs no more work.
func (j *Job) IsFinished() bool {
	return j.State == Done || j.State == Failed || j.State == Cancelled
}

// Listing is a TiVo's Now Playing list as last read.
type Listing struct {
	Fetched time.Time
	List    *togo.Listing
}

// Store is an open database.
type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
	id    INTEGER PRIMARY KEY AUTOINCREMENT,
	tsn   TEXT NOT NULL,
	state TEXT NOT NULL,
	data  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS listings (
	tsn     TEXT PRIMARY KEY,
	fetched TEXT NOT NULL,
	data    TEXT NOT NULL
);`

// Open opens the database at path, creating it if need be. The path
// ":memory:" gives a database that lasts until Close.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One connection: the workload is tiny, and it keeps an in-memory
	// database in one piece.
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{"PRAGMA journal_mode = WAL", "PRAGMA synchronous = NORMAL", schema} {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("could not open the database %s: %w", path, err)
		}
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// AddJob stores a new job and sets its ID.
func (s *Store) AddJob(j *Job) error {
	res, err := s.db.Exec("INSERT INTO jobs (tsn, state, data) VALUES (?, ?, '{}')", j.TSN, j.State)
	if err != nil {
		return err
	}
	if j.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	return s.UpdateJob(j)
}

// UpdateJob stores a job's current state.
func (s *Store) UpdateJob(j *Job) error {
	stored := *j
	stored.Rate = 0
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("UPDATE jobs SET tsn = ?, state = ?, data = ? WHERE id = ?", j.TSN, j.State, string(data), j.ID)
	return err
}

// DeleteJob removes a job.
func (s *Store) DeleteJob(id int64) error {
	_, err := s.db.Exec("DELETE FROM jobs WHERE id = ?", id)
	return err
}

// Jobs returns every job, oldest first.
func (s *Store) Jobs() ([]*Job, error) {
	rows, err := s.db.Query("SELECT id, data FROM jobs ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		var id int64
		var data string
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		j := &Job{}
		if err := json.Unmarshal([]byte(data), j); err != nil {
			continue // a row from a version that stored something else
		}
		j.ID = id
		out = append(out, j)
	}
	return out, rows.Err()
}

// SaveListing stores a TiVo's list, replacing the one held for it.
func (s *Store) SaveListing(tsn string, fetched time.Time, list *togo.Listing) error {
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO listings (tsn, fetched, data) VALUES (?, ?, ?)
		ON CONFLICT (tsn) DO UPDATE SET fetched = excluded.fetched, data = excluded.data`,
		tsn, fetched.UTC().Format(time.RFC3339), string(data))
	return err
}

// TouchListing records that a TiVo's stored list was confirmed as current.
func (s *Store) TouchListing(tsn string, fetched time.Time) error {
	_, err := s.db.Exec("UPDATE listings SET fetched = ? WHERE tsn = ?", fetched.UTC().Format(time.RFC3339), tsn)
	return err
}

// Listings returns the stored list for each TiVo, by TSN.
func (s *Store) Listings() (map[string]Listing, error) {
	rows, err := s.db.Query("SELECT tsn, fetched, data FROM listings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Listing{}
	for rows.Next() {
		var tsn, fetched, data string
		if err := rows.Scan(&tsn, &fetched, &data); err != nil {
			return nil, err
		}
		list := &togo.Listing{}
		if err := json.Unmarshal([]byte(data), list); err != nil {
			continue
		}
		when, _ := time.Parse(time.RFC3339, fetched)
		out[tsn] = Listing{Fetched: when, List: list}
	}
	return out, rows.Err()
}
