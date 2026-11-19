package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/vavallee/bindery/internal/models"
)

// lifecycleFixture is one author with a wanted book (100) and an imported
// book (101), and a requester whose book request for 100 and author request
// are both approved.
func lifecycleFixture(t *testing.T) (database *sql.DB, repo *RequestRepo, owner *User, bookReq, authorReq *models.LibraryRequest) {
	t.Helper()
	database, repo, users := newRequestsFixture(t)
	admin := mustUser(t, users, "admin")
	owner = mustUser(t, users, "reader")
	mustExec(t, database, `INSERT INTO authors (id, foreign_id, name, sort_name) VALUES (10, 'OL10A', 'Writer', 'Writer')`)
	mustExec(t, database, `INSERT INTO books (id, foreign_id, author_id, title, sort_title, status) VALUES (100, 'OL100W', 10, 'One', 'One', 'wanted')`)
	mustExec(t, database, `INSERT INTO books (id, foreign_id, author_id, title, sort_title, status) VALUES (101, 'OL101W', 10, 'Two', 'Two', 'imported')`)
	bookReq = mustRequest(t, repo, owner.ID, models.RequestKindBook, "OL100W")
	authorReq = mustRequest(t, repo, owner.ID, models.RequestKindAuthor, "OL10A")
	bookID, authorID := int64(100), int64(10)
	approveForTest(t, repo, bookReq.ID, admin.ID, &bookID, &authorID)
	approveForTest(t, repo, authorReq.ID, admin.ID, nil, &authorID)
	return database, repo, owner, bookReq, authorReq
}

func approveForTest(t *testing.T, repo *RequestRepo, id, adminID int64, bookID, authorID *int64) {
	t.Helper()
	claimed, err := repo.Claim(context.Background(), id, adminID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(context.Background(), id, claimed.ClaimToken, bookID, authorID); err != nil {
		t.Fatal(err)
	}
}

func fulfilledIDs(t *testing.T, repo *RequestRepo, bookID int64) []int64 {
	t.Helper()
	got, err := repo.MarkFulfilled(context.Background(), bookID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(got))
	for _, r := range got {
		if r.OwnerUsername != "reader" {
			t.Errorf("request %d came back without its owner: %+v", r.ID, r)
		}
		ids = append(ids, r.ID)
	}
	return ids
}

// A request is returned by MarkFulfilled once, when its book is imported (or,
// for an author request, the author's first book), and never again until the
// request is reopened.
func TestRequestRepo_MarkFulfilledStampsOnce(t *testing.T) {
	database, repo, owner, bookReq, authorReq := lifecycleFixture(t)
	ctx := context.Background()

	if got := fulfilledIDs(t, repo, 100); len(got) != 0 {
		t.Fatalf("a wanted book fulfilled %v", got)
	}
	if got := fulfilledIDs(t, repo, 101); len(got) != 1 || got[0] != authorReq.ID {
		t.Fatalf("the author's imported book fulfilled %v, want the author request", got)
	}
	mustExec(t, database, `UPDATE books SET status = 'imported' WHERE id = 100`)
	if got := fulfilledIDs(t, repo, 100); len(got) != 1 || got[0] != bookReq.ID {
		t.Fatalf("the imported book fulfilled %v, want the book request", got)
	}
	for _, bookID := range []int64{100, 101} {
		if got := fulfilledIDs(t, repo, bookID); len(got) != 0 {
			t.Fatalf("a second import of book %d fulfilled %v again", bookID, got)
		}
	}

	// Asking again clears the stamp, so the new ask can be fulfilled.
	if err := repo.Reopen(ctx, bookReq.ID, owner.ID, "", "{}", "", nil, 0); err != nil {
		t.Fatal(err)
	}
	bookID := int64(100)
	approveForTest(t, repo, bookReq.ID, owner.ID, &bookID, nil)
	if got := fulfilledIDs(t, repo, 100); len(got) != 1 || got[0] != bookReq.ID {
		t.Fatalf("a reopened request was fulfilled %v, want it once more", got)
	}
}

// Migration 136 stamps the requests that are fulfilled already, so the first
// import after an upgrade does not announce them, and leaves the rest unset.
func TestMigrate136BackfillsFulfilledRequests(t *testing.T) {
	database, repo, _, bookReq, authorReq := lifecycleFixture(t)

	// Put the database back at 135: run 136's own Down, then forget it ran.
	const file = "136_request_lifecycle_events.sql"
	content, err := migrationsFS.ReadFile("migrations/" + file)
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(content), "-- +migrate Down")
	if !ok {
		t.Fatal("migration 136 has no Down section")
	}
	for _, stmt := range splitSQLStatements(down) {
		mustExec(t, database, stmt)
	}
	mustExec(t, database, `DELETE FROM schema_migrations WHERE version = ?`, migrationVersionForTest(t, file))
	if err := migrate(database); err != nil {
		t.Fatalf("migrate 136 onto existing requests: %v", err)
	}

	stamped := func(id int64) bool {
		var at sql.NullTime
		if err := database.QueryRow(`SELECT fulfilled_at FROM requests WHERE id = ?`, id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at.Valid
	}
	if !stamped(authorReq.ID) {
		t.Error("an author request with an imported book was not stamped")
	}
	if stamped(bookReq.ID) {
		t.Error("a book request whose book is still wanted was stamped")
	}
	if got := fulfilledIDs(t, repo, 101); len(got) != 0 {
		t.Errorf("a request fulfilled before the upgrade was announced after it: %v", got)
	}
}

// The three lifecycle flags survive create, get, list and update.
func TestNotificationRepo_RequestLifecycleFlagsRoundTrip(t *testing.T) {
	database, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	repo := NewNotificationRepo(database)

	n := &models.Notification{Name: "hook", Type: "webhook", URL: "https://example.test/h", Method: "POST", OnRequestApproved: true, OnRequestFulfilled: true, Enabled: true}
	if err := repo.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, n.ID)
	if err != nil || got == nil || !got.OnRequestApproved || got.OnRequestDeclined || !got.OnRequestFulfilled {
		t.Fatalf("after create: %+v err %v, want approved and fulfilled only", got, err)
	}
	got.OnRequestApproved, got.OnRequestDeclined = false, true
	if err := repo.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	list, err := repo.List(ctx)
	if err != nil || len(list) != 1 || list[0].OnRequestApproved || !list[0].OnRequestDeclined || !list[0].OnRequestFulfilled {
		t.Fatalf("after update: %+v err %v", list, err)
	}
}
