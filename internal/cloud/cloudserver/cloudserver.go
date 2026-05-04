package cloudserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/chunkcodec"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/constants"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboardsession"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/knowledgebase"
	coreproject "github.com/ITECHDEV-MX/aria-core/internal/project"
	"github.com/ITECHDEV-MX/aria-core/internal/store"
	coresync "github.com/ITECHDEV-MX/aria-core/internal/sync"
)

type Option func(*CloudServer)

type ChunkStore interface {
	ReadManifest(ctx context.Context, project string) (*coresync.Manifest, error)
	WriteChunk(ctx context.Context, project, chunkID, createdBy, clientCreatedAt string, payload []byte) error
	ReadChunk(ctx context.Context, project, chunkID string) ([]byte, error)
	KnownSessionIDs(ctx context.Context, project string) (map[string]struct{}, error)
}

type Authenticator interface {
	Authorize(r *http.Request) error
}

type ProjectAuthorizer interface {
	AuthorizeProject(project string) error
}

type dashboardSessionCodec interface {
	MintDashboardSession(bearerToken string) (string, error)
	ParseDashboardSession(sessionToken string) (string, error)
}

type staticStatusProvider struct{ status dashboard.SyncStatus }

func (s staticStatusProvider) Status() dashboard.SyncStatus { return s.status }

type CloudServer struct {
	store          ChunkStore
	auth           Authenticator
	projectAuth    ProjectAuthorizer
	dashboardAdmin string
	port           int
	host           string
	mux            *http.ServeMux
	syncStatus     dashboard.SyncStatusProvider
	listenAndServe func(addr string, handler http.Handler) error
	sessionCodec   *dashboardsession.Codec
	userStore      DashboardUserService
	cotizador      dashboard.CotizadorService
	ariaMem        AriaMemService
	ariaMemDash    dashboard.AriaMemDashboardService
	pdfClient        dashboard.PDFClient
	email            EmailService
	invites          InviteService
	dashboardInvites dashboard.InviteDashboardService
	redactor         dashboard.RedactorService
	scrubber         ScrubGate
	publicURL        string
	vault            VaultService
	vaultDash        dashboard.VaultDashboardService
	roi              ROIService
	roiDash          dashboard.ROIService
	recipes          RecipeRunnerService
	pagesDash        dashboard.PagesDashboardService
	pageAttachments  PageAttachmentService
	pageShares       PageShareService
	pagePublicView   PagePublicViewService
	pageDB           PageDatabaseService
	pageComments     PageCommentsService
	quoteChat        dashboard.QuoteChatService
	welcomeMailer    dashboard.UserWelcomeMailer
	passwordSelf     dashboard.PasswordSelfService
	passwordResetMailer dashboard.PasswordResetMailerService
	personalCockpit  dashboard.PersonalCockpitService
	profile          dashboard.ProfileService
	teamProjects       TeamProjectsService
	teamProjectsCreate TeamProjectsCreateAdapter
	kb                 knowledgebase.Service
	kbDash             dashboard.KnowledgeBaseDashboardService
}

// ROIService es el contrato runtime del módulo ROI consumido por
// /v1/memory/search hook.
type ROIService interface {
	LogSearch(ctx context.Context, params ROILogSearchParams) error
}

// ROILogSearchParams espeja roi.LogSearchParams para evitar el import cíclico.
type ROILogSearchParams struct {
	Query          string
	ResultCount    int
	CanonHitCount  int
	TotalTokens    int
	TruncatedCount int
	DeveloperUID   string
	Project        string
	Scope          string
	ClientID       string
	DurationMs     int
}

// ScrubGate is the runtime contract used by /v1/memory/* handlers to scrub
// observation text before it reaches a Claude / OpenAI client. Mirrors a subset
// of redactor.Service so cloudserver does not import the redactor package.
type ScrubGate interface {
	// CanSendToLLM checks the policy gate for a sensitivity tag + provider.
	CanSendToLLM(sensitivity, provider string) bool
	// ScrubString runs PII scrubbing in tokens mode and returns (output, redactionsJSON).
	// On any error the original text is returned unmodified and an empty
	// redactions JSON is returned. This is fail-open for the dev path but
	// is paired with audit logging by LogEgress so the bypass is observable.
	ScrubString(ctx context.Context, text string) (string, string)
	// LogEgress records one row in aria_llm_egress_log.
	LogEgress(ctx context.Context, requestID, observationID, provider, model, clientID, userUID, reason, payloadHash string, payloadSize int, scrubbed bool, redactionsJSON string) error
}

// EmailService is the contract for sending transactional emails.
// The cotizador hooks call SendQuote* methods after status changes.
// All methods must be safe to call when the underlying client is not
// configured (no-op + log).
type EmailService interface {
	IsConfigured() bool
	PublicURL() string
	SendQuoteSent(ctx context.Context, qc EmailQuoteContext) error
	SendQuoteApproved(ctx context.Context, qc EmailQuoteContext, bccCreator string) error
	SendQuoteRejected(ctx context.Context, qc EmailQuoteContext, creatorEmail string) error
	SendQuoteExpiring(ctx context.Context, qc EmailQuoteContext, creatorEmail string) error
	SendInvite(ctx context.Context, ic EmailInviteContext) error
}

// EmailQuoteContext carries the data needed to render quote-event emails.
// Mirrors internal/cloud/email.QuoteContext so the cloudserver doesn't
// import the email package directly (kept clean by adapter).
type EmailQuoteContext struct {
	QuoteID                 string
	Folio                   string
	ProductName             string
	PreparedForCompany      string
	PreparedForContactName  string
	PreparedForContactEmail string
	Total                   float64
	Currency                string
	Status                  string
	ValidUntil              string
	PreparedByName          string
	PreparedByEmail         string
	Notes                   string
	PublicURL               string
}

// EmailInviteContext mirrors email.InviteContext.
type EmailInviteContext struct {
	Email     string
	Link      string
	ExpiresAt string
	InvitedBy string
	Roles     []string
}

// InviteService is the contract for the magic-link invite flow.
type InviteService interface {
	CreateInvite(ctx context.Context, email string, roles []string, invitedByUID string) (*InviteRecord, error)
	GetInvite(ctx context.Context, token string) (*InviteRecord, error)
	ConsumeInvite(ctx context.Context, token, password string) error
}

// InviteRecord is the cloudserver-facing view of a magic-link invite.
type InviteRecord struct {
	Token        string
	Email        string
	Roles        []string
	ExpiresAt    time.Time
	UsedAt       *time.Time
	InvitedByUID string
}

// IsUsable returns true if the invite is still valid (not used, not expired).
func (i *InviteRecord) IsUsable(now time.Time) bool {
	if i == nil {
		return false
	}
	if i.UsedAt != nil {
		return false
	}
	return now.Before(i.ExpiresAt)
}

// DashboardUserService es el contrato que cloudserver necesita para CRUD de users.
// Lo implementa internal/cloud/cloudusers.Store via adapter.
type DashboardUserService interface {
	VerifyPassword(ctx context.Context, email, password string) (*UserPrincipal, error)
	GetByUID(ctx context.Context, uid string) (*UserPrincipal, error)
	GetByEmail(ctx context.Context, email string) (*UserPrincipal, error)
	List(ctx context.Context) ([]*UserPrincipal, error)
	Create(ctx context.Context, email, name string, roles []string, password string) (*UserPrincipal, error)
	AddRole(ctx context.Context, uid, role string) error
	RemoveRole(ctx context.Context, uid, role string) error
	SetActive(ctx context.Context, uid string, active bool) error
	ChangePassword(ctx context.Context, uid, newPassword string) error
}

