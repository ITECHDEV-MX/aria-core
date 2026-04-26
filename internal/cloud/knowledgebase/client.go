// Package knowledgebase implementa la wave 8 de ARIA Core: sincronización
// automática del knowledge del equipo (PRDs, historias, cotizaciones,
// project READMEs, root index) a un repo central GitHub
// (ITECHDEV-MX/team-knowledge-base por default).
//
// El paquete expone:
//
//   - Service: API de alto nivel (EnsureCentralRepo, SyncPRD, SyncHistoria,
//     SyncCotizacion, SyncProjectReadme, GenerateQuoteDOCX, RefreshIndex).
//   - GitHubLike: contrato mínimo del cliente GitHub. Wave 7 está construyendo
//     internal/cloud/github/; aquí solo definimos la interface para no acoplar.
//     Al merge time un adapter en cmd/aria-core implementa el wiring contra
//     el cliente concreto.
//   - StandardProjectReadme: helper que wave 7 puede importar para generar
//     el README.md de cada repo individual.
//   - QuoteChatHook: contrato OnFinalize que la chat orchestrator (wave 6)
//     puede invocar al cerrar una cotización para disparar el sync.
//
// Pandoc es prerequisito para GenerateQuoteDOCX. El paquete verifica
// `which pandoc` al startup y deja fallar el export con un error claro
// cuando no está instalado.
package knowledgebase

import (
	"context"
	"errors"
)

// Errores públicos del paquete.
var (
	// ErrGitHubNotConfigured se devuelve cuando GitHubLike no fue inyectado.
	ErrGitHubNotConfigured = errors.New("knowledgebase: github client not configured")
	// ErrEntityNotFound se devuelve cuando el page/quote/project que pediste
	// sincronizar no existe en la DB.
	ErrEntityNotFound = errors.New("knowledgebase: entity not found")
	// ErrPandocNotAvailable se devuelve cuando GenerateQuoteDOCX se invoca
	// y `which pandoc` no localiza el binario.
	ErrPandocNotAvailable = errors.New("knowledgebase: pandoc binary not available — install pandoc (apt install pandoc)")
)

// GitHubFile es el resultado de GetFile — incluye contenido y SHA del blob
// (necesario para PutFile updates idempotentes).
type GitHubFile struct {
	Path    string
	Content []byte
	SHA     string // blob SHA del archivo, para enviar en updates
	Size    int64
}

// GitHubFileEntry es una entrada del listado de archivos de un directorio.
type GitHubFileEntry struct {
	Path string
	Type string // "file" | "dir"
	SHA  string
	Size int64
}

// GitHubPutFileRequest captura los inputs de una creación/actualización de
// archivo en el repo central. Los implementadores son responsables de
// detectar si es create (no SHA previo) o update (SHA previo presente).
type GitHubPutFileRequest struct {
	Owner         string
	Repo          string
	Branch        string // si vacío, usa default branch
	Path          string
	Content       []byte
	CommitMessage string
	// PreviousSHA es el SHA del blob anterior si estamos actualizando.
	// Si vacío, el adapter debe intentar GetFile primero para detectar
	// idempotencia o crear el archivo desde cero.
	PreviousSHA string
	AuthorName  string
	AuthorEmail string
}

// GitHubPutFileResult contiene el SHA del commit creado y el SHA del blob
// resultante (para actualizaciones futuras).
type GitHubPutFileResult struct {
	CommitSHA string
	BlobSHA   string
	HTMLURL   string // URL al archivo en GitHub web UI
}

// GitHubLike es el contrato mínimo que el knowledgebase consume del cliente
// GitHub que wave 7 construye en internal/cloud/github/. Está aislado
// para permitir tests con un mock y para que ambos worktrees mergeen sin
// pisar lo que cada uno escribe.
type GitHubLike interface {
	// GetRepo retorna nil si el repo no existe (sin error). Si existe,
	// retorna metadata mínima.
	GetRepo(ctx context.Context, owner, repo string) (*GitHubRepoInfo, error)

	// CreateRepo crea el repo dentro de la org dada. La implementación elige
	// si visibilidad public/private — para team-knowledge-base default es
	// private (org-only).
	CreateRepo(ctx context.Context, owner, repo, description string, private bool) (*GitHubRepoInfo, error)

	// GetFile retorna el contenido del archivo + blob SHA. Si no existe,
	// retorna (nil, nil) — NO error.
	GetFile(ctx context.Context, owner, repo, branch, path string) (*GitHubFile, error)

	// PutFile crea o actualiza un archivo. Si PreviousSHA está vacío,
	// el adapter primero intenta GetFile y, si encuentra, hace update.
	PutFile(ctx context.Context, req GitHubPutFileRequest) (*GitHubPutFileResult, error)

	// ListFiles lista los archivos en path (no recursivo). Si path es ""
	// lista la raíz.
	ListFiles(ctx context.Context, owner, repo, branch, path string) ([]GitHubFileEntry, error)
}

// GitHubRepoInfo es la metadata mínima de un repo.
type GitHubRepoInfo struct {
	Owner         string
	Name          string
	DefaultBranch string
	Private       bool
	HTMLURL       string
	CloneURL      string
}
