-- +migrate Up

-- Request lifecycle webhooks (#4054): requestApproved, requestDeclined and
-- requestFulfilled. Off for every existing notification, like
-- on_request_created in 089.
ALTER TABLE notifications ADD COLUMN on_request_approved INTEGER NOT NULL DEFAULT 0;
ALTER TABLE notifications ADD COLUMN on_request_declined INTEGER NOT NULL DEFAULT 0;
ALTER TABLE notifications ADD COLUMN on_request_fulfilled INTEGER NOT NULL DEFAULT 0;

-- When what a request asked for first reached the library as a file. It is set
-- once, which is what makes requestFulfilled fire once. NULL means not yet.
ALTER TABLE requests ADD COLUMN fulfilled_at DATETIME;

-- Requests that are fulfilled already are stamped here, so an upgrade does not
-- announce them. The rule is the one GET /requests reports as "fulfilled".
UPDATE requests SET fulfilled_at = COALESCE(decided_at, updated_at)
WHERE status = 'approved' AND (
    (kind = 'book' AND result_book_id IN (SELECT id FROM books WHERE status = 'imported'))
    OR (kind = 'author' AND result_author_id IN (SELECT author_id FROM books WHERE status = 'imported' AND excluded = 0))
);

-- +migrate Down

ALTER TABLE requests DROP COLUMN fulfilled_at;
ALTER TABLE notifications DROP COLUMN on_request_fulfilled;
ALTER TABLE notifications DROP COLUMN on_request_declined;
ALTER TABLE notifications DROP COLUMN on_request_approved;