// UserPrincipal es la representación del usuario autenticado en el dashboard.
type UserPrincipal struct {
	UID       string
	Email     string
	Name      string
	Roles     []string
	IsActive  bool
	CreatedAt time.Time
}

const defaultHost = "127.0.0.1"
const maxPushBodyBytes int64 = 8 * 1024 * 1024
const maxDashboardLoginBodyBytes int64 = 16 * 1024
const dashboardSessionCookieName = "aria-core_dashboard_token"

var ErrDashboardSessionCodecRequired = errors.New("dashboard session codec is required for dashboard auth")

func WithSyncStatusProvider(provider dashboard.SyncStatusProvider) Option {
	return func(s *CloudServer) {
		s.syncStatus = provider
	}
}

func WithHost(host string) Option {
	return func(s *CloudServer) {
		s.host = strings.TrimSpace(host)
	}
}

func WithProjectAuthorizer(authorizer ProjectAuthorizer) Option {
	return func(s *CloudServer) {
		s.projectAuth = authorizer
	}
}

func WithDashboardAdminToken(adminToken string) Option {
	return func(s *CloudServer) {
		s.dashboardAdmin = strings.TrimSpace(adminToken)
	}
}

// WithSessionCodec inyecta el codec JWT para sesiones del dashboard.
func WithSessionCodec(codec *dashboardsession.Codec) Option {
	return func(s *CloudServer) {
		s.sessionCodec = codec
	}
}

// WithUserStore inyecta el store de usuarios para login email+password y CRUD admin.
func WithUserStore(us DashboardUserService) Option {
	return func(s *CloudServer) {
		s.userStore = us
	}
}

// WithCotizador inyecta el servicio Cotizador para el dashboard.
func WithCotizador(c dashboard.CotizadorService) Option {
	return func(s *CloudServer) {
		s.cotizador = c
	}
}

// WithAriaMem inyecta el servicio de memoria ARIA Core (reemplazo del legacy mcp__aria__*).
func WithAriaMem(m AriaMemService) Option {
	return func(s *CloudServer) {
		s.ariaMem = m
	}
}

// WithAriaMemDashboard inyecta el servicio dashboard de memoria.
func WithAriaMemDashboard(m dashboard.AriaMemDashboardService) Option {
	return func(s *CloudServer) {
		s.ariaMemDash = m
	}
}

// WithPDFClient inyecta el cliente HTML→PDF (gotenberg).
func WithPDFClient(c dashboard.PDFClient) Option {
	return func(s *CloudServer) {
		s.pdfClient = c
	}
}

// WithEmailService inyecta el servicio de email para notificaciones.
func WithEmailService(e EmailService) Option {
	return func(s *CloudServer) {
		s.email = e
	}
}

// WithInviteService inyecta el servicio de magic-link invites.
func WithInviteService(i InviteService) Option {
	return func(s *CloudServer) {
		s.invites = i
	}
}

// WithDashboardInvites inyecta el servicio dashboard que combina creación
// de invite + envío de email para el handler /dashboard/admin/users/invite.
func WithDashboardInvites(d dashboard.InviteDashboardService) Option {
	return func(s *CloudServer) {
		s.dashboardInvites = d
	}
}

// WithWelcomeMailer inyecta el mailer que envía email de bienvenida cuando
// admin crea usuario manual con password (handler POST /dashboard/admin/users/create).
func WithWelcomeMailer(m dashboard.UserWelcomeMailer) Option {
	return func(s *CloudServer) {
		s.welcomeMailer = m
	}
}

// WithPasswordSelf inyecta el servicio self-service de password (cambiar/reset).
func WithPasswordSelf(p dashboard.PasswordSelfService) Option {
	return func(s *CloudServer) {
		s.passwordSelf = p
	}
}

// WithPasswordResetMailer inyecta el mailer para forgot-password emails.
func WithPasswordResetMailer(m dashboard.PasswordResetMailerService) Option {
	return func(s *CloudServer) {
		s.passwordResetMailer = m
	}
}

// WithPersonalCockpit inyecta el servicio que alimenta /dashboard/me.
func WithPersonalCockpit(p dashboard.PersonalCockpitService) Option {
	return func(s *CloudServer) {
		s.personalCockpit = p
	}
}

// WithProfile inyecta el servicio self-service de perfil.
func WithProfile(p dashboard.ProfileService) Option {
	return func(s *CloudServer) {
		s.profile = p
	}
}

// WithRedactor inyecta el servicio dashboard del módulo redactor (egress audit).
func WithRedactor(r dashboard.RedactorService) Option {
	return func(s *CloudServer) {
		s.redactor = r
	}
}

// WithScrubGate inyecta la capa runtime del redactor para v1_memory egress.
// Si nil, el handler de aria_search retorna observations sin scrub (sólo
// filtrado por sensitivity + log).
func WithScrubGate(g ScrubGate) Option {
	return func(s *CloudServer) {
		s.scrubber = g
	}
}

// WithPublicURL configura la URL pública usada para construir magic links.
func WithPublicURL(u string) Option {
	return func(s *CloudServer) {
		s.publicURL = strings.TrimSpace(u)
	}
}

// WithVaultDashboard inyecta el servicio dashboard del vault.
func WithVaultDashboard(v dashboard.VaultDashboardService) Option {
	return func(s *CloudServer) {
		s.vaultDash = v
	}
}

// WithROI inyecta el servicio runtime ROI. Cuando está presente, el handler
// /v1/memory/search registra cada call con stats (canon_hits, tokens) en
// aria_search_log para alimentar la métrica RDR.
func WithROI(r ROIService) Option {
	return func(s *CloudServer) {
		s.roi = r
	}
}

// WithROIDashboard inyecta el servicio dashboard del módulo ROI (vista
// /dashboard/roi). Si nil, el módulo ROI dashboard queda deshabilitado.
func WithROIDashboard(r dashboard.ROIService) Option {
	return func(s *CloudServer) {
		s.roiDash = r
	}
}

// WithPagesDashboard inyecta el servicio dashboard del módulo de páginas
// (mini-Notion). Si nil, /dashboard/pages y Cmd+K quedan deshabilitados.
func WithPagesDashboard(p dashboard.PagesDashboardService) Option {
	return func(s *CloudServer) {
		s.pagesDash = p
	}
}

// WithQuoteChat inyecta el servicio quote-chat (wave 6). Si nil, las rutas
// /dashboard/cotizador/quote-chat/* devuelven 503.
func WithQuoteChat(q dashboard.QuoteChatService) Option {
	return func(s *CloudServer) {
		s.quoteChat = q
	}
}

// WithKnowledgeBase inyecta el servicio knowledge-base sync (wave 8). Si nil,
// los endpoints /v1/knowledge-base/*, /v1/cotizador/quotes/{id}/export/* y la
// vista admin /dashboard/knowledge-base devuelven 503. El service también es
// el QuoteChatHook que la chat orchestrator dispara al finalizar una sesión.
func WithKnowledgeBase(kb knowledgebase.Service) Option {
	return func(s *CloudServer) {
		s.kb = kb
	}
}

// WithKnowledgeBaseDashboard inyecta el adapter dashboard del módulo KB.
// Separado de WithKnowledgeBase para que tests puedan inyectar mocks
// independientes.
func WithKnowledgeBaseDashboard(d dashboard.KnowledgeBaseDashboardService) Option {
	return func(s *CloudServer) {
		s.kbDash = d
	}
}

