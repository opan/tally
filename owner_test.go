package main

import (
	"context"
	"errors"
	"testing"
)

// The owner hands the tally to an admin; the old owner stays on as an admin
// and loses the owner-only powers.
func TestTransferOwnership(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	owner, _ := s.CreateUser(ctx, "owner", "x")
	next, _ := s.CreateUser(ctx, "next", "x")
	outsider, _ := s.CreateUser(ctx, "outsider", "x")
	tl, err := s.CreateTally(ctx, owner.ID, Tally{Slug: "club", Name: "Club", Template: "badminton_doubles", Visibility: VisibilityLink})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddAdmin(ctx, tl.ID, owner.ID, "next"); err != nil {
		t.Fatal(err)
	}

	if err := s.TransferOwnership(ctx, tl.ID, owner.ID, outsider.ID); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("to a non-admin: got %v, want ErrUserNotFound", err)
	}
	if err := s.TransferOwnership(ctx, tl.ID, owner.ID, owner.ID); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("to the owner themselves: got %v, want ErrUserNotFound", err)
	}
	if err := s.TransferOwnership(ctx, tl.ID, next.ID, next.ID); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("by an admin: got %v, want ErrNotOwner", err)
	}

	if err := s.TransferOwnership(ctx, tl.ID, owner.ID, next.ID); err != nil {
		t.Fatal(err)
	}
	for u, want := range map[string]string{owner.ID: RoleAdmin, next.ID: RoleOwner} {
		if role, _ := s.RoleFor(ctx, tl.ID, u); role != want {
			t.Errorf("role of %s: %q, want %q", u, role, want)
		}
	}
	got, _ := s.TallyBySlug(ctx, "club")
	if got.OwnerID != next.ID {
		t.Errorf("owner_id = %s, want the new owner", got.OwnerID)
	}
	if err := s.TransferOwnership(ctx, tl.ID, owner.ID, owner.ID); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("old owner again: got %v, want ErrNotOwner", err)
	}
	// The new owner can remove the old one.
	if err := s.RemoveAdmin(ctx, tl.ID, next.ID, owner.ID); err != nil {
		t.Fatalf("removing the old owner: %v", err)
	}
	var n int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tally_id = $1 AND entity = 'owner'`, tl.ID).Scan(&n)
	if n != 1 {
		t.Errorf("audit entries for the transfer: %d, want 1", n)
	}
}
