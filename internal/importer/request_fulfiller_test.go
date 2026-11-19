package importer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vavallee/bindery/internal/db"
	"github.com/vavallee/bindery/internal/models"
)

type spyFulfiller struct{ bookIDs []int64 }

func (f *spyFulfiller) BookImported(_ context.Context, bookID int64) {
	f.bookIDs = append(f.bookIDs, bookID)
}

// A clean import tells the requests API which book arrived, so a request for
// it can send requestFulfilled (#4054).
func TestImportSuccess_ReportsBookToRequests(t *testing.T) {
	libDir, dlDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(dlDir, "book.epub"), bookSized("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	ctx := context.Background()
	authorRepo, bookRepo, dlRepo := db.NewAuthorRepo(database), db.NewBookRepo(database), db.NewDownloadRepo(database)

	author := &models.Author{ForeignID: "OLA-4054", Name: "Asked Author", SortName: "Author, Asked", Monitored: true, MetadataProvider: "openlibrary"}
	if err := authorRepo.Create(ctx, author); err != nil {
		t.Fatal(err)
	}
	book := &models.Book{
		ForeignID: "OLB-4054", AuthorID: author.ID, Title: "Issue 4054", SortTitle: "Issue 4054",
		Status: models.BookStatusWanted, Monitored: true, AnyEditionOK: true, MetadataProvider: "openlibrary",
	}
	if err := bookRepo.Create(ctx, book); err != nil {
		t.Fatal(err)
	}
	dl := &models.Download{GUID: "4054-guid", Title: "Issue 4054", BookID: &book.ID, Status: models.StateCompleted}
	if err := dlRepo.Create(ctx, dl); err != nil {
		t.Fatal(err)
	}

	spy := &spyFulfiller{}
	s := NewScanner(dlRepo, db.NewDownloadClientRepo(database), bookRepo, authorRepo, db.NewHistoryRepo(database), libDir, "", "", "", "").
		WithRequestFulfiller(spy)
	s.tryImportInternal(ctx, dl, dlDir, "", "", "", nil, nil)

	if len(spy.bookIDs) != 1 || spy.bookIDs[0] != book.ID {
		t.Fatalf("books reported to requests = %v, want [%d]", spy.bookIDs, book.ID)
	}
	// The book has its file by the time requests are told, or a fulfilled
	// check made then would find it still wanted.
	if got, _ := bookRepo.GetByID(ctx, book.ID); got == nil || got.Status != models.BookStatusImported {
		t.Fatalf("book after import = %+v, want imported", got)
	}
}