// AriaMemService es el contrato de la capa de memoria ARIA.
type AriaMemService interface {
	Save(ctx context.Context, p AriaMemSaveInput) (*AriaMemObservation, error)
	GetByID(ctx context.Context, id string) (*AriaMemObservation, error)
	Search(ctx context.Context, p AriaMemSearchInput) ([]*AriaMemObservation, error)
	// SearchWithBudget aplica ranking inteligente + token budget. Returns
	// (results, truncated_count, tokens_used, error). Si tokenBudget<=0,
	// se comporta como Search (sin truncar).
	SearchWithBudget(ctx context.Context, p AriaMemSearchInput, tokenBudget int, strategy string) (results []*AriaMemObservation, truncated int, tokens int, err error)
	Timeline(ctx context.Context, project string, since, until *time.Time, limit int) ([]*AriaMemObservation, error)
	PromoteCanon(ctx context.Context, id, byUID string) error
	RecordQuality(ctx context.Context, id, signal string, score float64, notes, byUID string) error
	StartSession(ctx context.Context, p AriaMemStartSessionInput) (*AriaMemSession, error)
	GetSession(ctx context.Context, id string) (*AriaMemSession, error)
	SaveSummary(ctx context.Context, p AriaMemSaveSummaryInput) error
	GetContextStatus(ctx context.Context, project string) (*AriaMemContextStatus, error)
	ListSkills(ctx context.Context, stack []string) ([]*AriaMemSkill, error)
	// GetSkillsRanked rankea por effectiveness + FTS sobre task_description, y
	// registra retrieval por cada skill devuelto.
	GetSkillsRanked(ctx context.Context, p AriaMemSkillsRankedInput) (*AriaMemSkillsRankedOutput, error)
	GetRecipes(ctx context.Context, taskDescription string, stack []string, limit int) ([]*AriaMemRecipe, error)
	// BuildSessionAutoContext arma el primer mensaje markdown con canon+skills+recipes+open sessions.
	BuildSessionAutoContext(ctx context.Context, p AriaMemAutoContextInput) (*AriaMemAutoContextOutput, error)
	// RecordSkillFeedback registra feedback explícito sobre un skill.
	RecordSkillFeedback(ctx context.Context, p AriaMemSkillFeedbackInput) error
}

type AriaMemSaveInput struct {
	SessionID, DeveloperUID, DeveloperRole, ClientID, Project, Scope    string
	ObservationType, Title, Subtitle, Narrative, Facts, Concepts        string
	FilesTouched, ReasoningTrace, TopicKey, Source, GeneratedByModel    string
	// Sensitivity (opcional). Si vacío, el redactor auto-infiere.
	Sensitivity string
	// ForceSave permite saltar el bloqueo por leak detection en aria_save.
	// Reservado para admin override; los clients normales no deben pasarlo.
	ForceSave bool
}

type AriaMemSearchInput struct {
	Query, Project, Scope, ObservationType string
	Limit                                  int
}

type AriaMemStartSessionInput struct {
	DeveloperUID, DeveloperEmail, DeveloperRole, ClientID, MachineID string
	Project, Directory, Goal                                          string
}

type AriaMemSaveSummaryInput struct {
	SessionID, Request, Investigated, Learned, Completed string
	NextSteps, FilesRead, FilesEdited, Notes, QualityGrade string
}

type AriaMemObservation struct {
	ID, SessionID, DeveloperUID, DeveloperRole, ClientID, Project, Scope string
	ObservationType, Title, Subtitle, Narrative, Facts, Concepts         string
	FilesTouched, ReasoningTrace, GeneratedByModel, TopicKey, Source     string
	SupersededBy                                                          string
	Sensitivity                                                           string
	RelevanceCount, DiscoveryTokens                                       int
	QualityScore                                                          float64
	DriftDetected, Canon                                                  bool
	ValidFrom, CreatedAt, UpdatedAt                                       time.Time
	ValidUntil                                                            *time.Time
}

type AriaMemSession struct {
	ID, DeveloperUID, DeveloperEmail, DeveloperRole, ClientID, MachineID string
	Project, Directory, Goal, Status                                      string
	StartedAt, CreatedAt                                                  time.Time
	EndedAt                                                               *time.Time
}

type AriaMemContextStatus struct {
	Project           string
	ActiveSessionID   string
	TotalObservations int
	Last7DaysCount    int
	SkillsLoaded      []string
}

type AriaMemSkill struct {
	ID, Name, Description, Content, Source string
	Stack                                  []string
	Active                                 bool
}

type AriaMemRecipe struct {
	ID, TaskPattern, StepsJSON, SourceSessionID string
	Stack                                       []string
	UsageCount                                  int
}

// AriaMemSkillsRankedInput son los inputs del aria_get_skills mejorado.
type AriaMemSkillsRankedInput struct {
	TaskDescription string
	Stack           []string
	Limit           int
	TokenBudget     int
	SessionID       string
	DeveloperUID    string
	Project         string
	Strategy        string
}

// AriaMemSkillsRankedOutput agrega telemetría al output (truncated, tokens).
type AriaMemSkillsRankedOutput struct {
	Skills         []*AriaMemSkill
	TruncatedCount int
	TokensUsed     int
	Strategy       string
}

// AriaMemAutoContextInput se usa al iniciar sesión para inyectar el primer
// mensaje compuesto.
type AriaMemAutoContextInput struct {
	Project      string
	Goal         string
	Stack        []string
	DeveloperUID string
	SessionID    string
	TokenBudget  int
}

// AriaMemAutoContextOutput es el markdown ya rendereado + métricas.
type AriaMemAutoContextOutput struct {
	Markdown       string
	TokensUsed     int
	TruncatedItems int
}

// AriaMemSkillFeedbackInput captura la intención del developer sobre un skill.
type AriaMemSkillFeedbackInput struct {
	SkillID      string
	DeveloperUID string
	Signal       string
	Helped       *bool
	Notes        string
}

func New(store ChunkStore, authSvc Authenticator, port int, opts ...Option) *CloudServer {
	s := &CloudServer{
		store: store,
		auth:  authSvc,
		port:  port,
		host:  defaultHost,
		syncStatus: staticStatusProvider{status: dashboard.SyncStatus{
			Phase:         "degraded",
			ReasonCode:    constants.ReasonTransportFailed,
			ReasonMessage: "sync status provider is unavailable",
		}},
		listenAndServe: http.ListenAndServe,
	}
	if projectAuthorizer, ok := authSvc.(ProjectAuthorizer); ok {
		s.projectAuth = projectAuthorizer
	}
	for _, opt := range opts {
		opt(s)
	}
	s.routes()
	return s
}

func (s *CloudServer) Start() error {
	host := strings.TrimSpace(s.host)
	if host == "" {
		host = defaultHost
	}
	addr := fmt.Sprintf("%s:%d", host, s.port)
	log.Printf("[aria-core-cloud] listening on %s", addr)
	return s.listenAndServe(addr, s.Handler())
}

func (s *CloudServer) Handler() http.Handler {
	if s.mux == nil {
		s.routes()
	}
	// Wrap every request body with a size cap (32 MiB default,
	// 128 MiB on attachment/upload routes). Returns 413 to clients
	// that exceed the cap. See dashboard/middleware.go.
	return dashboard.WrapWithBodyLimit(s.mux)
}

