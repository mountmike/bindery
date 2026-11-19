package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vavallee/bindery/internal/notifier"
)

func lifecycleCapture(f *requestsFixture) *capturingNotifier {
	capture := &capturingNotifier{events: make(chan capturedEvent, 8)}
	f.h.WithNotifier(capture)
	return capture
}

func (c *capturingNotifier) next(t *testing.T, want string) map[string]interface{} {
	t.Helper()
	select {
	case got := <-c.events:
		if got.event != want {
			t.Fatalf("event = %q, want %q", got.event, want)
		}
		return got.payload
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s event sent", want)
		return nil
	}
}

func (c *capturingNotifier) none(t *testing.T, why string) {
	t.Helper()
	select {
	case extra := <-c.events:
		t.Fatalf("%s sent %+v", why, extra)
	case <-time.After(200 * time.Millisecond):
	}
}

// An admin's approval sends requestApproved, and the payload says who asked:
// the stable user id, a display name that cannot mention a channel, and an
// email address that is still an address (#4054).
func TestRequestsApprove_SendsRequestApproved(t *testing.T) {
	f := newRequestsFixture(t, wellsRequestStub(), &fakeAdder{})
	capture := lifecycleCapture(f)
	if _, err := f.database.ExecContext(context.Background(),
		"UPDATE users SET email = ?, display_name = ? WHERE id = ?", "reader@example.test", "@everyone Reader", f.requester.ID); err != nil {
		t.Fatal(err)
	}
	created := decodeRequest(t, f.create(t, f.requester, `{"kind":"book","foreignId":"OL27482W"}`))
	if p := capture.next(t, notifier.EventRequestCreated); p["email"] != "reader@example.test" {
		t.Errorf("requestCreated email = %v", p["email"])
	}

	if rec := f.approve(f.admin, created.ID, `{}`); rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	p := capture.next(t, notifier.EventRequestApproved)
	if p["requestId"] != created.ID || p["userId"] != f.requester.ID || p["username"] != "reader" || p["autoApproved"] != false {
		t.Errorf("payload = %+v", p)
	}
	if p["email"] != "reader@example.test" {
		t.Errorf("email = %v, want the address unchanged", p["email"])
	}
	if name, _ := p["displayName"].(string); name == "" || strings.Contains(name, "@") {
		t.Errorf("displayName = %q, want it present and without @", name)
	}
	capture.none(t, "an approval that added no book")
}

// An automatic approval sends requestApproved too, marked as automatic, and
// still no requestCreated.
func TestRequestsCreate_AutoApprovalSendsRequestApproved(t *testing.T) {
	f := newRequestsFixture(t, wellsRequestStub(), &fakeAdder{})
	capture := lifecycleCapture(f)
	enabled := true
	if err := f.users.SetRequestsAutoApprove(context.Background(), f.requester.ID, &enabled); err != nil {
		t.Fatal(err)
	}
	if rec := f.create(t, f.requester, `{"kind":"book","foreignId":"OL27482W"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if p := capture.next(t, notifier.EventRequestApproved); p["autoApproved"] != true {
		t.Errorf("payload = %+v, want autoApproved", p)
	}
	capture.none(t, "an auto approved request")
}

// A decline sends requestDeclined with the reason, made safe for a chat.
func TestRequestsDecline_SendsRequestDeclined(t *testing.T) {
	f := newRequestsFixture(t, wellsRequestStub(), &fakeAdder{})
	capture := lifecycleCapture(f)
	created := decodeRequest(t, f.create(t, f.requester, `{"kind":"book","foreignId":"OL27482W"}`))
	capture.next(t, notifier.EventRequestCreated)

	rec := httptest.NewRecorder()
	f.h.Decline(rec, withID(asUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"reason":"@here not this one"}`)), f.admin), created.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("decline: %d %s", rec.Code, rec.Body.String())
	}
	p := capture.next(t, notifier.EventRequestDeclined)
	if reason, _ := p["reason"].(string); !strings.HasSuffix(reason, "here not this one") || strings.Contains(reason, "@") {
		t.Errorf("reason = %q", p["reason"])
	}
}

// BookImported is what the importer calls: it sends requestFulfilled when the
// request's book is imported, and only the first time.
func TestRequestsBookImported_SendsRequestFulfilledOnce(t *testing.T) {
	f := newRequestsFixture(t, wellsRequestStub(), nil)
	ctx := context.Background()
	capture := lifecycleCapture(f)
	created := decodeRequest(t, f.create(t, f.requester, `{"kind":"book","foreignId":"OL27482W","mediaType":"ebook"}`))
	capture.next(t, notifier.EventRequestCreated)
	approved := decodeRequest(t, f.approve(f.admin, created.ID, `{"searchOnAdd":false}`))
	if approved.ResultBookID == nil {
		t.Fatalf("approved = %+v, want a book", approved)
	}
	capture.next(t, notifier.EventRequestApproved)
	capture.none(t, "an approval of a book with no file")

	if _, err := f.database.ExecContext(ctx, "UPDATE books SET status = 'imported' WHERE id = ?", *approved.ResultBookID); err != nil {
		t.Fatal(err)
	}
	f.h.BookImported(ctx, *approved.ResultBookID)
	if p := capture.next(t, notifier.EventRequestFulfilled); p["requestId"] != created.ID || p["userId"] != f.requester.ID {
		t.Errorf("payload = %+v", p)
	}
	f.h.BookImported(ctx, *approved.ResultBookID)
	capture.none(t, "a second import")
}
