package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/YoanWai/agent-manager/internal/store"
)

const ManagerWait = 30 * time.Second
const managerCheck = 200 * time.Millisecond
const managerQueryTimeout = 2 * time.Second

var ErrManagerBusy = errors.New("another manager is active or waiting for this profile and server")
var ErrManagerSuperseded = errors.New("another manager took over this profile and server")

// Ownership separates short-lived takeover priority from lifetime exclusion.
// A request deadline never releases the OS lock held by a live owner.
type Ownership struct {
	store   *store.Store
	request store.ManagerRequest
	file    *os.File
	once    sync.Once
}

// AcquireOwnership opens coordination before Local creates bindings or workers.
// continuation is only a clean self-update's same-generation reservation.
func AcquireOwnership(ctx context.Context, profile, server string, interactive bool, continuation *store.ManagerRequest) (*Ownership, error) {
	if server == "" {
		return nil, errors.New("manager requires an explicit server identity")
	}
	if err := os.MkdirAll(profile, 0o755); err != nil {
		return nil, err
	}
	profile, err := filepath.EvalSymlinks(profile)
	if err != nil {
		return nil, err
	}
	profile, err = filepath.Abs(profile)
	if err != nil {
		return nil, err
	}
	server, err = canonicalManagerPath(server)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(profile, "state.db"))
	if err != nil {
		return nil, err
	}
	database, err := filepath.EvalSymlinks(filepath.Join(profile, "state.db"))
	if err != nil {
		st.Close()
		return nil, err
	}
	owner := &Ownership{store: st}
	fail := func(err error) (*Ownership, error) { owner.Close(); return nil, err }
	databaseIdentity, err := databaseFileIdentity(database)
	if err != nil {
		return fail(err)
	}
	hash := sha256.Sum256([]byte(databaseIdentity))
	anchor := server + ".manager-" + hex.EncodeToString(hash[:]) + ".lock"
	if err := os.MkdirAll(filepath.Dir(anchor), 0o700); err != nil {
		return fail(err)
	}
	owner.file, err = os.OpenFile(anchor, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fail(err)
	}
	server, err = ownershipFileIdentity(owner.file)
	if err != nil {
		return fail(err)
	}
	owner.request.Server = server
	waitCtx, cancel := context.WithTimeout(ctx, ManagerWait)
	defer cancel()
	if interactive {
		if continuation != nil {
			if continuation.Server != server || continuation.Generation < 1 || continuation.Token == "" {
				return fail(errors.New("invalid manager continuation identity"))
			}
			owner.request = *continuation
		} else {
			token := make([]byte, 16)
			if _, err := rand.Read(token); err != nil {
				return fail(err)
			}
			queryCtx, cancelQuery := context.WithTimeout(waitCtx, managerQueryTimeout)
			owner.request, err = st.RequestManager(queryCtx, server, hex.EncodeToString(token), ManagerWait)
			cancelQuery()
			if err != nil {
				return fail(err)
			}
		}
	}

	for {
		if err := waitCtx.Err(); err != nil {
			return fail(fmt.Errorf("waiting for previous manager to finish: %w", err))
		}
		queryCtx, cancelQuery := context.WithTimeout(waitCtx, managerQueryTimeout)
		current, pending, queryErr := st.ManagerPriority(queryCtx, server)
		cancelQuery()
		if queryErr != nil {
			return fail(queryErr)
		}
		if interactive && (current.Generation != owner.request.Generation || current.Token != owner.request.Token || !pending) {
			return fail(ErrManagerSuperseded)
		}
		held, lockErr := tryOwnershipFileLock(owner.file)
		if lockErr != nil {
			return fail(lockErr)
		}
		if held {
			queryCtx, cancelQuery = context.WithTimeout(waitCtx, managerQueryTimeout)
			if interactive {
				var acquired bool
				acquired, err = st.AcquireManager(queryCtx, owner.request)
				if err == nil && !acquired {
					err = ErrManagerSuperseded
				}
				if err == nil {
					owner.request.State = store.ManagerAcquired
				}
			} else {
				current, pending, err = st.ManagerPriority(queryCtx, server)
				if err == nil && pending {
					err = ErrManagerBusy
				}
				owner.request.Generation = current.Generation
			}
			cancelQuery()
			if err != nil {
				return fail(err)
			}
			return owner, nil
		}
		if !interactive {
			return fail(ErrManagerBusy)
		}
		select {
		case <-waitCtx.Done():
		case <-time.After(managerCheck):
		}
	}
}

// Changed returns once for the owner's lifetime, without sharing poll work.
// A database failure asks the owner to stop, never authorizes another holder.
func (o *Ownership) Changed(ctx context.Context) error {
	ticker := time.NewTicker(managerCheck)
	defer ticker.Stop()
	for {
		queryCtx, cancel := context.WithTimeout(ctx, managerQueryTimeout)
		r, pending, err := o.store.ManagerPriority(queryCtx, o.request.Server)
		cancel()
		if err != nil {
			return err
		}
		if r.Generation > o.request.Generation {
			if pending {
				return ErrManagerSuperseded
			}
			if r.State == store.ManagerAcquired {
				return errors.New("manager ownership changed while lifetime lock held")
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ReserveContinuation runs only after every runtime resource has drained.
func (o *Ownership) ReserveContinuation(ctx context.Context) (store.ManagerRequest, error) {
	queryCtx, cancel := context.WithTimeout(ctx, managerQueryTimeout)
	defer cancel()
	ok, err := o.store.ReserveManager(queryCtx, o.request, ManagerWait)
	if err != nil {
		return store.ManagerRequest{}, err
	}
	if !ok {
		return store.ManagerRequest{}, ErrManagerSuperseded
	}
	r := o.request
	r.State = store.ManagerPending
	return r, nil
}

// Close is safe only before runtime starts or after confirmed quiescence.
func (o *Ownership) Close() {
	o.once.Do(func() {
		if o.request.Token != "" {
			ctx, cancel := context.WithTimeout(context.Background(), managerQueryTimeout)
			expected := o.request.State
			if expected == "" {
				expected = store.ManagerPending
			}
			_, _ = o.store.ResolveManager(ctx, o.request, expected)
			cancel()
		}
		if o.file != nil {
			_ = o.file.Close()
		}
		_ = o.store.Close()
	})
}

// ReleaseContinuation leaves the bounded same-generation reservation intact.
func (o *Ownership) ReleaseContinuation() {
	o.once.Do(func() {
		if o.file != nil {
			_ = o.file.Close()
		}
		_ = o.store.Close()
	})
}

var retainedOwners struct {
	sync.Mutex
	roots []any
}

// RetainUntilExit keeps the actual lock closure (and its file), database and
// workers strongly reachable. An uncertain drain must never unlock via GC.
func (o *Ownership) RetainUntilExit(resources ...any) {
	retainedOwners.Lock()
	retainedOwners.roots = append(retainedOwners.roots, o, resources)
	retainedOwners.Unlock()
}

func (o *Ownership) ID() string { return o.request.Token }
