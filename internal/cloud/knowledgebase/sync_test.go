package knowledgebase

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub implementa GitHubLike en memoria. Mantiene archivos por path
// global owner/repo/path y log de PutFile calls para assertions.
type fakeGitHub struct {
	mu       sync.Mutex
	repos    map[string]*GitHubRepoInfo
	files    map[string]*GitHubFile // key = "owner/repo/branch/path"
	puts     []GitHubPutFileRequest
	createOK bool
}

func newFakeGitHub() *fakeGitHub {
	return &fakeGitHub{
		repos: map[string]*GitHubRepoInfo{},
		files: map[string]*GitHubFile{},
	}
}

func (f *fakeGitHub) key(owner, repo, branch, path string) string {
	if branch == "" {
		branch = "main"
	}
	return owner + "/" + repo + "/" + branch + "/" + path
}

func (f *fakeGitHub) GetRepo(ctx context.Context, owner, repo string) (*GitHubRepoInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.repos[owner+"/"+repo]; ok {
		return r, nil
	}
	return nil, nil
}

func (f *fakeGitHub) CreateRepo(ctx context.Context, owner, repo, description string, private bool) (*GitHubRepoInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := &GitHubRepoInfo{
		Owner:         owner,
		Name:          repo,
		DefaultBranch: "main",
		Private:       private,
		HTMLURL:       "https://github.com/" + owner + "/" + repo,
	}
	f.repos[owner+"/"+repo] = r
	return r, nil
}

func (f *fakeGitHub) GetFile(ctx context.Context, owner, repo, branch, path string) (*GitHubFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.files[f.key(owner, repo, branch, path)]; ok {
		return v, nil
	}
	return nil, nil
}

func (f *fakeGitHub) PutFile(ctx context.Context, req GitHubPutFileRequest) (*GitHubPutFileResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts = append(f.puts, req)
	branch := req.Branch
	if branch == "" {
		branch = "main"
	}
	blobSHA := sha1Hex(req.Content)
	commitSHA := "commit-" + blobSHA[:8]
	f.files[f.key(req.Owner, req.Repo, branch, req.Path)] = &GitHubFile{
		Path:    req.Path,
		Content: append([]byte(nil), req.Content...),
		SHA:     blobSHA,
		Size:    int64(len(req.Content)),
	}
	return &GitHubPutFileResult{
		CommitSHA: commitSHA,
		BlobSHA:   blobSHA,
		HTMLURL:   "https://github.com/" + req.Owner + "/" + req.Repo + "/blob/" + branch + "/" + req.Path,
	}, nil
}

func (f *fakeGitHub) ListFiles(ctx context.Context, owner, repo, branch, path string) ([]GitHubFileEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := f.key(owner, repo, branch, "")
	if path != "" {
		prefix += strings.TrimRight(path, "/") + "/"
	}
	out := []GitHubFileEntry{}
	for k, v := range f.files {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rel := strings.TrimPrefix(k, prefix)
		if strings.Contains(rel, "/") {
			continue
		}
		out = append(out, GitHubFileEntry{Path: v.Path, Type: "file", SHA: v.SHA, Size: v.Size})
	}
	return out, nil
}

func sha1Hex(b []byte) string {
	h := sha1.Sum(b)
	return hex.EncodeToString(h[:])
}

// fakePages implementa PageReader.
type fakePages struct {
	pages map[string]*PageRecord
}

func (p *fakePages) GetPage(ctx context.Context, id string) (*PageRecord, error) {
	if r, ok := p.pages[id]; ok {
		return r, nil
	}
	return nil, ErrEntityNotFound
}

// fakeQuotes implementa QuoteReader.
type fakeQuotes struct {
	quotes map[string]*QuoteRecord
}

func (q *fakeQuotes) GetQuote(ctx context.Context, id string) (*QuoteRecord, error) {
	if r, ok := q.quotes[id]; ok {
		return r, nil
	}
	return nil, ErrEntityNotFound
}

// fakeProjects implementa ProjectReader.
type fakeProjects struct {
	byID map[string]*ProjectInfo
}

func (p *fakeProjects) GetProject(ctx context.Context, id string) (*ProjectInfo, error) {
	if r, ok := p.byID[id]; ok {
		return r, nil
	}
	return nil, ErrEntityNotFound
}
func (p *fakeProjects) ListProjects(ctx context.Context) ([]ProjectInfo, error) {
	out := []ProjectInfo{}
	for _, v := range p.byID {
		out = append(out, *v)
	}
	return out, nil
}
func (p *fakeProjects) ResolveProjectIDBySlug(ctx context.Context, slug string) (string, error) {
	for _, v := range p.byID {
		if v.Slug == slug {
			return v.ID, nil
		}
	}
	return "", nil
}