func (s *CloudServer) routes() {
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /health", s.handleHealth)
	var dashboardStore dashboard.DashboardStore
	if store, ok := s.store.(dashboard.DashboardStore); ok {
		dashboardStore = store
	}
	validateCredentials := func(email, password string) (*dashboard.LoginPrincipal, error) {
		if s.userStore == nil {
			return nil, fmt.Errorf("email/password login is not configured")
		}
		u, err := s.userStore.VerifyPassword(context.Background(), email, password)
		if err != nil {
			return nil, err
		}
		return &dashboard.LoginPrincipal{UID: u.UID, Email: u.Email, Name: u.Name, Roles: u.Roles}, nil
	}
	validateLoginToken := func(token string) error {
		token = strings.TrimSpace(token)
		if token == "" {
			return fmt.Errorf("admin recovery token is required")
		}
		adminToken := strings.TrimSpace(s.dashboardAdmin)
		if adminToken == "" {
			return fmt.Errorf("admin recovery is disabled")
		}
		if token != adminToken {
			return fmt.Errorf("invalid admin recovery token")
		}
		return nil
	}
	createSessionCookie := func(w http.ResponseWriter, r *http.Request, principal *dashboard.LoginPrincipal) error {
		if s.sessionCodec == nil {
			return fmt.Errorf("session codec not configured")
		}
		jwt, err := s.sessionCodec.Mint(principal.UID, principal.Email, principal.Roles)
		if err != nil {
			return err
		}
		http.SetCookie(w, &http.Cookie{
			Name:     dashboardSessionCookieName,
			Value:    jwt,
			Path:     "/dashboard",
			HttpOnly: true,
			Secure:   dashboardCookieSecure(r),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int((8 * time.Hour).Seconds()),
		})
		return nil
	}

	var adminUsers dashboard.AdminUserService
	if s.userStore != nil {
		adminUsers = userServiceAdapter{us: s.userStore}
	}

	dashboard.Mount(s.mux, dashboard.MountConfig{
		RequireSession:      s.authorizeDashboardRequest,
		ValidateCredentials: validateCredentials,
		ValidateLoginToken:  validateLoginToken,
		CreateSessionCookie: createSessionCookie,
		ClearSessionCookie: func(w http.ResponseWriter, r *http.Request) {
			http.SetCookie(w, &http.Cookie{
				Name:     dashboardSessionCookieName,
				Value:    "",
				Path:     "/dashboard",
				HttpOnly: true,
				Secure:   dashboardCookieSecure(r),
				SameSite: http.SameSiteLaxMode,
				MaxAge:   -1,
			})
		},
		IsAdmin: func(r *http.Request) bool {
			return s.isDashboardAdmin(r)
		},
		GetRoles: func(r *http.Request) []string {
			return s.dashboardRolesFromRequest(r)
		},
		GetDisplayName: func(r *http.Request) string {
			return s.displayNameFor(r)
		},
		GetUID: func(r *http.Request) string {
			claims, err := s.dashboardClaimsFromRequest(r)
			if err != nil || claims == nil {
				return ""
			}
			return claims.UID
		},
		Store:             dashboardStore,
		MaxLoginBodyBytes: maxDashboardLoginBodyBytes,
		StatusProvider:    s.syncStatus,
		AdminUsers:        adminUsers,
		Cotizador:         s.cotizador,
		AriaMem:           s.ariaMemDash,
		PDFClient:         s.pdfClient,
		Invites:             s.dashboardInvites,
		WelcomeMailer:       s.welcomeMailer,
		PasswordSelf:        s.passwordSelf,
		PasswordResetMailer: s.passwordResetMailer,
		PersonalCockpit:     s.personalCockpit,
		Profile:             s.profile,
		Redactor:          s.redactor,
		Vault:             s.vaultDash,
		ROI:               s.roiDash,
		Pages:             s.pagesDash,
		QuoteChat:         s.quoteChat,
		KnowledgeBase:     s.kbDash,
	})
	s.mux.HandleFunc("GET /sync/pull", s.withAuth(s.handlePullManifest))
	s.mux.HandleFunc("GET /sync/pull/{chunkID}", s.withAuth(s.handlePullChunk))
	s.mux.HandleFunc("POST /sync/push", s.withAuth(s.handlePushChunk))
	s.mux.HandleFunc("POST /sync/mutations/push", s.withAuth(s.handleMutationPush))
	s.mux.HandleFunc("GET /sync/mutations/pull", s.withAuth(s.handleMutationPull))

	// === /v1 API (JWT user-bound) ===
	// Auth: login con email+password, devuelve JWT.
	s.mux.HandleFunc("POST /v1/auth/login", s.handleV1AuthLogin)
	s.mux.HandleFunc("GET /v1/auth/me", s.withJWTAuth(s.handleV1AuthMe))

	// Cotizador (requiere role admin o cotizador en el JWT)
	s.mux.HandleFunc("GET /v1/cotizador/leads", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorLeadsList))
	s.mux.HandleFunc("POST /v1/cotizador/leads", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorLeadCreate))
	s.mux.HandleFunc("GET /v1/cotizador/leads/{id}", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorLeadGet))
	s.mux.HandleFunc("POST /v1/cotizador/leads/{id}/status", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorLeadStatus))
	s.mux.HandleFunc("GET /v1/cotizador/leads/{id}/history", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorLeadHistory))
	// RFPs
	s.mux.HandleFunc("POST /v1/cotizador/leads/{id}/rfps", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorRFPCreate))
	s.mux.HandleFunc("GET /v1/cotizador/leads/{id}/rfps", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorRFPList))
	s.mux.HandleFunc("GET /v1/cotizador/rfps/{rfpID}", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorRFPGet))
	s.mux.HandleFunc("POST /v1/cotizador/rfps/{rfpID}/analysis", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorRFPAnalysisUpdate))
	// Quotes
	s.mux.HandleFunc("POST /v1/cotizador/leads/{id}/quotes", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorQuoteCreate))
	s.mux.HandleFunc("GET /v1/cotizador/leads/{id}/quotes", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorQuoteList))
	s.mux.HandleFunc("GET /v1/cotizador/quotes/{quoteID}", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorQuoteGet))
	s.mux.HandleFunc("POST /v1/cotizador/quotes/{quoteID}/status", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorQuoteStatus))
	// Memoria histórica (commit 5)
	s.mux.HandleFunc("POST /v1/cotizador/quotes/{quoteID}/close", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorQuoteClose))
	s.mux.HandleFunc("GET /v1/cotizador/memory/similar-items", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorSimilarItems))
	s.mux.HandleFunc("GET /v1/cotizador/memory/outcome-stats", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorOutcomeStats))
	s.mux.HandleFunc("GET /v1/cotizador/memory/client-history", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorClientHistory))
	s.mux.HandleFunc("GET /v1/cotizador/memory/lessons", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorLessonsSearch))
	s.mux.HandleFunc("POST /v1/cotizador/memory/lessons", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorLessonCreate))
	// Clients (commit 6)
	s.mux.HandleFunc("POST /v1/cotizador/leads/{id}/promote", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorPromoteLead))
	s.mux.HandleFunc("GET /v1/cotizador/clients", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorClientsList))
	s.mux.HandleFunc("GET /v1/cotizador/clients/{clientID}", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorClientGet))
	// Templates (commit 9)
	s.mux.HandleFunc("GET /v1/cotizador/templates", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorTemplatesList))
	s.mux.HandleFunc("POST /v1/cotizador/quotes/{quoteID}/apply-template", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorApplyTemplate))

	// Quote-Chat (wave 6): assistant-driven cotización con scrub PII + email manual.
	s.mux.HandleFunc("POST /v1/cotizador/chat", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorChatCreate))
	s.mux.HandleFunc("GET /v1/cotizador/chat/{id}", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorChatGet))
	s.mux.HandleFunc("POST /v1/cotizador/chat/{id}/send", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorChatSend))
	s.mux.HandleFunc("POST /v1/cotizador/chat/{id}/finalize", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorChatFinalize))
	s.mux.HandleFunc("POST /v1/cotizador/chat/{id}/email-send", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1CotizadorChatEmailSend))

	// === Pages (mini-Notion): JWT user-bound. Cualquier rol autenticado lee/escribe.
	s.mux.HandleFunc("POST /v1/pages", s.withJWTAuth(s.handleV1PageCreate))
	s.mux.HandleFunc("GET /v1/pages/{id}", s.withJWTAuth(s.handleV1PageGet))
	s.mux.HandleFunc("GET /v1/pages/search", s.withJWTAuth(s.handleV1PagesSearch))
	s.mux.HandleFunc("GET /v1/pages/tree", s.withJWTAuth(s.handleV1PagesTree))

	// === Magic-link invites ===
	// Admin crea invite (auth admin, JWT bearer).
	s.mux.HandleFunc("POST /v1/admin/invites", s.withJWTRole([]string{"admin"}, s.handleV1AdminInviteCreate))
	// Form de activación + accept (sin auth — el token UUID es el credential).
	s.mux.HandleFunc("GET /dashboard/invite/{token}", s.handleDashboardInviteGet)
	s.mux.HandleFunc("POST /dashboard/invite/{token}/accept", s.handleDashboardInviteAccept)

	// === ARIA Memory (commit 10): reemplaza legacy mcp__aria__* ===
	// Cualquier role autenticado puede leer/guardar memoria.
	s.mux.HandleFunc("POST /v1/memory/save", s.withJWTAuth(s.handleV1MemorySave))
	s.mux.HandleFunc("GET /v1/memory/observations/{id}", s.withJWTAuth(s.handleV1MemoryGet))
	s.mux.HandleFunc("GET /v1/memory/search", s.withJWTAuth(s.handleV1MemorySearch))
	s.mux.HandleFunc("GET /v1/memory/timeline", s.withJWTAuth(s.handleV1MemoryTimeline))
	s.mux.HandleFunc("POST /v1/memory/observations/{id}/promote-canon", s.withJWTAuth(s.handleV1MemoryPromoteCanon))
	s.mux.HandleFunc("POST /v1/memory/observations/{id}/quality", s.withJWTAuth(s.handleV1MemoryRecordQuality))
	s.mux.HandleFunc("POST /v1/memory/sessions/start", s.withJWTAuth(s.handleV1MemorySessionStart))
	s.mux.HandleFunc("POST /v1/memory/sessions/{id}/summary", s.withJWTAuth(s.handleV1MemorySessionSummary))
	s.mux.HandleFunc("GET /v1/memory/context-status", s.withJWTAuth(s.handleV1MemoryContextStatus))
	s.mux.HandleFunc("GET /v1/memory/skills", s.withJWTAuth(s.handleV1MemorySkills))
	s.mux.HandleFunc("GET /v1/memory/recipes", s.withJWTAuth(s.handleV1MemoryRecipes))
	// Token budget + telemetry endpoints.
	s.mux.HandleFunc("POST /v1/memory/skills/feedback", s.withJWTAuth(s.handleV1MemorySkillFeedback))

	// === ARIA Vault: bóveda de secretos cifrados ===
	s.mux.HandleFunc("POST /v1/vault/secrets", s.withJWTAuth(s.handleV1VaultCreate))
	s.mux.HandleFunc("GET /v1/vault/secrets", s.withJWTAuth(s.handleV1VaultList))
	s.mux.HandleFunc("GET /v1/vault/secrets/{id}", s.withJWTAuth(s.handleV1VaultGet))
	s.mux.HandleFunc("POST /v1/vault/secrets/{id}/reveal", s.withJWTAuth(s.handleV1VaultReveal))
	s.mux.HandleFunc("POST /v1/vault/secrets/{id}/rotate", s.withJWTAuth(s.handleV1VaultRotate))
	s.mux.HandleFunc("POST /v1/vault/secrets/{id}/delete", s.withJWTAuth(s.handleV1VaultDelete))
	s.mux.HandleFunc("POST /v1/vault/secrets/{id}/grants", s.withJWTAuth(s.handleV1VaultGrant))
	s.mux.HandleFunc("GET /v1/vault/secrets/{id}/access-log", s.withJWTAuth(s.handleV1VaultAccessLog))

	// === ARIA Recipes: executable workflow runner ===
	// Cualquier user autenticado puede listar y ejecutar; admin ve histórico global.
	s.mux.HandleFunc("GET /v1/recipes/list", s.withJWTAuth(s.handleV1RecipeList))
	s.mux.HandleFunc("POST /v1/recipes/run", s.withJWTAuth(s.handleV1RecipeRun))
	s.mux.HandleFunc("GET /v1/recipes/executions", s.withJWTAuth(s.handleV1RecipeExecutionList))
	s.mux.HandleFunc("GET /v1/recipes/executions/{id}", s.withJWTAuth(s.handleV1RecipeExecutionGet))

	// === ARIA Pages: inline databases + comments + mentions ===
	// Inline databases (Notion-style): schema tipado, multi-view, filters/sort.
	s.mux.HandleFunc("POST /v1/pages/{pageID}/database", s.withJWTAuth(s.handleV1PageDBInit))
	s.mux.HandleFunc("GET /v1/pages/{pageID}/database", s.withJWTAuth(s.handleV1PageDBGet))
	s.mux.HandleFunc("PUT /v1/pages/{pageID}/database/schema", s.withJWTAuth(s.handleV1PageDBSchemaUpdate))
	s.mux.HandleFunc("POST /v1/pages/{pageID}/database/rows", s.withJWTAuth(s.handleV1PageDBRowCreate))
	s.mux.HandleFunc("GET /v1/pages/{pageID}/database/rows", s.withJWTAuth(s.handleV1PageDBRowsList))
	s.mux.HandleFunc("PATCH /v1/database-rows/{rowID}", s.withJWTAuth(s.handleV1PageDBRowUpdate))
	s.mux.HandleFunc("DELETE /v1/database-rows/{rowID}", s.withJWTAuth(s.handleV1PageDBRowDelete))
	s.mux.HandleFunc("POST /v1/pages/{pageID}/database/views", s.withJWTAuth(s.handleV1PageDBViewCreate))
	s.mux.HandleFunc("PATCH /v1/database-views/{viewID}", s.withJWTAuth(s.handleV1PageDBViewUpdate))
	s.mux.HandleFunc("DELETE /v1/database-views/{viewID}", s.withJWTAuth(s.handleV1PageDBViewDelete))

	// Comments threading + @mentions.
	s.mux.HandleFunc("POST /v1/pages/{pageID}/comments", s.withJWTAuth(s.handleV1CommentCreate))
	s.mux.HandleFunc("GET /v1/pages/{pageID}/comments", s.withJWTAuth(s.handleV1CommentsList))
	s.mux.HandleFunc("GET /v1/comments/{id}/replies", s.withJWTAuth(s.handleV1CommentRepliesList))
	s.mux.HandleFunc("PATCH /v1/comments/{id}", s.withJWTAuth(s.handleV1CommentUpdate))
	s.mux.HandleFunc("POST /v1/comments/{id}/resolve", s.withJWTAuth(s.handleV1CommentResolve))
	s.mux.HandleFunc("POST /v1/comments/{id}/unresolve", s.withJWTAuth(s.handleV1CommentUnresolve))
	s.mux.HandleFunc("DELETE /v1/comments/{id}", s.withJWTAuth(s.handleV1CommentDelete))
	s.mux.HandleFunc("GET /v1/mentions", s.withJWTAuth(s.handleV1MentionsList))
	s.mux.HandleFunc("POST /v1/pages/{pageID}/mentions/mark-read", s.withJWTAuth(s.handleV1MentionsMarkRead))

	// Dashboard mounts (only when adapter is configured).
	if s.recipes != nil {
		dashRecipes := newDashboardRecipeAdapter(s.recipes)
		mountRecipeDashboard(s.mux, dashRecipes, s.authorizeDashboardRequest, s.dashboardRolesFromRequest, s.displayNameFor)
	}

	// === Page attachments + shares ===
	if s.pageAttachments != nil {
		s.mux.HandleFunc("POST /v1/pages/{pageID}/attachments", s.withJWTAuth(s.handleV1AttachmentUpload))
		s.mux.HandleFunc("GET /v1/pages/{pageID}/attachments", s.withJWTAuth(s.handleV1AttachmentList))
		s.mux.HandleFunc("GET /v1/attachments/{id}", s.withJWTAuth(s.handleV1AttachmentGet))
		s.mux.HandleFunc("GET /v1/attachments/{id}/download", s.withJWTAuth(s.handleV1AttachmentDownload))
		s.mux.HandleFunc("GET /v1/attachments/{id}/thumbnail", s.withJWTAuth(s.handleV1AttachmentThumbnail))
		s.mux.HandleFunc("DELETE /v1/attachments/{id}", s.withJWTAuth(s.handleV1AttachmentDelete))

		if mountFn := newPageAttachmentsDashboard(s); mountFn != nil {
			mountFn(s.mux, s.authorizeDashboardRequest, s.displayNameFor, s.dashboardRolesFromRequest)
		}
	}
	if s.pageShares != nil {
		s.mux.HandleFunc("POST /v1/pages/{pageID}/share", s.withJWTAuth(s.handleV1ShareCreate))
		s.mux.HandleFunc("GET /v1/pages/{pageID}/shares", s.withJWTAuth(s.handleV1SharesList))
		s.mux.HandleFunc("DELETE /v1/shares/{id}", s.withJWTAuth(s.handleV1ShareRevoke))

		// PUBLIC routes — no auth, no /dashboard prefix. Rate-limited per IP.
		s.mux.HandleFunc("GET /p/{token}", s.handlePublicPageGet)
		s.mux.HandleFunc("POST /p/{token}/auth", s.handlePublicPageAuth)
		if s.pageAttachments != nil {
			s.mux.HandleFunc("GET /p/{token}/files/{id}", s.handlePublicAttachmentDownload)
		}
	}

	// === Page databases + comments (DB module) ===
	if s.pageDB != nil || s.pageComments != nil {
		s.mountPagesDashboard()
	}

	// === Team Projects (wave 7): plataforma de gestión de proyectos ===
	if s.teamProjects != nil {
		s.mountTeamProjectsDashboard()
		s.mountV1TeamProjects()
	}

	// === Knowledge-base sync (wave 8) ===
	if s.kb != nil {
		s.mux.HandleFunc("POST /v1/knowledge-base/sync", s.withJWTRole([]string{"admin"}, s.handleV1KBSyncAll))
		s.mux.HandleFunc("POST /v1/knowledge-base/sync/{projectID}", s.withJWTRole([]string{"admin"}, s.handleV1KBSyncProject))
		s.mux.HandleFunc("GET /v1/knowledge-base/status", s.withJWTAuth(s.handleV1KBStatus))
		s.mux.HandleFunc("GET /v1/cotizador/quotes/{quoteID}/export/docx", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1QuoteExportDOCX))
		s.mux.HandleFunc("GET /v1/cotizador/quotes/{quoteID}/export/markdown", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1QuoteExportMarkdown))
		s.mux.HandleFunc("POST /v1/cotizador/quotes/{quoteID}/sync-to-kb", s.withJWTRole([]string{"admin", "agent", "cotizador"}, s.handleV1QuoteSyncToKB))
	}
}

