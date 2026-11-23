package store

import (
	"context"
	"testing"
	"time"
)

func TestManagerPriorityPreservesGenerationAndRejectsStaleTransitions(t *testing.T) {
	s := openTwoManagerStore(t)
	ctx := context.Background()
	first, err := s.RequestManager(ctx, "server", "first", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.AcquireManager(ctx, first); err != nil || !ok {
		t.Fatalf("acquire = %v, %v", ok, err)
	}
	second, err := s.RequestManager(ctx, "server", "second", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != first.Generation+1 {
		t.Fatal("generation did not advance")
	}
	for _, state := range []string{ManagerPending, ManagerAcquired} {
		if ok, err := s.ResolveManager(ctx, first, state); err != nil || ok {
			t.Fatalf("stale resolve = %v, %v", ok, err)
		}
	}
	if ok, err := s.ReserveManager(ctx, first, time.Minute); err != nil || ok {
		t.Fatalf("stale reserve = %v, %v", ok, err)
	}
	if ok, err := s.AcquireManager(ctx, first); err != nil || ok {
		t.Fatalf("stale acquire = %v, %v", ok, err)
	}
	if ok, err := s.ResolveManager(ctx, second, ManagerPending); err != nil || !ok {
		t.Fatalf("withdraw = %v, %v", ok, err)
	}
	third, err := s.RequestManager(ctx, "server", "third", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if third.Generation != second.Generation+1 {
		t.Fatal("resolved generation reused")
	}
}

func TestManagerPendingPriorityAndRestartReservation(t *testing.T) {
	s := openTwoManagerStore(t)
	ctx := context.Background()
	r, err := s.RequestManager(ctx, "server", "token", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, pending, err := s.ManagerPriority(ctx, r.Server); err != nil || !pending {
		t.Fatalf("pending = %v, %v", pending, err)
	}
	if _, pending, err := s.ManagerPriority(ctx, "different"); err != nil || pending {
		t.Fatalf("other server pending = %v, %v", pending, err)
	}
	if ok, err := s.AcquireManager(ctx, r); err != nil || !ok {
		t.Fatalf("acquire = %v, %v", ok, err)
	}
	if _, pending, err := s.ManagerPriority(ctx, r.Server); err != nil || pending {
		t.Fatalf("acquired priority = %v, %v", pending, err)
	}
	if ok, err := s.ReserveManager(ctx, r, time.Minute); err != nil || !ok {
		t.Fatalf("reserve = %v, %v", ok, err)
	}
	reserved, pending, err := s.ManagerPriority(ctx, r.Server)
	if err != nil || !pending || reserved.Generation != r.Generation || reserved.Token != r.Token {
		t.Fatalf("reservation = %+v %v %v", reserved, pending, err)
	}
	if _, err := s.db.Exec(`UPDATE manager_requests SET priority_until = unixepoch() - 1 WHERE server = ?`, r.Server); err != nil {
		t.Fatal(err)
	}
	if _, pending, err := s.ManagerPriority(ctx, r.Server); err != nil || pending {
		t.Fatalf("expired priority = %v, %v", pending, err)
	}
	if ok, err := s.AcquireManager(ctx, r); err != nil || ok {
		t.Fatalf("expired acquire = %v, %v", ok, err)
	}
}

func TestManagerRequestDoesNotReuseExhaustedGeneration(t *testing.T) {
	s := openTwoManagerStore(t)
	ctx := context.Background()
	if _, err := s.RequestManager(ctx, "server", "first", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE manager_requests SET generation = 9223372036854775807`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestManager(ctx, "server", "second", time.Minute); err == nil {
		t.Fatal("generation overflow accepted")
	}
}