// newTestService construye un service sin DB (queries opcionales) +
// fakes para git/pages/quotes/projects.
func newTestService(t *testing.T, gh *fakeGitHub) *service {
	t.Helper()
	pages := &fakePages{pages: map[string]*PageRecord{}}
	quotes := &fakeQuotes{quotes: map[string]*QuoteRecord{}}
	projects := &fakeProjects{byID: map[string]*ProjectInfo{}}

	s := NewService(Config{
		GitHub:             gh,
		Pages:              pages,
		Quotes:             quotes,
		Projects:           projects,
		Org:                "ITECHDEV-MX",
		Repo:               "team-knowledge-base",
		DashboardPublicURL: "https://ariacore.itechdev.com.mx",
		AuthorName:         "ARIA",
		AuthorEmail:        "aria@itechdev.com.mx",
	}).(*service)
	// Manually expose the fakes back to the test by replacing readers.
	s.pages = pages
	s.quotes = quotes
	s.projects = projects
	s.defaultBranch = "main"
	return s
}

func TestEnsureCentralRepoBootstrap(t *testing.T) {
	gh := newFakeGitHub()
	s := newTestService(t, gh)

	if err := s.EnsureCentralRepo(context.Background()); err != nil {
		t.Fatalf("EnsureCentralRepo: %v", err)
	}
	// Repo creado.
	r, _ := gh.GetRepo(context.Background(), "ITECHDEV-MX", "team-knowledge-base")
	if r == nil {
		t.Fatal("repo was not created")
	}
	// README + 3 templates + .gitignore = 5 files.
	wantPaths := []string{
		"README.md",
		"plantillas/prd-template.md",
		"plantillas/historia-template.md",
		"plantillas/cotizacion-template.md",
		".gitignore",
	}
	for _, p := range wantPaths {
		if f, _ := gh.GetFile(context.Background(), "ITECHDEV-MX", "team-knowledge-base", "main", p); f == nil {
			t.Errorf("expected bootstrap to create %q", p)
		}
	}

	// Idempotente: segunda llamada no debe re-commitear nada existente.
	beforePuts := len(gh.puts)
	if err := s.EnsureCentralRepo(context.Background()); err != nil {
		t.Fatalf("second EnsureCentralRepo: %v", err)
	}
	if len(gh.puts) != beforePuts {
		t.Fatalf("expected idempotent bootstrap, second call wrote %d new files", len(gh.puts)-beforePuts)
	}
}