func (s *CloudServer) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.auth != nil {
			if err := s.auth.Authorize(r); err != nil {
				http.Error(w, fmt.Sprintf("unauthorized: %v", err), http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

func (s *CloudServer) withAuthHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.auth != nil {
			if err := s.auth.Authorize(r); err != nil {
				http.Error(w, fmt.Sprintf("unauthorized: %v", err), http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *CloudServer) authorizeDashboardRequest(r *http.Request) error {
	if _, err := s.dashboardClaimsFromRequest(r); err != nil {
		return err
	}
	return nil
}

func (s *CloudServer) dashboardClaimsFromRequest(r *http.Request) (*dashboardsession.Claims, error) {
	if s.sessionCodec == nil {
		return nil, fmt.Errorf("dashboard session codec not configured")
	}
	cookie, err := r.Cookie(dashboardSessionCookieName)
	if err != nil {
		return nil, err
	}
	return s.sessionCodec.Parse(cookie.Value)
}

func dashboardCookieSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	forwardedProto := r.Header.Get("X-Forwarded-Proto")
	for _, proto := range strings.Split(forwardedProto, ",") {
		if strings.EqualFold(strings.TrimSpace(proto), "https") {
			return true
		}
	}
	return false
}

func (s *CloudServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]any{"status": "ok", "service": "aria-core-cloud"})
}

func (s *CloudServer) isDashboardAdmin(r *http.Request) bool {
	claims, err := s.dashboardClaimsFromRequest(r)
	if err != nil || claims == nil {
		return false
	}
	return claims.HasRole("admin")
}

// dashboardRolesFromRequest retorna los roles del usuario autenticado, vacío si no hay sesión.
func (s *CloudServer) dashboardRolesFromRequest(r *http.Request) []string {
	claims, err := s.dashboardClaimsFromRequest(r)
	if err != nil || claims == nil {
		return nil
	}
	if len(claims.Roles) > 0 {
		return claims.Roles
	}
	if claims.Role != "" {
		return []string{claims.Role}
	}
	return nil
}

func (s *CloudServer) displayNameFor(r *http.Request) string {
	claims, err := s.dashboardClaimsFromRequest(r)
	if err != nil || claims == nil {
		return "OPERATOR"
	}
	if strings.TrimSpace(claims.Email) != "" {
		return claims.Email
	}
	return "OPERATOR"
}

// userServiceAdapter convierte DashboardUserService al contrato dashboard.AdminUserService.
type userServiceAdapter struct {
	us DashboardUserService
}

func (a userServiceAdapter) ListUsers(ctx context.Context) ([]dashboard.AdminUserView, error) {
	users, err := a.us.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.AdminUserView, 0, len(users))
	for _, u := range users {
		out = append(out, dashboard.AdminUserView{
			UID: u.UID, Email: u.Email, Name: u.Name, Roles: u.Roles, IsActive: u.IsActive, CreatedAt: u.CreatedAt,
		})
	}
	return out, nil
}

func (a userServiceAdapter) CreateUser(ctx context.Context, email, name string, roles []string, password string) error {
	if len(roles) == 0 {
		return fmt.Errorf("at least one role is required")
	}
	_, err := a.us.Create(ctx, email, name, roles, password)
	return err
}

func (a userServiceAdapter) AddRole(ctx context.Context, uid, role string) error {
	return a.us.AddRole(ctx, uid, role)
}

func (a userServiceAdapter) RemoveRole(ctx context.Context, uid, role string) error {
	return a.us.RemoveRole(ctx, uid, role)
}

func (a userServiceAdapter) SetActive(ctx context.Context, uid string, active bool) error {
	return a.us.SetActive(ctx, uid, active)
}

func (a userServiceAdapter) ChangePassword(ctx context.Context, uid, newPassword string) error {
	return a.us.ChangePassword(ctx, uid, newPassword)
}

func (s *CloudServer) handlePullManifest(w http.ResponseWriter, r *http.Request) {
	project, ok := projectFromRequest(w, r)
	if !ok {
		return
	}
	if !s.authorizeProjectScope(w, project) {
		return
	}
	manifest, err := s.store.ReadManifest(r.Context(), project)
	if err != nil {
		http.Error(w, fmt.Sprintf("read manifest: %v", err), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, manifest)
}

func (s *CloudServer) handlePullChunk(w http.ResponseWriter, r *http.Request) {
	project, ok := projectFromRequest(w, r)
	if !ok {
		return
	}
	if !s.authorizeProjectScope(w, project) {
		return
	}
	chunkID := strings.TrimSpace(r.PathValue("chunkID"))
	if chunkID == "" {
		http.Error(w, "chunkID is required", http.StatusBadRequest)
		return
	}
	chunk, err := s.store.ReadChunk(r.Context(), project, chunkID)
	if err != nil {
		if errors.Is(err, cloudstore.ErrChunkNotFound) {
			http.Error(w, fmt.Sprintf("read chunk: %v", err), http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("read chunk: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(chunk)
}

func (s *CloudServer) handlePushChunk(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPushBodyBytes)
	var req struct {
		ChunkID         string          `json:"chunk_id"`
		CreatedBy       string          `json:"created_by"`
		ClientCreatedAt string          `json:"client_created_at"`
		Project         string          `json:"project"`
		Data            json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeActionableError(w, http.StatusRequestEntityTooLarge, constants.UpgradeErrorClassRepairable, constants.UpgradeErrorCodePayloadTooLarge, fmt.Sprintf("push payload too large (max %d bytes)", maxPushBodyBytes))
			return
		}
		writeActionableError(w, http.StatusBadRequest, constants.UpgradeErrorClassRepairable, constants.UpgradeErrorCodePayloadInvalid, fmt.Sprintf("invalid push payload: %v", err))
		return
	}
	if len(req.Data) == 0 {
		writeActionableError(w, http.StatusBadRequest, constants.UpgradeErrorClassRepairable, constants.UpgradeErrorCodePayloadInvalid, "data is required")
		return
	}
	project := strings.TrimSpace(req.Project)
	if project == "" {
		project = strings.TrimSpace(r.URL.Query().Get("project"))
	}
	if project == "" {
		writeActionableError(w, http.StatusBadRequest, constants.UpgradeErrorClassBlocked, constants.UpgradeErrorCodeProjectRequired, "project is required")
		return
	}
	project, _ = store.NormalizeProject(project)
	project = strings.TrimSpace(project)
	if project == "" {
		writeActionableError(w, http.StatusBadRequest, constants.UpgradeErrorClassBlocked, constants.UpgradeErrorCodeProjectRequired, "project is required")
		return
	}
	if !s.authorizeProjectScope(w, project) {
		return
	}

	// Push-path pause guard: check project sync control before accepting the chunk.
	// Uses a structural interface assertion so the ChunkStore interface is NOT extended.
	// Satisfies REQ-109 / Design Decision 5.
	if storeForControls, ok := s.store.(interface {
		IsProjectSyncEnabled(project string) (bool, error)
	}); ok {
		enabled, err := storeForControls.IsProjectSyncEnabled(project)
		if err != nil {
			writeActionableError(w, http.StatusInternalServerError,
				constants.UpgradeErrorClassBlocked,
				constants.UpgradeErrorCodeInternal,
				fmt.Sprintf("check project control: %v", err))
			return
		}
		if !enabled {
			// REQ-405: emit audit entry for chunk-push pause-rejection before writing 409.
			// Structural type assertion — ChunkStore is NOT extended.
			contributor := strings.TrimSpace(req.CreatedBy)
			if contributor == "" {
				contributor = "unknown"
			}
			if auditor, ok := s.store.(interface {
				InsertAuditEntry(ctx context.Context, entry cloudstore.AuditEntry) error
			}); ok {
				if aerr := auditor.InsertAuditEntry(r.Context(), cloudstore.AuditEntry{
					Contributor: contributor,
					Project:     project,
					Action:      cloudstore.AuditActionChunkPush,
					Outcome:     cloudstore.AuditOutcomeRejectedProjectPaused,
					ReasonCode:  "sync-paused",
				}); aerr != nil {
					log.Printf("cloudserver: audit insert failed (chunk push): %v", aerr)
				}
			} else {
				log.Printf("cloudserver: store (%T) does not implement InsertAuditEntry; audit skipped", s.store)
			}
			// JW4: include project envelope fields in 409 response, consistent
			// with the mutation push 409 envelope (REQ-414 parity for chunk path).
			jsonResponse(w, http.StatusConflict, map[string]any{
				"error_class":    strings.TrimSpace(constants.UpgradeErrorClassPolicy),
				"error_code":     "sync-paused",
				"error":          fmt.Sprintf("sync is paused for project %q", project),
				"project":        project,
				"project_source": coreproject.SourceRequestBody,
				"project_path":   "",
			})
			return
		}
	}

	normalizedData, err := coerceChunkProject(req.Data, project)
	if err != nil {
		writeActionableError(w, http.StatusBadRequest, constants.UpgradeErrorClassRepairable, constants.UpgradeErrorCodePayloadInvalid, fmt.Sprintf("invalid push payload: %v", err))
		return
	}
	chunk, err := validateImportableChunkPayload(normalizedData)
	if err != nil {
		writeActionableError(w, http.StatusBadRequest, constants.UpgradeErrorClassRepairable, constants.UpgradeErrorCodePayloadInvalid, fmt.Sprintf("invalid push payload: %v", err))
		return
	}
	knownSessionIDs, err := s.store.KnownSessionIDs(r.Context(), project)
	if err != nil {
		writeActionableError(w, http.StatusInternalServerError, constants.UpgradeErrorClassBlocked, constants.UpgradeErrorCodeInternal, fmt.Sprintf("validate push payload: %v", err))
		return
	}
	if err := validateChunkSessionReferences(chunk, knownSessionIDs); err != nil {
		writeActionableError(w, http.StatusBadRequest, constants.UpgradeErrorClassRepairable, constants.UpgradeErrorCodePayloadInvalid, fmt.Sprintf("invalid push payload: %v", err))
		return
	}

	computedChunkID := chunkIDFromPayload(normalizedData)
	providedChunkID := strings.TrimSpace(req.ChunkID)
	if providedChunkID != "" && providedChunkID != computedChunkID {
		writeActionableError(w, http.StatusBadRequest, constants.UpgradeErrorClassRepairable, constants.UpgradeErrorCodePayloadInvalid, fmt.Sprintf("chunk_id does not match payload content hash (expected %s)", computedChunkID))
		return
	}
	clientCreatedAt := strings.TrimSpace(req.ClientCreatedAt)
	if clientCreatedAt != "" {
		if _, err := time.Parse(time.RFC3339, clientCreatedAt); err != nil {
			writeActionableError(w, http.StatusBadRequest, constants.UpgradeErrorClassRepairable, constants.UpgradeErrorCodePayloadInvalid, "client_created_at must be RFC3339")
			return
		}
	}

	if err := s.store.WriteChunk(r.Context(), project, computedChunkID, req.CreatedBy, clientCreatedAt, normalizedData); err != nil {
		if errors.Is(err, cloudstore.ErrChunkConflict) {
			writeActionableError(w, http.StatusConflict, constants.UpgradeErrorClassRepairable, constants.UpgradeErrorCodeChunkConflict, fmt.Sprintf("write chunk: %v", err))
			return
		}
		writeActionableError(w, http.StatusInternalServerError, constants.UpgradeErrorClassBlocked, constants.UpgradeErrorCodeInternal, fmt.Sprintf("write chunk: %v", err))
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"status": "ok", "chunk_id": computedChunkID})
}

func chunkIDFromPayload(payload []byte) string {
	return chunkcodec.ChunkID(payload)
}

func projectFromRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" {
		writeActionableError(w, http.StatusBadRequest, constants.UpgradeErrorClassBlocked, constants.UpgradeErrorCodeProjectRequired, "project is required")
		return "", false
	}
	project, _ = store.NormalizeProject(project)
	project = strings.TrimSpace(project)
	if project == "" {
		writeActionableError(w, http.StatusBadRequest, constants.UpgradeErrorClassBlocked, constants.UpgradeErrorCodeProjectRequired, "project is required")
		return "", false
	}
	return project, true
}

func (s *CloudServer) authorizeProjectScope(w http.ResponseWriter, project string) bool {
	if s.projectAuth == nil {
		return true
	}
	if err := s.projectAuth.AuthorizeProject(project); err != nil {
		writeActionableError(w, http.StatusForbidden, constants.UpgradeErrorClassPolicy, constants.ReasonPolicyForbidden, "forbidden: project is not allowed")
		return false
	}
	return true
}

func writeActionableError(w http.ResponseWriter, status int, class, code, message string) {
	jsonResponse(w, status, map[string]any{
		"error_class": strings.TrimSpace(class),
		"error_code":  strings.TrimSpace(code),
		"error":       strings.TrimSpace(message),
	})
}

func coerceChunkProject(payload []byte, project string) ([]byte, error) {
	return chunkcodec.CanonicalizeForProject(payload, project)
}

func decodeSyncMutationPayload(payload string, dest any) error {
	return chunkcodec.DecodeSyncMutationPayload(payload, dest)
}

func validateImportableChunkPayload(payload []byte) (coresync.ChunkData, error) {
	var chunk coresync.ChunkData
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return coresync.ChunkData{}, fmt.Errorf("chunk schema: %w", err)
	}
	if err := validateDirectChunkArrayEntries(chunk); err != nil {
		return coresync.ChunkData{}, err
	}
	return chunk, nil

}

