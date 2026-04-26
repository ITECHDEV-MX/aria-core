package attachments

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// openTestDB mimics vault_test.go's helper. Tests are gated on ARIA_CORE_TEST_DSN
// because they require a running Postgres instance.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("ARIA_CORE_TEST_DSN"))
	if dsn == "" {
		t.Skip("ARIA_CORE_TEST_DSN not set; skipping integration test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Skipf("open db failed (skipping): %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("ping db failed (skipping): %v", err)
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestUploadEnforcesMimeWhitelist verifies that an unsupported MIME (video/mp4
// magic bytes) is rejected with ErrMIMENotAllowed.
func TestUploadEnforcesMimeWhitelist(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	dir := t.TempDir()
	storage, err := NewFilesystemStorage(dir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	store := NewAttachmentStore(db, storage, Config{})

	// MP4 ftyp header — DetectContentType returns video/mp4.
	mp4Header := []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0, 0, 0, 0, 'm', 'p', '4', '2', 'i', 's', 'o', 'm'}
	body := append(mp4Header, []byte(strings.Repeat("x", 1024))...)
	_, err = store.Upload(context.Background(), UploadParams{
		PageID:           uuid.NewString(),
		OriginalFilename: "evil.mp4",
		Body:             strings.NewReader(string(body)),
		UploadedByUID:    uuid.NewString(),
	})
	if !errors.Is(err, ErrMIMENotAllowed) {
		t.Errorf("want ErrMIMENotAllowed, got %v", err)
	}
}

// TestUploadEnforcesMaxBytes verifies that exceeding MaxBytes triggers
// ErrFileTooBig and the file is rolled back via SoftDelete.
func TestUploadEnforcesMaxBytes(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	dir := t.TempDir()
	storage, err := NewFilesystemStorage(dir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	store := NewAttachmentStore(db, storage, Config{MaxBytes: 1024})

	// 2KB PNG-ish payload — first bytes are valid PNG magic so MIME is OK.
	pngMagic := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	body := append(pngMagic, []byte(strings.Repeat("a", 4096))...)
	_, err = store.Upload(context.Background(), UploadParams{
		PageID:           uuid.NewString(),
		OriginalFilename: "big.png",
		Body:             strings.NewReader(string(body)),
		UploadedByUID:    uuid.NewString(),
	})
	if !errors.Is(err, ErrFileTooBig) {
		t.Errorf("want ErrFileTooBig, got %v", err)
	}
}

// TestUploadHappyPathPersistsRow verifies that a valid PNG flows through:
// stored on disk, sha256 computed, row inserted, listable.
func TestUploadHappyPathPersistsRow(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	dir := t.TempDir()
	storage, err := NewFilesystemStorage(dir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	store := NewAttachmentStore(db, storage, Config{})
	ctx := context.Background()

	pageID := uuid.NewString()
	pngMagic := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	body := append(pngMagic, []byte("fake png body")...)
	att, err := store.Upload(ctx, UploadParams{
		PageID:           pageID,
		OriginalFilename: "hello.png",
		Body:             strings.NewReader(string(body)),
		UploadedByUID:    uuid.NewString(),
		Description:      "screenshot",
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if att.MIMEType != "image/png" {
		t.Errorf("mime = %q, want image/png", att.MIMEType)
	}
	if att.SizeBytes != int64(len(body)) {
		t.Errorf("size = %d, want %d", att.SizeBytes, len(body))
	}
	if att.SHA256 == "" {
		t.Error("sha256 must be set")
	}
	if att.Description != "screenshot" {
		t.Errorf("description = %q", att.Description)
	}

	// List by page returns the row.
	list, err := store.ListByPage(ctx, pageID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != att.ID {
		t.Errorf("list = %+v, want one item with id %s", list, att.ID)
	}
}

// TestUploadDedupBySha256 verifies that uploading the same body twice for the
// same page returns the existing row instead of inserting a duplicate.
func TestUploadDedupBySha256(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	dir := t.TempDir()
	storage, err := NewFilesystemStorage(dir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	store := NewAttachmentStore(db, storage, Config{})
	ctx := context.Background()

	pageID := uuid.NewString()
	pngMagic := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	body := append(pngMagic, []byte("dedup body")...)
	att1, err := store.Upload(ctx, UploadParams{
		PageID:           pageID,
		OriginalFilename: "x.png",
		Body:             strings.NewReader(string(body)),
		UploadedByUID:    uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("upload1: %v", err)
	}
	att2, err := store.Upload(ctx, UploadParams{
		PageID:           pageID,
		OriginalFilename: "x.png",
		Body:             strings.NewReader(string(body)),
		UploadedByUID:    uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("upload2: %v", err)
	}
	if att1.ID != att2.ID {
		t.Errorf("dedup failed: ids %s vs %s", att1.ID, att2.ID)
	}

	// And the table really only contains one row for that sha.
	list, _ := store.ListByPage(ctx, pageID)
	if len(list) != 1 {
		t.Errorf("list len = %d, want 1", len(list))
	}
}

// TestDeleteSoftDeletes verifies that delete sets is_deleted=TRUE and removes
// the row from ListByPage.
func TestDeleteSoftDeletes(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	dir := t.TempDir()
	storage, err := NewFilesystemStorage(dir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	store := NewAttachmentStore(db, storage, Config{})
	ctx := context.Background()

	pageID := uuid.NewString()
	pngMagic := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	att, err := store.Upload(ctx, UploadParams{
		PageID:           pageID,
		OriginalFilename: "tombstone.png",
		Body:             strings.NewReader(string(append(pngMagic, []byte("body")...))),
		UploadedByUID:    uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if err := store.Delete(ctx, att.ID, uuid.NewString()); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Get returns the soft-deleted row with IsDeleted=true.
	got, err := store.Get(ctx, att.ID)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if !got.IsDeleted {
		t.Error("IsDeleted should be true")
	}
	// ListByPage skips soft-deleted rows.
	list, _ := store.ListByPage(ctx, pageID)
	if len(list) != 0 {
		t.Errorf("list len = %d, want 0", len(list))
	}
}

// ─── Share-link tests ──────────────────────────────────────────────────────

func TestShareTokenUniqueness(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	st := NewPgShareStore(db)
	ctx := context.Background()
	pageID := uuid.NewString()
	creator := uuid.NewString()

	seen := map[string]struct{}{}
	for i := 0; i < 20; i++ {
		link, err := st.Create(ctx, CreateShareParams{PageID: pageID, CreatedByUID: creator})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, dup := seen[link.Token]; dup {
			t.Fatalf("duplicate token: %s", link.Token)
		}
		seen[link.Token] = struct{}{}
	}
}

func TestShareExpiryEnforced(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	st := NewPgShareStore(db)
	ctx := context.Background()
	pageID := uuid.NewString()
	creator := uuid.NewString()

	past := time.Now().UTC().Add(-time.Hour)
	link, err := st.Create(ctx, CreateShareParams{
		PageID: pageID, CreatedByUID: creator, ExpiresAt: &past,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = st.Resolve(ctx, link.Token, "", "127.0.0.1", "test-ua")
	if !errors.Is(err, ErrShareExpired) {
		t.Errorf("want ErrShareExpired, got %v", err)
	}
}

func TestSharePasswordGate(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	st := NewPgShareStore(db)
	ctx := context.Background()
	pageID := uuid.NewString()
	creator := uuid.NewString()

	link, err := st.Create(ctx, CreateShareParams{
		PageID: pageID, CreatedByUID: creator, Password: "secret123",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !link.HasPassword {
		t.Errorf("HasPassword should be true")
	}
	// Wrong password rejected.
	if _, err := st.Resolve(ctx, link.Token, "wrong", "1.1.1.1", ""); !errors.Is(err, ErrSharePasswordRequired) {
		t.Errorf("wrong pw: want ErrSharePasswordRequired, got %v", err)
	}
	// No password rejected.
	if _, err := st.Resolve(ctx, link.Token, "", "1.1.1.1", ""); !errors.Is(err, ErrSharePasswordRequired) {
		t.Errorf("empty pw: want ErrSharePasswordRequired, got %v", err)
	}
	// Correct password accepted.
	if _, err := st.Resolve(ctx, link.Token, "secret123", "1.1.1.1", ""); err != nil {
		t.Errorf("correct pw: got %v", err)
	}
}

func TestShareViewCountIncrementsAtomically(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	st := NewPgShareStore(db)
	ctx := context.Background()
	link, err := st.Create(ctx, CreateShareParams{
		PageID: uuid.NewString(), CreatedByUID: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := st.Resolve(ctx, link.Token, "", "1.1.1.1", ""); err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
	}
	got, err := st.Get(ctx, link.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ViewCount != 5 {
		t.Errorf("view_count = %d, want 5", got.ViewCount)
	}
}

func TestShareRevokeBlocksResolve(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	st := NewPgShareStore(db)
	ctx := context.Background()
	link, err := st.Create(ctx, CreateShareParams{
		PageID: uuid.NewString(), CreatedByUID: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.Revoke(ctx, link.ID, uuid.NewString()); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := st.GetByToken(ctx, link.Token); !errors.Is(err, ErrShareNotFound) {
		t.Errorf("revoked GetByToken: want ErrShareNotFound, got %v", err)
	}
}

func TestShareConfidentialBlocked(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	st := NewPgShareStore(db)
	st.SetSensitivityResolver(func(ctx context.Context, pageID string) (string, error) {
		return "confidential", nil
	})
	ctx := context.Background()
	_, err := st.Create(ctx, CreateShareParams{
		PageID: uuid.NewString(), CreatedByUID: uuid.NewString(),
	})
	if !errors.Is(err, ErrShareForbiddenSensitivity) {
		t.Errorf("want ErrShareForbiddenSensitivity, got %v", err)
	}
}