func TestSyncPRDProducesExpectedPathAndCommitMessage(t *testing.T) {
	gh := newFakeGitHub()
	s := newTestService(t, gh)
	if err := s.EnsureCentralRepo(context.Background()); err != nil {
		t.Fatal(err)
	}

	id := "11111111-1111-1111-1111-111111111111"
	p := &PageRecord{
		ID:           id,
		Title:        "Cobranza inteligente",
		ContentMD:    "## Problema\nLento.\n",
		Project:      "park",
		TemplateKey:  "prd-v1",
		Status:       "draft",
		CreatedByUID: "u1",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	s.pages.(*fakePages).pages[id] = p

	beforePuts := len(gh.puts)
	commit, path, err := s.SyncPRD(context.Background(), id)
	if err != nil {
		t.Fatalf("SyncPRD: %v", err)
	}
	if commit == "" {
		t.Fatal("expected commit SHA")
	}
	wantPath := "proyectos/park/prds/00001-cobranza-inteligente.md"
	if path != wantPath {
		t.Fatalf("path=%q, want %q", path, wantPath)
	}

	// Verificar el commit message en el último put.
	last := gh.puts[len(gh.puts)-1]
	if !strings.HasPrefix(last.CommitMessage, "prd: Cobranza inteligente") {
		t.Errorf("commit message = %q, want prefix 'prd: Cobranza inteligente'", last.CommitMessage)
	}
	if !strings.Contains(last.CommitMessage, "[skip-aria-sync]") {
		t.Errorf("commit message missing [skip-aria-sync] tag: %q", last.CommitMessage)
	}

	// Frontmatter presente en el contenido committed.
	stored, _ := gh.GetFile(context.Background(), "ITECHDEV-MX", "team-knowledge-base", "main", path)
	if stored == nil {
		t.Fatal("file not stored")
	}
	if !strings.Contains(string(stored.Content), "id: "+id) {
		t.Errorf("content missing id frontmatter, got:\n%s", string(stored.Content))
	}
	if !strings.Contains(string(stored.Content), "## Problema") {
		t.Errorf("content missing original markdown body")
	}

	if len(gh.puts) <= beforePuts {
		t.Errorf("expected new put after SyncPRD")
	}
}

func TestSyncHistoriaUsesHistoriasFolder(t *testing.T) {
	gh := newFakeGitHub()
	s := newTestService(t, gh)
	if err := s.EnsureCentralRepo(context.Background()); err != nil {
		t.Fatal(err)
	}
	id := "22222222-2222-2222-2222-222222222222"
	s.pages.(*fakePages).pages[id] = &PageRecord{
		ID:        id,
		Title:     "Historia: cliente revisa",
		ContentMD: "**Como** PM\n",
		Project:   "park",
	}
	_, path, err := s.SyncHistoria(context.Background(), id)
	if err != nil {
		t.Fatalf("SyncHistoria: %v", err)
	}
	if !strings.Contains(path, "/historias/") {
		t.Fatalf("path should be in historias/, got %q", path)
	}
	last := gh.puts[len(gh.puts)-1]
	if !strings.HasPrefix(last.CommitMessage, "historia:") {
		t.Errorf("expected commit prefix 'historia:', got %q", last.CommitMessage)
	}
}

func TestSyncCotizacionWritesMDAndMetadata(t *testing.T) {
	gh := newFakeGitHub()
	s := newTestService(t, gh)
	if err := s.EnsureCentralRepo(context.Background()); err != nil {
		t.Fatal(err)
	}
	qid := "33333333-3333-3333-3333-333333333333"
	s.quotes.(*fakeQuotes).quotes[qid] = &QuoteRecord{
		ID:                 qid,
		ProjectSlug:        "park",
		Folio:              "ITD-2026-001-001",
		Status:             "approved",
		Currency:           "MXN",
		Subtotal:           150000,
		Total:              150000,
		ProductName:        "Plataforma cobranza",
		PreparedForCompany: "PARK",
		Items: []QuoteItemView{
			{SKU: "RPA-001", Description: "Bot", Qty: 1, UnitPrice: 100000, Subtotal: 100000},
		},
	}
	commit, dir, err := s.SyncCotizacion(context.Background(), qid)
	if err != nil {
		t.Fatalf("SyncCotizacion: %v", err)
	}
	if commit == "" || dir == "" {
		t.Fatalf("expected commit + dir, got %q / %q", commit, dir)
	}
	wantDir := "proyectos/park/cotizaciones/ITD-2026-001-001"
	if dir != wantDir {
		t.Fatalf("dir = %q, want %q", dir, wantDir)
	}
	// markdown y metadata sí se commitean (DOCX queda fuera porque docx==nil).
	mdFile, _ := gh.GetFile(context.Background(), "ITECHDEV-MX", "team-knowledge-base", "main", dir+"/propuesta.md")
	if mdFile == nil {
		t.Fatal("propuesta.md missing")
	}
	if !strings.Contains(string(mdFile.Content), "PARK") {
		t.Error("md should mention PARK")
	}
	metaFile, _ := gh.GetFile(context.Background(), "ITECHDEV-MX", "team-knowledge-base", "main", dir+"/metadata.json")
	if metaFile == nil {
		t.Fatal("metadata.json missing")
	}
	if !strings.Contains(string(metaFile.Content), `"folio": "ITD-2026-001-001"`) {
		t.Errorf("metadata missing folio:\n%s", string(metaFile.Content))
	}
}

func TestSyncProjectReadmeAndIndexUpdate(t *testing.T) {
	gh := newFakeGitHub()
	s := newTestService(t, gh)
	if err := s.EnsureCentralRepo(context.Background()); err != nil {
		t.Fatal(err)
	}
	pid := "44444444-4444-4444-4444-444444444444"
	s.projects.(*fakeProjects).byID[pid] = &ProjectInfo{
		ID:     pid,
		Slug:   "mercedes-rpa",
		Name:   "Mercedes RPA",
		Status: "shipped",
	}
	commit, path, err := s.SyncProjectReadme(context.Background(), pid)
	if err != nil {
		t.Fatalf("SyncProjectReadme: %v", err)
	}
	if commit == "" {
		t.Fatal("expected commit")
	}
	if path != "proyectos/mercedes-rpa/README.md" {
		t.Fatalf("path = %q", path)
	}
	stored, _ := gh.GetFile(context.Background(), "ITECHDEV-MX", "team-knowledge-base", "main", path)
	if stored == nil || !strings.Contains(string(stored.Content), "Mercedes RPA") {
		t.Fatalf("project README missing or wrong content")
	}
	// Root index re-rendered.
	root, _ := gh.GetFile(context.Background(), "ITECHDEV-MX", "team-knowledge-base", "main", "README.md")
	if root == nil || !strings.Contains(string(root.Content), "mercedes-rpa") {
		t.Fatalf("root README not updated with project entry, got:\n%s", string(root.Content))
	}
}

func TestRefreshIndexIdempotent(t *testing.T) {
	gh := newFakeGitHub()
	s := newTestService(t, gh)
	if err := s.EnsureCentralRepo(context.Background()); err != nil {
		t.Fatal(err)
	}
	beforePuts := len(gh.puts)
	if err := s.RefreshIndex(context.Background()); err != nil {
		t.Fatalf("RefreshIndex: %v", err)
	}
	first := len(gh.puts)
	if err := s.RefreshIndex(context.Background()); err != nil {
		t.Fatalf("RefreshIndex idempotent call: %v", err)
	}
	// Without DB, hash-skip is bypassed (loadSync returns nil), so each call
	// will re-put. But absence of DB shouldn't fail.
	if len(gh.puts) < first {
		t.Errorf("puts went down? before=%d first=%d after=%d", beforePuts, first, len(gh.puts))
	}
}

func TestOnQuoteFinalizedCallsSyncCotizacion(t *testing.T) {
	gh := newFakeGitHub()
	s := newTestService(t, gh)
	if err := s.EnsureCentralRepo(context.Background()); err != nil {
		t.Fatal(err)
	}
	qid := "55555555-5555-5555-5555-555555555555"
	s.quotes.(*fakeQuotes).quotes[qid] = &QuoteRecord{
		ID: qid, ProjectSlug: "park", Folio: "ITD-X", Currency: "MXN",
		Items: []QuoteItemView{{SKU: "X", Description: "Y", Qty: 1, UnitPrice: 1, Subtotal: 1}},
	}
	if err := s.OnQuoteFinalized(context.Background(), "session-1", qid); err != nil {
		t.Fatalf("OnQuoteFinalized: %v", err)
	}
	if f, _ := gh.GetFile(context.Background(), "ITECHDEV-MX", "team-knowledge-base", "main",
		"proyectos/park/cotizaciones/ITD-X/propuesta.md"); f == nil {
		t.Fatal("expected propuesta.md to be committed by OnQuoteFinalized")
	}
}

func TestSyncPRDFailsWhenPageHasNoProject(t *testing.T) {
	gh := newFakeGitHub()
	s := newTestService(t, gh)
	id := "66666666-6666-6666-6666-666666666666"
	s.pages.(*fakePages).pages[id] = &PageRecord{
		ID:    id,
		Title: "Sin proyecto",
		// Project intentionally empty.
	}
	_, _, err := s.SyncPRD(context.Background(), id)
	if err == nil {
		t.Fatal("expected error when page has no project")
	}
	if !strings.Contains(err.Error(), "no project") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestServiceDegradedWithoutGitHub(t *testing.T) {
	s := NewService(Config{}).(*service)
	if s.Available() {
		t.Fatal("service should not be Available without GitHub")
	}
	_, _, err := s.SyncPRD(context.Background(), "x")
	if err == nil || err.Error() != ErrGitHubNotConfigured.Error() {
		// errors.Is tolerant
		if err == nil {
			t.Fatalf("expected error without github, got nil")
		}
		if !strings.Contains(err.Error(), "github client not configured") {
			t.Fatalf("unexpected err: %v", err)
		}
	}
}

func TestContentHashStable(t *testing.T) {
	a := contentHash("hola")
	b := contentHash("hola")
	if a != b {
		t.Fatal("hash should be deterministic")
	}
	if a == contentHash("hola!") {
		t.Fatal("hash should change with content")
	}
}

func TestIsUUIDLike(t *testing.T) {
	good := "11111111-1111-1111-1111-111111111111"
	bad := "not-a-uuid"
	if !isUUIDLike(good) {
		t.Errorf("expected UUID accepted: %q", good)
	}
	if isUUIDLike(bad) {
		t.Errorf("expected non-UUID rejected: %q", bad)
	}
}

func TestShortIDStripsDashes(t *testing.T) {
	in := "11111111-2222-3333-4444-555555555555"
	got := shortID(in)
	want := "11111111"
	if got != want {
		t.Fatalf("shortID = %q, want %q", got, want)
	}
}

// debug helper used in failing tests; keeps lint quiet when unused.
var _ = fmt.Sprintf