func validateDirectChunkArrayEntries(chunk coresync.ChunkData) error {
	for i, session := range chunk.Sessions {
		if strings.TrimSpace(session.ID) == "" {
			return fmt.Errorf("sessions[%d].id is required", i)
		}
		if strings.TrimSpace(session.Directory) == "" {
			return fmt.Errorf("sessions[%d].directory is required", i)
		}
	}

	for i, observation := range chunk.Observations {
		if strings.TrimSpace(observation.SyncID) == "" {
			return fmt.Errorf("observations[%d].sync_id is required", i)
		}
		if strings.TrimSpace(observation.SessionID) == "" {
			return fmt.Errorf("observations[%d].session_id is required", i)
		}
		if strings.TrimSpace(observation.Type) == "" {
			return fmt.Errorf("observations[%d].type is required", i)
		}
		if strings.TrimSpace(observation.Title) == "" {
			return fmt.Errorf("observations[%d].title is required", i)
		}
		if strings.TrimSpace(observation.Content) == "" {
			return fmt.Errorf("observations[%d].content is required", i)
		}
		if strings.TrimSpace(observation.Scope) == "" {
			return fmt.Errorf("observations[%d].scope is required", i)
		}
	}

	for i, prompt := range chunk.Prompts {
		if strings.TrimSpace(prompt.SyncID) == "" {
			return fmt.Errorf("prompts[%d].sync_id is required", i)
		}
		if strings.TrimSpace(prompt.SessionID) == "" {
			return fmt.Errorf("prompts[%d].session_id is required", i)
		}
		if strings.TrimSpace(prompt.Content) == "" {
			return fmt.Errorf("prompts[%d].content is required", i)
		}
	}

	return nil
}

