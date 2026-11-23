package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ManagerRequest is priority to acquire a manager's lifetime lock, never a
// replacement for holding that lock. Generations survive resolved requests.
type ManagerRequest struct {
	Server        string
	Generation    int64
	Token, State  string
	PriorityUntil int64
}

const (
	ManagerPending   = "pending"
	ManagerAcquired  = "acquired"
	ManagerWithdrawn = "withdrawn"
)

func scanManagerRequest(row *sql.Row, server string) (ManagerRequest, error) {
	r := ManagerRequest{Server: server}
	err := row.Scan(&r.Generation, &r.Token, &r.State, &r.PriorityUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return ManagerRequest{Server: server}, nil
	}
	return r, err
}

// RequestManager publishes once. Retrying a contender must not supersede a
// newer startup, and deleting old requests would reuse their generations.
func (s *Store) RequestManager(ctx context.Context, server, token string, wait time.Duration) (ManagerRequest, error) {
	if server == "" || token == "" || wait < time.Second {
		return ManagerRequest{}, errors.New("invalid manager request")
	}
	row := s.db.QueryRowContext(ctx, `INSERT INTO manager_requests(server, generation, token, state, priority_until)
 VALUES (?, 1, ?, 'pending', unixepoch() + ?)
 ON CONFLICT(server) DO UPDATE SET generation = generation + 1, token = excluded.token,
 state = excluded.state, priority_until = excluded.priority_until
 WHERE generation < 9223372036854775807
 RETURNING generation, token, state, priority_until`, server, token, int64(wait/time.Second))
	r, err := scanManagerRequest(row, server)
	if err == nil && r.Generation == 0 {
		err = errors.New("manager request generation exhausted")
	}
	return r, err
}

func (s *Store) ManagerRequest(ctx context.Context, server string) (ManagerRequest, error) {
	return scanManagerRequest(s.db.QueryRowContext(ctx, `SELECT generation, token, state, priority_until FROM manager_requests WHERE server = ?`, server), server)
}

// AcquireManager is called only while holding the corresponding OS lock.
func (s *Store) AcquireManager(ctx context.Context, r ManagerRequest) (bool, error) {
	return s.changeManagerRequest(ctx, `UPDATE manager_requests SET state = 'acquired', priority_until = 0
 WHERE server = ? AND generation = ? AND token = ? AND state = ? AND priority_until > unixepoch()`, r, ManagerPending)
}

// ReserveManager carries a clean self-update through the launch gap without
// publishing another startup and stealing a newer TUI's priority.
func (s *Store) ReserveManager(ctx context.Context, r ManagerRequest, wait time.Duration) (bool, error) {
	if wait < time.Second {
		return false, fmt.Errorf("invalid manager continuation wait")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE manager_requests SET state = 'pending', priority_until = unixepoch() + ?
 WHERE server = ? AND generation = ? AND token = ? AND state = ?`, int64(wait/time.Second), r.Server, r.Generation, r.Token, ManagerAcquired)
	return managerChanged(result, err)
}

func (s *Store) ResolveManager(ctx context.Context, r ManagerRequest, expected string) (bool, error) {
	if expected != ManagerPending && expected != ManagerAcquired {
		return false, errors.New("invalid manager request state")
	}
	return s.changeManagerRequest(ctx, `UPDATE manager_requests SET state = 'withdrawn', priority_until = 0
 WHERE server = ? AND generation = ? AND token = ? AND state = ?`, r, expected)
}

func (s *Store) changeManagerRequest(ctx context.Context, query string, r ManagerRequest, expected string) (bool, error) {
	result, err := s.db.ExecContext(ctx, query, r.Server, r.Generation, r.Token, expected)
	return managerChanged(result, err)
}

func managerChanged(result sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// ManagerPriority checks liveness using the same database clock as publication.
// It only affects contenders: expiration can never release an owner's OS lock.
func (s *Store) ManagerPriority(ctx context.Context, server string) (ManagerRequest, bool, error) {
	r := ManagerRequest{Server: server}
	var pending bool
	err := s.db.QueryRowContext(ctx, `SELECT generation, token, state, priority_until,
 state = 'pending' AND priority_until > unixepoch() FROM manager_requests WHERE server = ?`, server).
		Scan(&r.Generation, &r.Token, &r.State, &r.PriorityUntil, &pending)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, pending, err
}