func validateChunkSessionReferences(chunk coresync.ChunkData, knownSessionIDs map[string]struct{}) error {
	chunkSessionIDs := make(map[string]struct{}, len(chunk.Sessions))
	for i, session := range chunk.Sessions {
		sessionID := strings.TrimSpace(session.ID)
		if sessionID == "" {
			return fmt.Errorf("sessions[%d].id is required", i)
		}
		chunkSessionIDs[sessionID] = struct{}{}
	}
	for i, mutation := range chunk.Mutations {
		if mutation.Entity != store.SyncEntitySession || mutation.Op != store.SyncOpUpsert {
			continue
		}
		var body struct {
			ID string `json:"id"`
		}
		if err := decodeSyncMutationPayload(mutation.Payload, &body); err != nil {
			return fmt.Errorf("mutations[%d] invalid payload: %w", i, err)
		}
		sessionID := strings.TrimSpace(body.ID)
		if sessionID == "" {
			sessionID = strings.TrimSpace(mutation.EntityKey)
		}
		if sessionID == "" {
			return fmt.Errorf("mutations[%d].payload.id is required for session upsert", i)
		}
		chunkSessionIDs[sessionID] = struct{}{}
	}

	hasSession := func(sessionID string) bool {
		if _, ok := chunkSessionIDs[sessionID]; ok {
			return true
		}
		_, ok := knownSessionIDs[sessionID]
		return ok
	}

	for i, observation := range chunk.Observations {
		sessionID := strings.TrimSpace(observation.SessionID)
		if sessionID == "" {
			return fmt.Errorf("observations[%d].session_id is required", i)
		}
		if !hasSession(sessionID) {
			return fmt.Errorf("observations[%d] references missing session_id %q", i, sessionID)
		}
	}

	for i, prompt := range chunk.Prompts {
		sessionID := strings.TrimSpace(prompt.SessionID)
		if sessionID == "" {
			return fmt.Errorf("prompts[%d].session_id is required", i)
		}
		if !hasSession(sessionID) {
			return fmt.Errorf("prompts[%d] references missing session_id %q", i, sessionID)
		}
	}

	for i, mutation := range chunk.Mutations {
		if mutation.Entity != store.SyncEntityObservation && mutation.Entity != store.SyncEntityPrompt {
			continue
		}
		var body struct {
			SessionID string `json:"session_id"`
		}
		if err := decodeSyncMutationPayload(mutation.Payload, &body); err != nil {
			return fmt.Errorf("mutations[%d] invalid payload: %w", i, err)
		}
		sessionID := strings.TrimSpace(body.SessionID)
		if mutation.Op == store.SyncOpUpsert && sessionID == "" {
			return fmt.Errorf("mutations[%d].payload.session_id is required for upsert", i)
		}
		if mutation.Op == store.SyncOpUpsert && !hasSession(sessionID) {
			return fmt.Errorf("mutations[%d] references missing session_id %q", i, sessionID)
		}
	}
	return nil
}

func jsonResponse(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}
