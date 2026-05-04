package dashboard

//go:generate go tool templ generate

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/constants"
	"github.com/a-h/templ"
)

type SyncStatus struct {
	Phase                string
	ReasonCode           string
	ReasonMessage        string
	UpgradeStage         string
	UpgradeReasonCode    string
	UpgradeReasonMessage string
}

type SyncStatusProvider interface {
	Status() SyncStatus
}

type staticSyncStatusProvider struct {
	status SyncStatus
}

func (s staticSyncStatusProvider) Status() SyncStatus { return s.status }

// LoginPrincipal es el resultado de un login exitoso (email+password o admin token).
type LoginPrincipal struct {
	UID   string
	Email string
	Name  string
	Roles []string
}

type MountConfig struct {
	RequireSession func(r *http.Request) error
	// ValidateCredentials recibe email y password. Si OK retorna el principal del usuario.
	// Si nil, login email+password queda deshabilitado.
	ValidateCredentials func(email, password string) (*LoginPrincipal, error)
	// ValidateLoginToken (opcional) — fallback para login con admin token legacy.
	// Si retorna nil error, el principal es admin recovery.
	ValidateLoginToken  func(token string) error
	CreateSessionCookie func(w http.ResponseWriter, r *http.Request, principal *LoginPrincipal) error
	ClearSessionCookie  func(w http.ResponseWriter, r *http.Request)
	IsAdmin             func(r *http.Request) bool
	GetRoles            func(r *http.Request) []string
	GetDisplayName      func(r *http.Request) string
	// GetUID (opcional) — retorna el UID del usuario autenticado.
	// Necesario para cockpit personal /dashboard/me y módulos como vault.
	GetUID func(r *http.Request) string
	Store               DashboardStore
	MaxLoginBodyBytes   int64
	StatusProvider      SyncStatusProvider
	// AdminUsers (opcional) — habilita CRUD de usuarios en /dashboard/admin/users.
	AdminUsers AdminUserService
	// Cotizador (opcional) — habilita módulo Cotizaciones en /dashboard/cotizador.
	Cotizador CotizadorService
	// AriaMem (opcional) — habilita /dashboard/memorias con aria_observations.
	AriaMem AriaMemDashboardService
	// PDFClient (opcional) — habilita endpoints de export PDF (gotenberg).
	PDFClient PDFClient
	// Invites (opcional) — habilita invitar usuario por email.
	Invites InviteDashboardService
	// WelcomeMailer (opcional) — envía email de bienvenida cuando admin
	// crea usuario manual (con password). Si nil, el usuario se crea sin notificación.
	WelcomeMailer UserWelcomeMailer
	// PasswordSelf (opcional) — habilita /dashboard/me/security (cambiar pwd) y
	// /dashboard/forgot-password + /dashboard/reset-password/{token}.
	PasswordSelf PasswordSelfService
	// Profile (opcional) — habilita /dashboard/me/profile y /me/notifications.
	Profile ProfileService
	// PasswordResetMailer (opcional) — envía email con magic link de reset.
	PasswordResetMailer PasswordResetMailerService
	// PersonalCockpit (opcional) — habilita /dashboard/me cockpit del dev.
	PersonalCockpit PersonalCockpitService
	// Redactor (opcional) — habilita /dashboard/audit/egress y stats.
	Redactor RedactorService
	// Vault (opcional) — habilita /dashboard/vault con CRUD de secrets + audit log.
	Vault VaultDashboardService
	// ROI (opcional) — habilita /dashboard/roi con métricas TTC/RDR/CWR/SVR/DTT
	// y savings consolidados.
	ROI ROIService
	// Pages (opcional) — habilita /dashboard/pages con mini-Notion: tree + markdown editor + Cmd+K.
	Pages PagesDashboardService
	// QuoteChat (opcional) — habilita /dashboard/cotizador/quote-chat (wave 6).
	QuoteChat QuoteChatService
	// KnowledgeBase (opcional) — habilita /dashboard/knowledge-base (wave 8).
	KnowledgeBase KnowledgeBaseDashboardService
	// SkillsRoot (sprint 3) — repository skills/ directory; enables the
	// /dashboard/skills/health audit view. Empty disables that surface.
	SkillsRoot string

	// HistoriasRoot (F5) — on-disk root for /Historias/<slug>/ chains.
	// Typically <repo>/Historias on a self-hosted dashboard. Empty
	// disables the /dashboard/historias surface.
	HistoriasRoot string
}

// KnowledgeBaseDashboardService es el contrato que el adapter del knowledge-base
// expone al dashboard. Espeja la API pública de knowledgebase.Service pero
// con view-models propios para evitar import cíclico.
type KnowledgeBaseDashboardService interface {
	Available() bool
	Status(ctx context.Context) (KBStatusView, error)
	List(ctx context.Context, filter KBListFilterView) ([]KBSyncedEntityView, error)
	ResyncFailed(ctx context.Context) (int, error)
	ResyncProject(ctx context.Context, projectID string) (int, error)
	SyncQuote(ctx context.Context, quoteID string) (string, string, error)
	SyncPRD(ctx context.Context, pageID string) (string, string, error)
	RefreshIndex(ctx context.Context) error
	GenerateQuoteDOCX(ctx context.Context, quoteID string) ([]byte, error)
}

// KBStatusView resume estado de sync para el dashboard.
type KBStatusView struct {
	Total   int
	OK      int
	Pending int
	Failed  int
	Skipped int
}

// KBListFilterView filtra el listado del dashboard.
type KBListFilterView struct {
	EntityType string
	ProjectID  string
	Status     string
	Limit      int
}

// KBSyncedEntityView es una fila del tracking visible en el dashboard.
type KBSyncedEntityView struct {
	ID            string
	EntityType    string
	EntityID      string
	ProjectID     string
	RepoPath      string
	LastCommitSHA string
	LastSyncedAt  time.Time
	SyncStatus    string
	LastError     string
	GitHubURL     string
}

// QuoteChatService is the dashboard contract for the chat-quote workflow.
// Implementation lives in /cmd/aria-core/quote_chat_adapter.go.
type QuoteChatService interface {
	CreateSession(ctx context.Context, in CreateChatSessionInput) (*ChatSessionView, error)
	GetSession(ctx context.Context, id string) (*ChatSessionView, []ChatMessageView, []ChatPreviewSectionView, error)
	ListMessages(ctx context.Context, sessionID string) ([]ChatMessageView, error)
	ListSections(ctx context.Context, sessionID string) ([]ChatPreviewSectionView, error)
	SendUserMessage(ctx context.Context, in SendChatMessageInput) (*ChatMessageView, *ChatMessageView, error)
	UpsertSection(ctx context.Context, sessionID, key, title, contentMD string) error
	GetSection(ctx context.Context, sessionID, key string) (string, string, error)
	FinalizeSession(ctx context.Context, sessionID, byUID string) (string, error)
	BuildEmailPreview(ctx context.Context, sessionID string) (*EmailPreviewView, error)
	SendEmail(ctx context.Context, sessionID, quoteID, to, cc, subject, bodyHTML string) error
	ListTemplates() []CotizadorTemplateView
}

// CreateChatSessionInput is the input for QuoteChatService.CreateSession.
type CreateChatSessionInput struct {
	LeadID       string
	RFPID        string
	TemplateKey  string
	Title        string
	InitiatedBy  string
}

// SendChatMessageInput is the input for QuoteChatService.SendUserMessage.
type SendChatMessageInput struct {
	SessionID    string
	UserUID      string
	Message      string
	Sensitivity  string
	Attachments  []ChatAttachmentView
	TimeoutSec   int
}

// PagesDashboardService es el contrato del módulo de páginas (mini-Notion) para
// el dashboard. Implementación de referencia: internal/cloud/pages.PgStore (vía adapter).
type PagesDashboardService interface {
	Tree(ctx context.Context, project, scope string) ([]PageView, error)
	Get(ctx context.Context, id string) (*PageView, error)
	Create(ctx context.Context, in CreatePageInput) (*PageView, error)
	Update(ctx context.Context, id string, in UpdatePageInput) (*PageView, error)
	Move(ctx context.Context, id, newParentID string, newSortOrder int) error
	Archive(ctx context.Context, id string) error
	Restore(ctx context.Context, id string) error
	ListRevisions(ctx context.Context, pageID string, limit int) ([]PageRevisionView, error)
	RevertToRevision(ctx context.Context, pageID, revisionID, byUID string) error
	ListTemplates() []PageTemplateView
	QuickSearchAll(ctx context.Context, query string, limit int) (*QuickSearchView, error)
}

// PageView espeja pages.Page sin importar el paquete pages dentro de dashboard.
type PageView struct {
	ID            string
	ParentID      string
	Title         string
	ContentMD     string
	Icon          string
	Project       string
	Scope         string
	ClientID      string
	PageType      string
	TemplateKey   string
	Sensitivity   string
	SortOrder     int
	IsArchived    bool
	CreatedByUID  string
	CreatedAt     time.Time
	UpdatedByUID  string
	UpdatedAt     time.Time
	ChildrenCount int
	Path          []PageBreadcrumbView
}

type PageBreadcrumbView struct {
	ID    string
	Title string
	Icon  string
}

type PageRevisionView struct {
	ID          string
	PageID      string
	Title       string
	ContentMD   string
	EditedByUID string
	EditSummary string
	CreatedAt   time.Time
}

type PageTemplateView struct {
	Key         string
	Name        string
	Description string
	Icon        string
}

type CreatePageInput struct {
	ParentID     string
	Title        string
	ContentMD    string
	Icon         string
	Project      string
	Scope        string
	Sensitivity  string
	TemplateKey  string
	CreatedByUID string
}

type UpdatePageInput struct {
	Title        *string
	ContentMD    *string
	Icon         *string
	Project      *string
	Scope        *string
	Sensitivity  *string
	UpdatedByUID string
	EditSummary  string
}

// QuickSearchView espeja pages.QuickSearchResult.
type QuickSearchView struct {
	Pages        []QuickHitView
	Observations []QuickHitView
	Skills       []QuickHitView
	Recipes      []QuickHitView
	Leads        []QuickHitView
	Quotes       []QuickHitView
}

type QuickHitView struct {
	ID       string
	Type     string
	Title    string
	Subtitle string
	URL      string
	Score    float64
}

// ROIService es el contrato del módulo ROI consumido por el dashboard.
// Implementación de referencia: internal/cloud/roi.MetricsStore (vía adapter).
type ROIService interface {
	CalculateSavings(ctx context.Context, devUID string, since, until time.Time) (*ROISavingsView, error)
	TopContributors(ctx context.Context, since time.Time, limit int) ([]ROIContributorScore, error)
	PerClientBreakdown(ctx context.Context, since time.Time) ([]ROIClientBreakdown, error)
	WeeklyTimeline(ctx context.Context, devUID string, weeks int, costPerMin float64) ([]ROIWeeklyPoint, error)
	// CostMXNPerMin retorna el costo configurado en MXN/min.
	CostMXNPerMin() float64
}

// ROISavingsView espeja roi.SavingsView para evitar el import del paquete roi
// dentro de dashboard (regla: dashboard no depende de packages de feature).
type ROISavingsView struct {
	WindowDays        int
	TotalSavedMinutes float64
	TotalSavedMXN     float64
	WorkdayPctSaved   float64
	ByPillar          map[string]float64
	ByPillarMXN       map[string]float64
	Compared          ROISavingsCompared

	RDR float64
	CWR float64
	SVR float64
	TTC float64
	DTT float64

	CostMXNPerMin float64
}

// ROISavingsCompared espeja roi.SavingsCompared.
type ROISavingsCompared struct {
	PreviousMinutes float64
	PreviousMXN     float64
	DeltaMinutes    float64
	DeltaMXN        float64
	DeltaPct        float64
}

// ROIContributorScore espeja roi.ContributorScore.
type ROIContributorScore struct {
	DeveloperUID   string
	DeveloperEmail string
	CanonCount     int
	RelevanceTotal int
}

// ROIClientBreakdown espeja roi.ClientROI.
type ROIClientBreakdown struct {
	ClientID         string
	ObservationCount int
	CanonCount       int
	VaultAccess      int
	VaultUseInCmd    int
}

// ROIWeeklyPoint espeja roi.WeeklyPoint.
type ROIWeeklyPoint struct {
	WeekStart    time.Time
	SavedMinutes float64
	SavedMXN     float64
}

// RedactorService es el contrato dashboard del módulo redactor (PII scrubber +
// LLM egress audit). Ver internal/cloud/redactor/.
type RedactorService interface {
	ListEgress(ctx context.Context, filter EgressFilter, limit, offset int) ([]EgressRow, int, error)
	StatsLastDays(ctx context.Context, days int) (*EgressStatsView, error)
	RevealAlias(ctx context.Context, token string) (entityType, displayValue string, err error)
}

// EgressFilter mirrors redactor.EgressFilter without importing the package
// (the dashboard package has zero deps on internal/cloud/redactor).
type EgressFilter struct {
	From     time.Time
	To       time.Time
	ClientID string
	UserUID  string
	Provider string
}

// EgressRow mirrors redactor.EgressRow.
type EgressRow struct {
	ID            string
	RequestID     string
	ObservationID string
	LLMProvider   string
	LLMModel      string
	ClientID      string
	UserUID       string
	Scrubbed      bool
	RedactionsRaw string
	PayloadHash   string
	PayloadSize   int
	Reason        string
	OccurredAt    time.Time
}

// EgressStatsView mirrors redactor.EgressStats.
type EgressStatsView struct {
	WindowDays    int
	TotalRequests int
	TotalScrubbed int
	TotalBypassed int
	BytesSent     int64
	ByProvider    map[string]int
	ByReason      map[string]int
}

// PDFClient es el contrato dashboard para conversiones HTML→PDF (gotenberg).
type PDFClient interface {
	ConvertHTML(ctx context.Context, htmlBytes []byte, opts PDFConvertOptions) ([]byte, error)
}

type PDFConvertOptions struct {
	PaperWidth        float64
	PaperHeight       float64
	MarginTop         float64
	MarginBottom      float64
	MarginLeft        float64
	MarginRight       float64
	PreferCSSPageSize bool
	PrintBackground   bool
}

// PasswordResetMailerService envía el email magic-link de reset password.
type PasswordResetMailerService interface {
	SendPasswordReset(ctx context.Context, email, link string) error
}

// UserWelcomeMailer es el contrato para enviar email de bienvenida al crear
// usuarios manualmente (con password en clear). Renderiza template welcome_user.html
// vía Microsoft Graph. Implementado por un adapter en cmd/aria-core/.
type UserWelcomeMailer interface {
	SendWelcome(ctx context.Context, params WelcomeParams) error
}

// WelcomeParams espeja email.WelcomeContext. Mantiene dashboard sin import del paquete email.
type WelcomeParams struct {
	Name      string
	Email     string
	Password  string
	Roles     []string
	CreatedBy string
}

// InviteDashboardService es el contrato del módulo de invites para el dashboard.
type InviteDashboardService interface {
	CreateAndSend(ctx context.Context, email string, roles []string, invitedByUID, invitedByEmail string) (link string, emailSent bool, info string, err error)
}

// VaultSecretView es la representación de un secret en el dashboard (sin valor descifrado).
type VaultSecretView struct {
	ID             string
	Name           string
	Category       string
	Scope          string
	Project        string
	ClientID       string
	Description    string
	RotationPolicy string
	ExpiresAt      *time.Time
	CreatedAt      time.Time
	CreatedByUID   string
	IsActive       bool
}

// VaultAccessEntryView es una fila del audit log para mostrar en dashboard.
type VaultAccessEntryView struct {
	ID            string
	SecretID      string
	SecretName    string
	AccessedByUID string
	Action        string
	Reason        string
	CommandHash   string
	AccessedAt    time.Time
}

// VaultDashboardService es el contrato del módulo vault para el dashboard.
// Es admin-gated en la layer de routes (requireAdmin).
type VaultDashboardService interface {
	List(ctx context.Context, ownerUID string, onlyOwned bool) ([]VaultSecretView, error)
	Create(ctx context.Context, name, category, scope, project, clientID, description, value string, byUID string) (string, error)
	Reveal(ctx context.Context, id, byUID, reason string) (string, error)
	Rotate(ctx context.Context, id, newValue, byUID string) error
	Delete(ctx context.Context, id, byUID string) error
	AccessLog(ctx context.Context, secretID string, limit int) ([]VaultAccessEntryView, error)
	GlobalAuditLog(ctx context.Context, limit, offset int) ([]VaultAccessEntryView, error)
	Available() bool
}

// AriaMemDashboardService es el contrato dashboard para la capa de memoria ARIA.
type AriaMemDashboardService interface {
	Search(ctx context.Context, query, project, scope, obsType string, limit int) ([]AriaMemoryView, error)
	GetByID(ctx context.Context, id string) (*AriaMemoryView, error)
	PromoteCanon(ctx context.Context, id, byUID string) error
	ListProjects(ctx context.Context) ([]string, error)
	// Skills admin (commit 12)
	ListAllSkills(ctx context.Context) ([]AriaSkillView, error)
	GetSkillByID(ctx context.Context, id string) (*AriaSkillView, error)
	UpsertSkill(ctx context.Context, p UpsertAriaSkillInput) error
	SetSkillActive(ctx context.Context, id string, active bool) error
	DeleteSkill(ctx context.Context, id string) error
	SearchSkills(ctx context.Context, query, stack string, activeOnly bool) ([]AriaSkillView, error)
	ListUniqueStacks(ctx context.Context) ([]string, error)
}

type AriaSkillView struct {
	ID          string
	Name        string
	Description string
	Stack       []string
	Content     string
	Source      string
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type UpsertAriaSkillInput struct {
	ID          string
	Name        string
	Description string
	Stack       []string
	Content     string
	Source      string
	Active      bool
}

type AriaMemoryView struct {
	ID              string
	SessionID       string
	Project         string
	Scope           string
	ObservationType string
	Title           string
	Subtitle        string
	Narrative       string
	Facts           string
	Concepts        string
	FilesTouched    string
	ReasoningTrace  string // JSON
	TopicKey        string
	Source          string
	Canon           bool
	Sensitivity     string // public|internal|client|confidential
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// CotizadorService es el contrato del módulo Cotizador para el dashboard.
type CotizadorService interface {
	// Leads
	ListLeads(ctx context.Context, status string) ([]CotizadorLeadView, error)
	GetLead(ctx context.Context, id string) (*CotizadorLeadView, error)
	CreateLead(ctx context.Context, p CreateLeadInput) (*CotizadorLeadView, error)
	UpdateLead(ctx context.Context, id, name, company, email, phone, source, notes string) error
	UpdateLeadStatus(ctx context.Context, id, newStatus, byUID, notes string) error
	LeadHistory(ctx context.Context, leadID string, limit int) ([]CotizadorLeadHistoryView, error)
	CountLeadsByStatus(ctx context.Context) (map[string]int, error)
	// RFPs
	ListRFPsByLead(ctx context.Context, leadID string) ([]CotizadorRFPView, error)
	GetRFP(ctx context.Context, id string) (*CotizadorRFPView, error)
	CreateRFP(ctx context.Context, p CreateRFPInput) (*CotizadorRFPView, error)
	UpdateRFPAnalysis(ctx context.Context, id, analysisJSON string) error
	// Quotes
	ListQuotesByLead(ctx context.Context, leadID string) ([]CotizadorQuoteView, error)
	GetQuote(ctx context.Context, id string) (*CotizadorQuoteView, error)
	ListQuoteItems(ctx context.Context, quoteID string) ([]CotizadorQuoteItemView, error)
	CreateQuote(ctx context.Context, p CreateQuoteInput) (*CotizadorQuoteView, error)
	UpdateQuoteStatus(ctx context.Context, quoteID, newStatus, byUID, notes string) error
	QuoteHistory(ctx context.Context, quoteID string, limit int) ([]CotizadorQuoteHistoryView, error)
	// Memoria histórica (commit 5)
	CloseQuoteWithOutcome(ctx context.Context, quoteID, newStatus, byUID, reason, lessonText string, lessonTags []string) error
	SearchSimilarItems(ctx context.Context, query string, limit int) ([]CotizadorSimilarItemView, error)
	GetOutcomeStats(ctx context.Context) (CotizadorOutcomeStatsView, error)
	GetDashboardStats(ctx context.Context) (*CotizadorDashboardStatsView, error)
	GetClientHistory(ctx context.Context, query string) ([]CotizadorClientHistoryView, error)
	SearchLessons(ctx context.Context, query, tag string, limit int) ([]CotizadorLessonView, error)
	CreateLesson(ctx context.Context, p CreateLessonInput) (*CotizadorLessonView, error)
	// Clients (commit 6)
	PromoteLeadToClient(ctx context.Context, p PromoteLeadInput) (*CotizadorClientView, error)
	ListClients(ctx context.Context) ([]CotizadorClientView, error)
	GetClient(ctx context.Context, id string) (*CotizadorClientView, error)
	// Proposal sections (commit 7)
	ListSections(ctx context.Context, quoteID string) ([]CotizadorQuoteSectionView, error)
	UpsertSection(ctx context.Context, quoteID, key, title, contentMD string, sortOrder int) error
	DeleteSection(ctx context.Context, quoteID, key string) error
	UpdateProposalHeader(ctx context.Context, quoteID string, p UpdateProposalHeaderInput) error
	// Templates (commit 9)
	ListTemplates() []CotizadorTemplateView
	ApplyTemplate(ctx context.Context, quoteID, templateKey string) error
}

type CotizadorTemplateView struct {
	Key             string
	Name            string
	Description     string
	ProposalType    string
	DefaultProduct  string
	DefaultSubtitle string
	DefaultTags     []string
	SectionCount    int
}

type CotizadorClientView struct {
	ID            string
	LeadID        string
	LegalName     string
	RFC           string
	FiscalAddress string
	BillingEmail  string
	ContactsJSON  string
	Notes         string
	CreatedAt     time.Time
}

type PromoteLeadInput struct {
	LeadID         string
	LegalName      string
	RFC            string
	FiscalAddress  string
	BillingEmail   string
	ContactsJSON   string
	Notes          string
	CreatedByUID   string
}

type CotizadorSimilarItemView struct {
	QuoteID      string
	Version      int
	QuoteStatus  string
	LeadID       string
	LeadName     string
	LeadCompany  string
	SKU          string
	Description  string
	Qty          float64
	UnitPrice    float64
	Subtotal     float64
	Currency     string
	QuoteCreated time.Time
	Rank         float64
}

type CotizadorOutcomeStatsView struct {
	Total        int
	Won          int
	Lost         int
	Expired      int
	Open         int
	WinRate      float64
	AvgWonTotal  float64
	AvgLostTotal float64
}

type CotizadorDashboardStatsView struct {
	CotizadorOutcomeStatsView
	LeadsByStatus    map[string]int
	QuotesByStatus   map[string]int
	PipelineValue    map[string]float64
	MonthlyTrend     []CotizadorMonthlyPoint
	AvgDealSize      float64
	TotalPipelineMXN float64
	TopCurrencies    []string
}

type CotizadorMonthlyPoint struct {
	Month   string
	Created int
	Won     int
	Lost    int
	WonMXN  float64
}

type CotizadorClientHistoryView struct {
	LeadID      string
	LeadName    string
	Company     string
	QuoteCount  int
	WonCount    int
	LostCount   int
	TotalSold   float64
	LastQuoteAt *time.Time
}

type CotizadorLessonView struct {
	ID            string
	QuoteID       string
	LeadID        string
	Text          string
	Tags          []string
	CreatedByRole string
	CreatedAt     time.Time
}

type CreateLessonInput struct {
	QuoteID      string
	LeadID       string
	Text         string
	Tags         []string
	CreatedByUID string
	Role         string
}

type CotizadorRFPView struct {
	ID            string
	LeadID        string
	SourceType    string
	SourceContent string
	AnalysisJSON  string
	CreatedAt     time.Time
}

type CreateRFPInput struct {
	LeadID        string
	SourceType    string
	SourceContent string
	AnalysisJSON  string
	CreatedByUID  string
}

type CotizadorQuoteView struct {
	ID            string
	LeadID        string
	RFPID         string
	Version       int
	Status        string
	Currency      string
	Subtotal      float64
	Taxes         float64
	Total         float64
	ValidUntil    *time.Time
	Terms         string
	Justification string
	CreatedByRole string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ApprovedAt    *time.Time
	// Header propuesta (commit 7)
	Folio                   string
	ProposalType            string
	ProductName             string
	ProductSubtitle         string
	Tags                    []string
	PreparedForCompany      string
	PreparedForArea         string
	PreparedForContactName  string
	PreparedForContactEmail string
	IssueDate               *time.Time
	PreparedByName          string
	PreparedByEmail         string
	PreparedByRole          string
}

type CotizadorQuoteSectionView struct {
	ID        string
	Key       string
	Title     string
	ContentMD string
	SortOrder int
}

type UpdateProposalHeaderInput struct {
	Folio                   string
	ProposalType            string
	ProductName             string
	ProductSubtitle         string
	Tags                    []string
	PreparedForCompany      string
	PreparedForArea         string
	PreparedForContactName  string
	PreparedForContactEmail string
	IssueDate               *time.Time
	PreparedByName          string
	PreparedByEmail         string
	PreparedByRole          string
}

type CotizadorQuoteItemView struct {
	ID          string
	SKU         string
	Description string
	Qty         float64
	UnitPrice   float64
	Subtotal    float64
	SortOrder   int
}

type CotizadorQuoteHistoryView struct {
	Action     string
	FromStatus string
	ToStatus   string
	Notes      string
	OccurredAt time.Time
}

type CreateQuoteInput struct {
	LeadID        string
	RFPID         string
	Currency      string
	ValidUntil    *time.Time
	Terms         string
	Justification string
	CreatedByUID  string
	Role          string
	Items         []CreateQuoteItemInput
}

type CreateQuoteItemInput struct {
	SKU         string
	Description string
	Qty         float64
	UnitPrice   float64
}

type CotizadorLeadView struct {
	ID            string
	Name          string
	Company       string
	Email         string
	Phone         string
	Source        string
	Status        string
	Notes         string
	CreatedByRole string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type CotizadorLeadHistoryView struct {
	Action     string
	FromStatus string
	ToStatus   string
	Notes      string
	OccurredAt time.Time
}

type CreateLeadInput struct {
	Name         string
	Company      string
	Email        string
	Phone        string
	Source       string
	Notes        string
	CreatedByUID string
	Role         string
}

// AdminUserService expone el CRUD de usuarios al dashboard admin.
type AdminUserService interface {
	ListUsers(ctx context.Context) ([]AdminUserView, error)
	CreateUser(ctx context.Context, email, name string, roles []string, password string) error
	AddRole(ctx context.Context, uid, role string) error
	RemoveRole(ctx context.Context, uid, role string) error
	SetActive(ctx context.Context, uid string, active bool) error
	ChangePassword(ctx context.Context, uid, newPassword string) error
}

// ProfileService es el contrato self-service para que el dev gestione
// su propio perfil (nombre, datos personales, preferencias).
type ProfileService interface {
	GetProfile(ctx context.Context, uid string) (*UserProfileView, error)
	UpdateProfile(ctx context.Context, uid string, p UserProfileUpdate) error
	UpdatePreferences(ctx context.Context, uid string, prefsJSON []byte) error
}

// UserProfileView espeja cloudusers.User con los campos editables.
type UserProfileView struct {
	UID         string
	Email       string
	Name        string
	Phone       string
	Timezone    string
	Language    string
	JobTitle    string
	Bio         string
	AvatarURL   string
	Roles       []string
	Preferences map[string]any // shape: {notifications: {mentions: bool, quotes: bool, weekly_digest: bool}}
	CreatedAt   time.Time
	LastActive  *time.Time
}

// UserProfileUpdate es el body que el handler pasa al store en POST profile.
type UserProfileUpdate struct {
	Name      string
	Phone     string
	Timezone  string
	Language  string
	JobTitle  string
	Bio       string
	AvatarURL string
}

// PasswordSelfService es el contrato para self-service password change +
// forgot-password flow. Separado de AdminUserService para mantener admin clean
// (admin no necesita verify-with-current; agente no debe poder triggerar resets).
type PasswordSelfService interface {
	// VerifyAndChangePassword: dev cambia su propio password (verifica current).
	VerifyAndChangePassword(ctx context.Context, uid, currentPassword, newPassword string) error
	// CreatePasswordResetToken: anyone puede pedir; si email existe + active,
	// crea token 1h y retorna found=true. Si no, found=false (anti-enumeration).
	CreatePasswordResetToken(ctx context.Context, email string) (token string, expiresAt time.Time, found bool, err error)
	// ConsumePasswordResetToken: aplica el reset y marca token usado. Retorna uid del user.
	ConsumePasswordResetToken(ctx context.Context, token, newPassword string) (uid string, err error)
}

type AdminUserView struct {
	UID       string
	Email     string
	Name      string
	Roles     []string
	IsActive  bool
	CreatedAt time.Time
}

type DashboardStore interface {
	// Existing methods (from cloud-dashboard-parity).
	ListProjects(query string) ([]cloudstore.DashboardProjectRow, error)
	ProjectDetail(project string) (cloudstore.DashboardProjectDetail, error)
	ListContributors(query string) ([]cloudstore.DashboardContributorRow, error)
	ListRecentSessions(project string, query string, limit int) ([]cloudstore.DashboardSessionRow, error)
	ListRecentObservations(project string, query string, limit int) ([]cloudstore.DashboardObservationRow, error)
	ListRecentPrompts(project string, query string, limit int) ([]cloudstore.DashboardPromptRow, error)
	AdminOverview() (cloudstore.DashboardAdminOverview, error)

	// Paginated list methods (from cloud-dashboard-visual-parity).
	ListProjectsPaginated(query string, limit, offset int) ([]cloudstore.DashboardProjectRow, int, error)
	ListRecentObservationsPaginated(project, query, obsType string, limit, offset int) ([]cloudstore.DashboardObservationRow, int, error)
	ListRecentSessionsPaginated(project, query string, limit, offset int) ([]cloudstore.DashboardSessionRow, int, error)
	ListRecentPromptsPaginated(project, query string, limit, offset int) ([]cloudstore.DashboardPromptRow, int, error)
	ListContributorsPaginated(query string, limit, offset int) ([]cloudstore.DashboardContributorRow, int, error)

	// Detail methods.
	GetSessionDetail(project, sessionID string) (cloudstore.DashboardSessionRow, []cloudstore.DashboardObservationRow, []cloudstore.DashboardPromptRow, error)
	GetObservationDetail(project, sessionID, syncID string) (cloudstore.DashboardObservationRow, cloudstore.DashboardSessionRow, []cloudstore.DashboardObservationRow, error)
	GetPromptDetail(project, sessionID, syncID string) (cloudstore.DashboardPromptRow, cloudstore.DashboardSessionRow, []cloudstore.DashboardPromptRow, error)

	// SystemHealth.
	SystemHealth() (cloudstore.DashboardSystemHealth, error)

	// Sync control methods.
	ListProjectSyncControls() ([]cloudstore.ProjectSyncControl, error)
	GetProjectSyncControl(project string) (*cloudstore.ProjectSyncControl, error)
	SetProjectSyncEnabled(project string, enabled bool, updatedBy, reason string) error
	IsProjectSyncEnabled(project string) (bool, error)

	// Batch 6: Connected navigation methods.
	GetContributorDetail(name string) (cloudstore.DashboardContributorRow, []cloudstore.DashboardSessionRow, []cloudstore.DashboardObservationRow, []cloudstore.DashboardPromptRow, error)
	ListDistinctTypes() ([]string, error)

	// Audit log (REQ-409).
	ListAuditEntriesPaginated(ctx context.Context, filter cloudstore.AuditFilter, limit, offset int) ([]cloudstore.DashboardAuditRow, int, error)
}

type handlers struct {
	cfg MountConfig
}

// Mount registers all dashboard routes onto mux.
//
// Returns an error if the embedded static FS sub-tree is unavailable.
// Callers (typically cloudserver) decide whether to halt startup,
// degrade to API-only, or surface the error to operators. We MUST
// NOT log.Fatal in a leaf module — that takes down the whole binary
// (#5 from the 2026-05-04 improvement audit).
func Mount(mux *http.ServeMux, cfg MountConfig) error {
	h := &handlers{cfg: cfg}

	staticSub, err := fs.Sub(StaticFS, "static")
	if err != nil {
		return fmt.Errorf("dashboard: create static sub FS: %w", err)
	}
	mux.Handle("GET /dashboard/static/", http.StripPrefix("/dashboard/static/", http.FileServer(http.FS(staticSub))))

	mux.HandleFunc("GET /dashboard/health", h.handleHealth)
	mux.HandleFunc("GET /dashboard/login", h.handleLoginPage)
	mux.HandleFunc("POST /dashboard/login", h.handleLoginSubmit)
	mux.HandleFunc("POST /dashboard/logout", h.handleLogout)

	mux.HandleFunc("GET /dashboard", h.requireSession(h.handleDashboardHome))
	mux.HandleFunc("GET /dashboard/", h.requireSession(h.handleDashboardHome))
	mux.HandleFunc("GET /dashboard/stats", h.requireSession(h.handleDashboardStats))
	mux.HandleFunc("GET /dashboard/activity", h.requireSession(h.handleDashboardActivity))
	mux.HandleFunc("GET /dashboard/browser", h.requireSession(h.handleBrowser))
	mux.HandleFunc("GET /dashboard/browser/observations", h.requireSession(h.handleBrowserObservations))
	mux.HandleFunc("GET /dashboard/browser/sessions", h.requireSession(h.handleBrowserSessions))
	mux.HandleFunc("GET /dashboard/browser/sessions/{sessionID}", h.requireSession(h.handleBrowserSessionDetail))
	mux.HandleFunc("GET /dashboard/browser/prompts", h.requireSession(h.handleBrowserPrompts))
	mux.HandleFunc("GET /dashboard/projects", h.requireSession(h.handleProjects))
	mux.HandleFunc("GET /dashboard/projects/{project}", h.requireSession(h.handleProjectDetail))
	mux.HandleFunc("GET /dashboard/contributors", h.requireSession(h.handleContributors))
	mux.HandleFunc("GET /dashboard/contributors/list", h.requireSession(h.handleContributorsList))
	mux.HandleFunc("GET /dashboard/contributors/{contributor}", h.requireSession(h.handleContributorDetail))
	mux.HandleFunc("GET /dashboard/admin", h.requireSession(h.handleAdmin))
	mux.HandleFunc("GET /dashboard/skills/health", h.requireSession(h.handleSkillsHealth))
	mux.HandleFunc("GET /dashboard/historias", h.requireSession(h.handleHistoriasIndex))
	mux.HandleFunc("GET /dashboard/historias/{slug}", h.requireSession(h.handleHistoriaDetail))
	mux.HandleFunc("GET /dashboard/admin/projects", h.requireSession(h.handleAdminProjectControls))
	// R4-10: /dashboard/admin/contributors was a dead route (duplicate of /dashboard/contributors
	// behind an extra admin gate). Removed to avoid confusion.

	// 11 new routes — visual parity + composite-ID detail pages (REQ-106, Design Decision 3).
	mux.HandleFunc("GET /dashboard/projects/list", h.requireSession(h.handleProjectsList))
	mux.HandleFunc("GET /dashboard/projects/{name}/observations", h.requireSession(h.handleProjectObservationsPartial))
	mux.HandleFunc("GET /dashboard/projects/{name}/sessions", h.requireSession(h.handleProjectSessionsPartial))
	mux.HandleFunc("GET /dashboard/projects/{name}/prompts", h.requireSession(h.handleProjectPromptsPartial))
	mux.HandleFunc("GET /dashboard/admin/users", h.requireAdmin(h.handleAdminUsers))
	mux.HandleFunc("GET /dashboard/admin/users/list", h.requireAdmin(h.handleAdminUsersList))
	mux.HandleFunc("POST /dashboard/admin/users/create", h.requireAdmin(h.handleAdminUserCreate))
	mux.HandleFunc("POST /dashboard/admin/users/{uid}/roles/{role}/add", h.requireAdmin(h.handleAdminUserAddRole))
	mux.HandleFunc("POST /dashboard/admin/users/{uid}/roles/{role}/remove", h.requireAdmin(h.handleAdminUserRemoveRole))
	mux.HandleFunc("POST /dashboard/admin/users/{uid}/activate", h.requireAdmin(h.handleAdminUserSetActive(true)))
	mux.HandleFunc("POST /dashboard/admin/users/{uid}/deactivate", h.requireAdmin(h.handleAdminUserSetActive(false)))
	mux.HandleFunc("POST /dashboard/admin/users/{uid}/password", h.requireAdmin(h.handleAdminUserChangePassword))
	mux.HandleFunc("POST /dashboard/admin/users/invite", h.requireAdmin(h.handleAdminInviteCreate))
	mux.HandleFunc("GET /dashboard/admin/health", h.requireSession(h.handleAdminHealth))
	mux.HandleFunc("POST /dashboard/admin/projects/{name}/sync", h.requireSession(h.handleAdminSyncTogglePost))
	mux.HandleFunc("GET /dashboard/admin/projects/{name}/sync/form", h.requireSession(h.handleAdminSyncToggleForm))
	mux.HandleFunc("GET /dashboard/sessions/{project}/{sessionID}", h.requireSession(h.handleSessionDetail))
	mux.HandleFunc("GET /dashboard/observations/{project}/{sessionID}/{syncID}", h.requireSession(h.handleObservationDetail))
	mux.HandleFunc("GET /dashboard/prompts/{project}/{sessionID}/{syncID}", h.requireSession(h.handlePromptDetail))

	// Audit log routes — admin-gated (REQ-408, REQ-409).
	mux.HandleFunc("GET /dashboard/admin/audit-log", h.requireSession(h.handleAdminAuditLog))
	mux.HandleFunc("GET /dashboard/admin/audit-log/list", h.requireSession(h.handleAdminAuditLogList))

	// === Cotizador module — visible solo para roles admin + cotizador ===
	cotizadorRoles := []string{"admin", "cotizador"}
	mux.HandleFunc("GET /dashboard/cotizador", h.requireAnyRole(cotizadorRoles, h.handleCotizadorHome))
	mux.HandleFunc("GET /dashboard/cotizador/leads/list", h.requireAnyRole(cotizadorRoles, h.handleCotizadorLeadsList))
	mux.HandleFunc("POST /dashboard/cotizador/leads/create", h.requireAnyRole(cotizadorRoles, h.handleCotizadorLeadCreate))
	mux.HandleFunc("GET /dashboard/cotizador/leads/{id}", h.requireAnyRole(cotizadorRoles, h.handleCotizadorLeadDetail))
	mux.HandleFunc("POST /dashboard/cotizador/leads/{id}/status", h.requireAnyRole(cotizadorRoles, h.handleCotizadorLeadStatusChange))
	mux.HandleFunc("POST /dashboard/cotizador/leads/{id}/update", h.requireAnyRole(cotizadorRoles, h.handleCotizadorLeadUpdate))
	// RFPs
	mux.HandleFunc("POST /dashboard/cotizador/leads/{id}/rfps/create", h.requireAnyRole(cotizadorRoles, h.handleCotizadorRFPCreate))
	mux.HandleFunc("GET /dashboard/cotizador/rfps/{rfpID}", h.requireAnyRole(cotizadorRoles, h.handleCotizadorRFPDetail))
	mux.HandleFunc("POST /dashboard/cotizador/rfps/{rfpID}/analysis", h.requireAnyRole(cotizadorRoles, h.handleCotizadorRFPAnalysisUpdate))
	// Quotes
	mux.HandleFunc("GET /dashboard/cotizador/leads/{id}/quotes/new", h.requireAnyRole(cotizadorRoles, h.handleCotizadorQuoteNewForm))
	mux.HandleFunc("POST /dashboard/cotizador/leads/{id}/quotes/create", h.requireAnyRole(cotizadorRoles, h.handleCotizadorQuoteCreate))
	mux.HandleFunc("GET /dashboard/cotizador/quotes/{quoteID}", h.requireAnyRole(cotizadorRoles, h.handleCotizadorQuoteDetail))
	mux.HandleFunc("POST /dashboard/cotizador/quotes/{quoteID}/status", h.requireAnyRole(cotizadorRoles, h.handleCotizadorQuoteStatusChange))
	// Proposal vista completa + edit header + sections (commit 7)
	mux.HandleFunc("GET /dashboard/cotizador/quotes/{quoteID}/proposal", h.requireAnyRole(cotizadorRoles, h.handleCotizadorQuoteProposal))
	// Export PDF de propuestas vía gotenberg (commit 13)
	mux.HandleFunc("GET /dashboard/cotizador/quotes/{quoteID}/proposal.pdf", h.requireAnyRole(cotizadorRoles, h.handleCotizadorQuoteProposalPDF))
	mux.HandleFunc("GET /dashboard/cotizador/quotes/{quoteID}/edit-header", h.requireAnyRole(cotizadorRoles, h.handleCotizadorQuoteEditHeader))
	mux.HandleFunc("POST /dashboard/cotizador/quotes/{quoteID}/header", h.requireAnyRole(cotizadorRoles, h.handleCotizadorQuoteUpdateHeader))
	mux.HandleFunc("POST /dashboard/cotizador/quotes/{quoteID}/sections/upsert", h.requireAnyRole(cotizadorRoles, h.handleCotizadorQuoteSectionUpsert))
	mux.HandleFunc("POST /dashboard/cotizador/quotes/{quoteID}/sections/{key}/delete", h.requireAnyRole(cotizadorRoles, h.handleCotizadorQuoteSectionDelete))
	mux.HandleFunc("POST /dashboard/cotizador/quotes/{quoteID}/apply-template", h.requireAnyRole(cotizadorRoles, h.handleCotizadorQuoteApplyTemplate))
	mux.HandleFunc("GET /dashboard/cotizador/stats", h.requireAnyRole(cotizadorRoles, h.handleCotizadorStats))

	// === Quote-Chat (wave 6): split-pane assistant + live quote preview ===
	mux.HandleFunc("POST /dashboard/cotizador/quote-chat/create", h.requireAnyRole(cotizadorRoles, h.handleQuoteChatCreate))
	mux.HandleFunc("GET /dashboard/cotizador/quote-chat/{id}", h.requireAnyRole(cotizadorRoles, h.handleQuoteChatPage))
	mux.HandleFunc("POST /dashboard/cotizador/quote-chat/{id}/send", h.requireAnyRole(cotizadorRoles, h.handleQuoteChatSend))
	mux.HandleFunc("GET /dashboard/cotizador/quote-chat/{id}/preview", h.requireAnyRole(cotizadorRoles, h.handleQuoteChatPreview))
	mux.HandleFunc("GET /dashboard/cotizador/quote-chat/{id}/messages", h.requireAnyRole(cotizadorRoles, h.handleQuoteChatMessages))
	mux.HandleFunc("GET /dashboard/cotizador/quote-chat/{id}/sections/{key}/edit", h.requireAnyRole(cotizadorRoles, h.handleQuoteChatSectionEdit))
	mux.HandleFunc("POST /dashboard/cotizador/quote-chat/{id}/sections/{key}/save", h.requireAnyRole(cotizadorRoles, h.handleQuoteChatSectionSave))
	mux.HandleFunc("POST /dashboard/cotizador/quote-chat/{id}/finalize", h.requireAnyRole(cotizadorRoles, h.handleQuoteChatFinalize))
	mux.HandleFunc("GET /dashboard/cotizador/quote-chat/{id}/email-preview", h.requireAnyRole(cotizadorRoles, h.handleQuoteChatEmailPreview))
	mux.HandleFunc("POST /dashboard/cotizador/quote-chat/{id}/email-send", h.requireAnyRole(cotizadorRoles, h.handleQuoteChatEmailSend))

	// === Memoria ARIA (commit 11) ===
	mux.HandleFunc("GET /dashboard/memorias", h.requireSession(h.handleAriaMemList))
	mux.HandleFunc("GET /dashboard/memorias/list", h.requireSession(h.handleAriaMemListPartial))
	mux.HandleFunc("GET /dashboard/memorias/{id}", h.requireSession(h.handleAriaMemDetail))
	mux.HandleFunc("POST /dashboard/memorias/{id}/promote-canon", h.requireSession(h.handleAriaMemPromoteCanon))

	// Página de ayuda / guía de uso (visible para todos los autenticados).
	mux.HandleFunc("GET /dashboard/ayuda", h.requireSession(h.handleAyudaPage))

	// === Admin: Skills CRUD + vista MCP profiles (commit 12) ===
	mux.HandleFunc("GET /dashboard/admin/skills", h.requireAdmin(h.handleAdminSkillsList))
	mux.HandleFunc("GET /dashboard/admin/skills/list", h.requireAdmin(h.handleAdminSkillsListPartial))
	mux.HandleFunc("GET /dashboard/admin/skills/new", h.requireAdmin(h.handleAdminSkillNew))
	mux.HandleFunc("GET /dashboard/admin/skills/{id}", h.requireAdmin(h.handleAdminSkillEdit))
	mux.HandleFunc("POST /dashboard/admin/skills/upsert", h.requireAdmin(h.handleAdminSkillUpsert))
	mux.HandleFunc("POST /dashboard/admin/skills/{id}/toggle", h.requireAdmin(h.handleAdminSkillToggle))
	mux.HandleFunc("POST /dashboard/admin/skills/{id}/delete", h.requireAdmin(h.handleAdminSkillDelete))
	mux.HandleFunc("GET /dashboard/admin/mcp", h.requireAdmin(h.handleAdminMCPView))

	// Personal cockpit (/dashboard/me) — vista personal del dev autenticado.
	mux.HandleFunc("GET /dashboard/me", h.requireSession(h.handlePersonalCockpit))
	mux.HandleFunc("POST /dashboard/sessions/{id}/resume", h.requireSession(h.handleSessionResume))

	// Mi cuenta · Seguridad (cambiar password self-service)
	mux.HandleFunc("GET /dashboard/me/security", h.requireSession(h.handleAccountSecurityPage))
	mux.HandleFunc("POST /dashboard/me/security/password", h.requireSession(h.handleAccountPasswordChange))

	// Mi cuenta · Perfil + Notificaciones
	mux.HandleFunc("GET /dashboard/me/profile", h.requireSession(h.handleProfilePage))
	mux.HandleFunc("POST /dashboard/me/profile", h.requireSession(h.handleProfileUpdate))
	mux.HandleFunc("GET /dashboard/me/notifications", h.requireSession(h.handleNotificationsPage))
	mux.HandleFunc("POST /dashboard/me/notifications", h.requireSession(h.handleNotificationsUpdate))

	// Forgot/reset password (rutas públicas, sin auth)
	mux.HandleFunc("GET /dashboard/forgot-password", h.handleForgotPasswordPage)
	mux.HandleFunc("POST /dashboard/forgot-password", h.handleForgotPasswordSubmit)
	mux.HandleFunc("GET /dashboard/reset-password/{token}", h.handleResetPasswordPage)
	mux.HandleFunc("POST /dashboard/reset-password/{token}", h.handleResetPasswordSubmit)
	mux.HandleFunc("POST /dashboard/sessions/{id}/close", h.requireSession(h.handleSessionClose))

	// === Audit egress (redactor module) — admin-gated ===
	mux.HandleFunc("GET /dashboard/audit/egress", h.requireAdmin(h.handleAuditEgress))
	mux.HandleFunc("GET /dashboard/audit/egress/list", h.requireAdmin(h.handleAuditEgressList))
	mux.HandleFunc("GET /dashboard/audit/egress.csv", h.requireAdmin(h.handleAuditEgressCSV))

	// === ROI: métricas de Return-On-Investment (cualquier dev autenticado puede ver el suyo).
	// Admin ve agregados globales; dev ve solo su propio ROI vía filtro JS-side.
	mux.HandleFunc("GET /dashboard/roi", h.requireSession(h.handleROIPage))
	mux.HandleFunc("GET /dashboard/roi/data", h.requireSession(h.handleROIData))
	mux.HandleFunc("GET /dashboard/roi/export.csv", h.requireSession(h.handleROIExportCSV))

	// === Pages (mini-Notion) ===
	// Wave 5: fundación document-centric — tree padre-hijo + markdown editor + Cmd+K.
	mux.HandleFunc("GET /dashboard/pages", h.requireSession(h.handlePagesHome))
	mux.HandleFunc("GET /dashboard/pages/tree", h.requireSession(h.handlePagesTreePartial))
	mux.HandleFunc("GET /dashboard/pages/editor", h.requireSession(h.handlePagesEditor))
	mux.HandleFunc("POST /dashboard/pages/preview", h.requireSession(h.handlePagesPreview))
	mux.HandleFunc("POST /dashboard/pages/create", h.requireSession(h.handlePagesCreate))
	mux.HandleFunc("POST /dashboard/pages/{id}/update", h.requireSession(h.handlePagesUpdate))
	mux.HandleFunc("POST /dashboard/pages/{id}/move", h.requireSession(h.handlePagesMove))
	mux.HandleFunc("POST /dashboard/pages/{id}/archive", h.requireSession(h.handlePagesArchive))
	mux.HandleFunc("POST /dashboard/pages/{id}/restore", h.requireSession(h.handlePagesRestore))
	mux.HandleFunc("GET /dashboard/pages/{id}/revisions", h.requireSession(h.handlePagesRevisions))
	mux.HandleFunc("POST /dashboard/pages/{id}/revisions/{rev}/revert", h.requireSession(h.handlePagesRevert))

	// Cmd+K Quick Switcher cross-everything (pages + obs + skills + recipes + leads + quotes).
	mux.HandleFunc("GET /dashboard/quick-search", h.requireSession(h.handleQuickSearch))

	// === Knowledge base sync (wave 8) — admin-gated ===
	if cfg.KnowledgeBase != nil {
		mux.HandleFunc("GET /dashboard/knowledge-base", h.requireAdmin(h.handleKnowledgeBasePage))
		mux.HandleFunc("GET /dashboard/knowledge-base/list", h.requireAdmin(h.handleKnowledgeBaseList))
		mux.HandleFunc("GET /dashboard/knowledge-base/status", h.requireAdmin(h.handleKnowledgeBaseStatus))
		mux.HandleFunc("POST /dashboard/knowledge-base/resync-failed", h.requireAdmin(h.handleKnowledgeBaseResyncFailed))
		mux.HandleFunc("POST /dashboard/knowledge-base/refresh-index", h.requireAdmin(h.handleKnowledgeBaseRefreshIndex))
		mux.HandleFunc("POST /dashboard/knowledge-base/projects/{projectID}/resync", h.requireAdmin(h.handleKnowledgeBaseResyncProject))
		// Sync de cotización: cualquier rol comercial puede triggerear el sync.
		mux.HandleFunc("POST /dashboard/knowledge-base/quotes/{quoteID}/sync", h.requireAnyRole([]string{"admin", "cotizador"}, h.handleKnowledgeBaseSyncQuote))
	}

	// === Vault: bóveda de secretos (admin-gated) ===
	mux.HandleFunc("GET /dashboard/vault", h.requireAdmin(h.handleVaultPage))
	mux.HandleFunc("GET /dashboard/vault/list", h.requireAdmin(h.handleVaultList))
	mux.HandleFunc("POST /dashboard/vault/create", h.requireAdmin(h.handleVaultCreate))
	mux.HandleFunc("POST /dashboard/vault/{id}/reveal", h.requireAdmin(h.handleVaultReveal))
	mux.HandleFunc("POST /dashboard/vault/{id}/rotate", h.requireAdmin(h.handleVaultRotate))
	mux.HandleFunc("POST /dashboard/vault/{id}/delete", h.requireAdmin(h.handleVaultDelete))
	mux.HandleFunc("GET /dashboard/vault/{id}/audit", h.requireAdmin(h.handleVaultAuditDetail))
	mux.HandleFunc("GET /dashboard/vault/audit", h.requireAdmin(h.handleVaultAuditGlobal))
	return nil
}

func (h *handlers) handleAyudaPage(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	component := AyudaPage(p.Roles())
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Guía de uso", p.DisplayName(), "ayuda", p.Roles(), component))
}

func Handler() http.Handler {
	return HandlerWithStatus(staticSyncStatusProvider{status: SyncStatus{Phase: "idle"}})
}

func HandlerWithStatus(provider SyncStatusProvider) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		status := provider.Status()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(renderSyncStatusPage(status)))
	})
	return mux
}

func renderSyncStatusPage(status SyncStatus) string {
	code := status.ReasonCode
	message := status.ReasonMessage
	headline := reasonHeadline(status.ReasonCode)
	phase := status.Phase
	phase = html.EscapeString(phase)
	headline = html.EscapeString(headline)
	code = html.EscapeString(code)
	message = html.EscapeString(message)

	return fmt.Sprintf(`<html>
<head><title>AriaCore Cloud Dashboard</title></head>
<body>
  <main>
    <h1>AriaCore Cloud Dashboard</h1>
    <p>phase: %s</p>
    <section>
      <h2>%s</h2>
      <p>reason_code: %s</p>
      <p>reason_message: %s</p>
      <p>upgrade_stage: %s</p>
      <p>upgrade_reason_code: %s</p>
      <p>upgrade_reason_message: %s</p>
    </section>
  </main>
</body>
</html>`, phase, headline, code, message, html.EscapeString(status.UpgradeStage), html.EscapeString(status.UpgradeReasonCode), html.EscapeString(status.UpgradeReasonMessage))
}

func reasonHeadline(code string) string {
	switch code {
	case constants.ReasonBlockedUnenrolled:
		return "Blocked — project unenrolled"
	case constants.ReasonPaused:
		return "Paused"
	case constants.ReasonAuthRequired:
		return "Authentication required"
	case constants.ReasonTransportFailed:
		return "Transport failure"
	default:
		if code == "" {
			return "Healthy"
		}
		return "Sync issue"
	}
}

func (h *handlers) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","subsystem":"dashboard"}`))
}

// renderComponent renders a templ component to the HTTP response.
func renderComponent(w http.ResponseWriter, r *http.Request, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := component.Render(r.Context(), w); err != nil {
		log.Printf("dashboard: templ render error: %v", err)
	}
}

// renderWithToast renders the primary HTMX partial and appends an out-of-band
// toast to the global toast container. Variant: success | error | info.
func renderWithToast(w http.ResponseWriter, r *http.Request, component templ.Component, message, variant string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := component.Render(r.Context(), w); err != nil {
		log.Printf("dashboard: templ render error: %v", err)
		return
	}
	if err := ToastOOB(message, variant).Render(r.Context(), w); err != nil {
		log.Printf("dashboard: toast render error: %v", err)
	}
}

// renderComponentStatus renders a templ component with a specific HTTP status code.
func renderComponentStatus(w http.ResponseWriter, r *http.Request, status int, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := component.Render(r.Context(), w); err != nil {
		log.Printf("dashboard: templ render error: %v", err)
	}
}

func (h *handlers) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	next := sanitizeDashboardNext(r.URL.Query().Get("next"))
	if h.cfg.RequireSession != nil {
		if err := h.cfg.RequireSession(r); err == nil {
			http.Redirect(w, r, dashboardPostLoginPath(next), http.StatusSeeOther)
			return
		}
	}
	renderComponent(w, r, LoginPage("", next))
}

func (h *handlers) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if h.cfg.MaxLoginBodyBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, h.cfg.MaxLoginBodyBytes)
	}
	if err := r.ParseForm(); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, fmt.Sprintf("login payload too large (max %d bytes)", h.cfg.MaxLoginBodyBytes), http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid form payload", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.PostForm.Get("email"))
	password := r.PostForm.Get("password")
	tokenLegacy := strings.TrimSpace(r.PostForm.Get("token"))
	next := sanitizeDashboardNext(r.PostForm.Get("next"))
	if next == "" {
		next = sanitizeDashboardNext(r.URL.Query().Get("next"))
	}
	if h.cfg.RequireSession != nil {
		if err := h.cfg.RequireSession(r); err == nil {
			http.Redirect(w, r, dashboardPostLoginPath(next), http.StatusSeeOther)
			return
		}
	}

	var principal *LoginPrincipal

	switch {
	case email != "" && password != "" && h.cfg.ValidateCredentials != nil:
		p, err := h.cfg.ValidateCredentials(email, password)
		if err != nil || p == nil {
			renderComponent(w, r, LoginPage("invalid email or password", next))
			return
		}
		principal = p
	case tokenLegacy != "" && h.cfg.ValidateLoginToken != nil:
		if err := h.cfg.ValidateLoginToken(tokenLegacy); err != nil {
			renderComponent(w, r, LoginPage("invalid recovery token", next))
			return
		}
		principal = &LoginPrincipal{UID: "admin-recovery", Email: "admin@recovery.local", Roles: []string{"admin"}}
	default:
		renderComponent(w, r, LoginPage("email and password are required", next))
		return
	}

	if h.cfg.CreateSessionCookie != nil {
		if err := h.cfg.CreateSessionCookie(w, r, principal); err != nil {
			http.Error(w, "unable to create dashboard session", http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, dashboardPostLoginPath(next), http.StatusSeeOther)
}

func (h *handlers) handleLogout(w http.ResponseWriter, r *http.Request) {
	if h.cfg.ClearSessionCookie != nil {
		h.cfg.ClearSessionCookie(w, r)
	}
	http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
}

func (h *handlers) handleDashboardHome(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	if isHTMXRequest(r) {
		renderComponent(w, r, DashboardHome(p.DisplayName()))
		return
	}
	renderComponent(w, r, Layout("Inicio", p.DisplayName(), "dashboard", p.Roles(), DashboardHome(p.DisplayName())))
}

func (h *handlers) handleDashboardStats(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	overview := cloudstore.DashboardAdminOverview{}
	if h.cfg.Store != nil {
		loaded, err := h.cfg.Store.AdminOverview()
		if err != nil {
			h.renderStoreError(w, r, "dashboard", "Stats", err)
			return
		}
		overview = loaded
	}
	// R5-1: Build the stats body as raw HTML then wrap in templ Layout so the
	// status-ribbon, shell-footer, and "CLOUD ACTIVE" pill are always present on
	// full-page navigation (non-HTMX). Previously renderPageOrHTMX called the
	// string-based renderLayout which lacked those elements.
	body := fmt.Sprintf(`<section class="frame-section"><p class="section-kicker">STATS</p><h2>Cloud Stats</h2><div class="metric-strip"><a href="/dashboard/projects" class="metric-card stat-card-link"><span class="metric-value">%d</span><span class="metric-label">Projects</span></a><a href="/dashboard/contributors" class="metric-card stat-card-link"><span class="metric-value">%d</span><span class="metric-label">Contributors</span></a><a href="/dashboard/browser" class="metric-card stat-card-link"><span class="metric-value">%d</span><span class="metric-label">Chunks</span></a></div></section>`, overview.Projects, overview.Contributors, overview.Chunks)
	if isHTMXRequest(r) {
		renderHTML(w, body)
		return
	}
	renderComponent(w, r, Layout("Estadísticas", p.DisplayName(), "dashboard", p.Roles(), templ.Raw(body)))
}

func (h *handlers) handleDashboardActivity(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	rows := make([]cloudstore.DashboardObservationRow, 0)
	if h.cfg.Store != nil {
		loaded, err := h.cfg.Store.ListRecentObservations(project, query, 25)
		if err != nil {
			h.renderStoreError(w, r, "dashboard", "Activity", err)
			return
		}
		rows = loaded
	}
	b := strings.Builder{}
	b.WriteString(`<section class="frame-section"><p class="section-kicker">ACTIVITY</p><h2>Recent Observation Activity</h2>`)
	if len(rows) == 0 {
		b.WriteString(`<div class="empty-state"><h3>No Activity</h3><p>No recent observations are available.</p></div>`)
	} else {
		b.WriteString(`<table class="data-table"><thead><tr><th>Project</th><th>Type</th><th>Title</th><th>Session</th><th>Created</th></tr></thead><tbody>`)
		for _, row := range rows {
			b.WriteString(fmt.Sprintf(`<tr><td>%s</td><td>%s</td><td>%s</td><td><a href="%s">%s</a></td><td>%s</td></tr>`, html.EscapeString(row.Project), html.EscapeString(row.Type), html.EscapeString(row.Title), safeQuery("/dashboard/browser/sessions/"+url.PathEscape(row.SessionID), preserveQuery(r.URL.RawQuery, "project", row.Project)), html.EscapeString(row.SessionID), html.EscapeString(row.CreatedAt)))
		}
		b.WriteString(`</tbody></table>`)
	}
	b.WriteString(`</section>`)
	// R5-1: Use templ Layout for non-HTMX so status-ribbon and shell-footer are present.
	if isHTMXRequest(r) {
		renderHTML(w, b.String())
		return
	}
	renderComponent(w, r, Layout("Actividad", p.DisplayName(), "dashboard", p.Roles(), templ.Raw(b.String())))
}

func (h *handlers) handleBrowser(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	obsType := strings.TrimSpace(r.URL.Query().Get("type"))
	var projectNames []string
	var obsTypes []string
	if h.cfg.Store != nil {
		if projs, err := h.cfg.Store.ListProjects(""); err == nil {
			for _, pr := range projs {
				projectNames = append(projectNames, pr.Project)
			}
		}
		// Batch 6: source type pills from store (degrade gracefully on error).
		if types, err := h.cfg.Store.ListDistinctTypes(); err == nil {
			obsTypes = types
		}
	}
	component := BrowserPage(projectNames, obsTypes, project, query, obsType)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Memorias", p.DisplayName(), "browser", p.Roles(), component))
}

func (h *handlers) handleBrowserObservations(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	obsType := strings.TrimSpace(r.URL.Query().Get("type"))
	// R2-1: parse page/pageSize without pre-clamping (total not known yet).
	reqPage, pageSize := parsePaginationRaw(r)
	rows := make([]cloudstore.DashboardObservationRow, 0)
	total := 0
	if h.cfg.Store != nil {
		var err error
		rows, total, err = h.cfg.Store.ListRecentObservationsPaginated(project, query, obsType, pageSize, (reqPage-1)*pageSize)
		if err != nil {
			h.renderStoreError(w, r, "browser", "Observations", err)
			return
		}
	}
	// R2-1: re-clamp page to real totalPages; re-fetch if the requested page was beyond the end.
	// R3-7: on re-fetch error, log and keep previous rows rather than returning an error page.
	// R4-9: when re-fetch fails and rows are empty, attempt one additional fetch at page 1.
	pg, needsRefetch := reclampPagination(reqPage, pageSize, total)
	if needsRefetch && h.cfg.Store != nil {
		if refetched, _, err := h.cfg.Store.ListRecentObservationsPaginated(project, query, obsType, pageSize, pg.Offset()); err == nil {
			rows = refetched
		} else {
			log.Printf("dashboard: re-fetch observations page %d: %v (using first-page rows)", pg.Page, err)
			if len(rows) == 0 {
				if fallback, _, fallbackErr := h.cfg.Store.ListRecentObservationsPaginated(project, query, obsType, pageSize, 0); fallbackErr == nil {
					rows = fallback
				} else {
					log.Printf("dashboard: fallback observations page 1: %v", fallbackErr)
				}
			}
		}
	}
	partial := ObservationsPartial(rows, pg)
	if isHTMXRequest(r) {
		renderComponent(w, r, partial)
		return
	}
	renderComponent(w, r, Layout("Memorias", p.DisplayName(), "browser", p.Roles(), BrowserPage(nil, nil, project, query, obsType)))
}

func (h *handlers) handleBrowserSessions(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	// R2-1: parse page/pageSize without pre-clamping.
	reqPage, pageSize := parsePaginationRaw(r)
	rows := make([]cloudstore.DashboardSessionRow, 0)
	total := 0
	if h.cfg.Store != nil {
		var err error
		rows, total, err = h.cfg.Store.ListRecentSessionsPaginated(project, query, pageSize, (reqPage-1)*pageSize)
		if err != nil {
			h.renderStoreError(w, r, "browser", "Sessions", err)
			return
		}
	}
	// R2-1: re-clamp and re-fetch if needed.
	// R3-7: on re-fetch error, log and keep previous rows (graceful degradation).
	// R4-9: when re-fetch fails and rows are empty, attempt one additional fetch at page 1.
	pg, needsRefetch := reclampPagination(reqPage, pageSize, total)
	if needsRefetch && h.cfg.Store != nil {
		if refetched, _, err := h.cfg.Store.ListRecentSessionsPaginated(project, query, pageSize, pg.Offset()); err == nil {
			rows = refetched
		} else {
			log.Printf("dashboard: re-fetch sessions page %d: %v (using first-page rows)", pg.Page, err)
			if len(rows) == 0 {
				if fallback, _, fallbackErr := h.cfg.Store.ListRecentSessionsPaginated(project, query, pageSize, 0); fallbackErr == nil {
					rows = fallback
				} else {
					log.Printf("dashboard: fallback sessions page 1: %v", fallbackErr)
				}
			}
		}
	}
	partial := SessionsPartial(rows, pg)
	if isHTMXRequest(r) {
		renderComponent(w, r, partial)
		return
	}
	renderComponent(w, r, Layout("Memorias", p.DisplayName(), "browser", p.Roles(), BrowserPage(nil, nil, project, query, "")))
}

func (h *handlers) handleBrowserPrompts(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	// R2-1: parse page/pageSize without pre-clamping.
	reqPage, pageSize := parsePaginationRaw(r)
	rows := make([]cloudstore.DashboardPromptRow, 0)
	total := 0
	if h.cfg.Store != nil {
		var err error
		rows, total, err = h.cfg.Store.ListRecentPromptsPaginated(project, query, pageSize, (reqPage-1)*pageSize)
		if err != nil {
			h.renderStoreError(w, r, "browser", "Prompts", err)
			return
		}
	}
	// R2-1: re-clamp and re-fetch if needed.
	// R3-7: on re-fetch error, log and keep previous rows (graceful degradation).
	// R4-9: when re-fetch fails and rows are empty, attempt one additional fetch at page 1.
	pg, needsRefetch := reclampPagination(reqPage, pageSize, total)
	if needsRefetch && h.cfg.Store != nil {
		if refetched, _, err := h.cfg.Store.ListRecentPromptsPaginated(project, query, pageSize, pg.Offset()); err == nil {
			rows = refetched
		} else {
			log.Printf("dashboard: re-fetch prompts page %d: %v (using first-page rows)", pg.Page, err)
			if len(rows) == 0 {
				if fallback, _, fallbackErr := h.cfg.Store.ListRecentPromptsPaginated(project, query, pageSize, 0); fallbackErr == nil {
					rows = fallback
				} else {
					log.Printf("dashboard: fallback prompts page 1: %v", fallbackErr)
				}
			}
		}
	}
	partial := PromptsPartial(rows, pg)
	if isHTMXRequest(r) {
		renderComponent(w, r, partial)
		return
	}
	renderComponent(w, r, Layout("Memorias", p.DisplayName(), "browser", p.Roles(), BrowserPage(nil, nil, project, query, "")))
}

// handleBrowserSessionDetail handles GET /dashboard/browser/sessions/{sessionID}.
// R4-5: migrated to use principalFromRequest and renderComponentStatus for empty state.
// R5-6: use r.Clone to avoid mutating shared request state when delegating to handleBrowserSessions.
func (h *handlers) handleBrowserSessionDetail(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	if sessionID == "" {
		renderComponentStatus(w, r, http.StatusNotFound, Layout("Detalle de Sesión", p.DisplayName(), "browser", p.Roles(), EmptyState("Session Not Found", "No dashboard data exists for that session identifier.")))
		return
	}
	// Clone the request before mutating URL so the original request is not modified.
	r2 := r.Clone(r.Context())
	r2.URL.RawQuery = preserveQuery(r.URL.RawQuery, "q", sessionID)
	h.handleBrowserSessions(w, r2)
}

func (h *handlers) handleProjects(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	component := ProjectsPage(query)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Proyectos", p.DisplayName(), "projects", p.Roles(), component))
}

func (h *handlers) handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	project := strings.TrimSpace(r.PathValue("project"))
	if project == "" {
		renderComponentStatus(w, r, http.StatusNotFound, Layout("Detalle de Proyecto", p.DisplayName(), "projects", p.Roles(), EmptyState("Project Not Found", "No replicated dashboard data exists for that project.")))
		return
	}
	var stats *cloudstore.DashboardProjectRow
	var ctrl *cloudstore.ProjectSyncControl
	if h.cfg.Store != nil {
		detail, err := h.cfg.Store.ProjectDetail(project)
		if err != nil {
			h.renderStoreError(w, r, "projects", "Project detail", err)
			return
		}
		statsRow := detail.Stats
		stats = &statsRow
		// Degrade gracefully: if sync control lookup fails, render without pause audit.
		if c, err := h.cfg.Store.GetProjectSyncControl(project); err == nil {
			ctrl = c
		}
	}
	component := ProjectDetailPage(project, stats, ctrl)
	renderComponent(w, r, Layout("Detalle de Proyecto", p.DisplayName(), "projects", p.Roles(), component))
}

func (h *handlers) handleContributors(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	// R6-1: serve only the shell; the list is loaded via HTMX from /dashboard/contributors/list.
	// This mirrors the ProjectsPage pattern — no store call at the shell level.
	component := ContributorsPage(query)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Colaboradores", p.DisplayName(), "contributors", p.Roles(), component))
}

// handleContributorsList handles GET /dashboard/contributors/list.
// R5-2: always returns ContributorsListPartial (no full page wrapper) so HTMX
// pagination targets can swap just the content div.
// R6-2: on store error, always renders a fragment (no Layout wrapper) — partial-only contract.
func (h *handlers) handleContributorsList(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	reqPage, pageSize := parsePaginationRaw(r)
	rows := make([]cloudstore.DashboardContributorRow, 0)
	total := 0
	if h.cfg.Store != nil {
		var err error
		rows, total, err = h.cfg.Store.ListContributorsPaginated(query, pageSize, (reqPage-1)*pageSize)
		if err != nil {
			log.Printf("dashboard: contributors list store error: %v", err)
			renderComponentStatus(w, r, http.StatusBadGateway, EmptyState("Service Unavailable", "Dashboard data is temporarily unavailable."))
			return
		}
	}
	pg, needsRefetch := reclampPagination(reqPage, pageSize, total)
	if needsRefetch && h.cfg.Store != nil {
		if refetched, _, err := h.cfg.Store.ListContributorsPaginated(query, pageSize, pg.Offset()); err == nil {
			rows = refetched
		} else {
			log.Printf("dashboard: re-fetch contributors list page %d: %v", pg.Page, err)
			if len(rows) == 0 {
				if fallback, _, fallbackErr := h.cfg.Store.ListContributorsPaginated(query, pageSize, 0); fallbackErr == nil {
					rows = fallback
				} else {
					log.Printf("dashboard: fallback contributors list page 1: %v", fallbackErr)
				}
			}
		}
	}
	renderComponent(w, r, ContributorsListPartial(rows, pg))
}

func (h *handlers) handleContributorDetail(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	contributor := strings.TrimSpace(r.PathValue("contributor"))
	if contributor == "" {
		renderComponentStatus(w, r, http.StatusNotFound, Layout("Detalle de Colaborador", p.DisplayName(), "contributors", p.Roles(), EmptyState("Contributor Not Found", "No dashboard data exists for that contributor.")))
		return
	}
	if h.cfg.Store == nil {
		renderComponent(w, r, Layout("Detalle de Colaborador", p.DisplayName(), "contributors", p.Roles(), ContributorDetailPage(nil, nil, nil, nil)))
		return
	}
	row, sessions, observations, prompts, err := h.cfg.Store.GetContributorDetail(contributor)
	if err != nil {
		h.renderStoreError(w, r, "contributors", "Contributor detail", err)
		return
	}
	component := ContributorDetailPage(&row, sessions, observations, prompts)
	renderComponent(w, r, Layout("Detalle de Colaborador", p.DisplayName(), "contributors", p.Roles(), component))
}

func (h *handlers) handleAdmin(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	if !p.IsAdmin() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var health *cloudstore.DashboardSystemHealth
	var controls []cloudstore.ProjectSyncControl
	if h.cfg.Store != nil {
		if sh, err := h.cfg.Store.SystemHealth(); err == nil {
			health = &sh
		}
		if ctrls, err := h.cfg.Store.ListProjectSyncControls(); err == nil {
			controls = ctrls
		}
	}
	component := AdminPage(health, controls)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Administración", p.DisplayName(), "admin", p.Roles(), component))
}

// handleAdminProjectControls handles GET /dashboard/admin/projects.
// Batch 6: renders AdminProjectsPage templ with sync controls, replacing the
// previous delegation to handleProjects which had no toggle UI.
func (h *handlers) handleAdminProjectControls(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	if !p.IsAdmin() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var controls []cloudstore.ProjectSyncControl
	if h.cfg.Store != nil {
		// Degrade gracefully: empty controls if store fails.
		if ctrls, err := h.cfg.Store.ListProjectSyncControls(); err == nil {
			controls = ctrls
		}
	}
	component := AdminProjectsPage(controls)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Admin Projects", p.DisplayName(), "admin", p.Roles(), component))
}

// ─── 11 New Handler Implementations (visual parity batch) ────────────────────

// handleProjectsList handles GET /dashboard/projects/list (HTMX partial).
// Batch 6: passes sync controls map so Paused badge renders correctly.
// R4-3: uses parsePaginationRaw + reclampPagination so >50 projects are reachable.
func (h *handlers) handleProjectsList(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	// R4-3: parse raw page/pageSize (no pre-clamp) before first store call.
	reqPage, pageSize := parsePaginationRaw(r)
	rows := make([]cloudstore.DashboardProjectRow, 0)
	total := 0
	var controlsMap map[string]cloudstore.ProjectSyncControl
	if h.cfg.Store != nil {
		var err error
		rows, total, err = h.cfg.Store.ListProjectsPaginated(query, pageSize, (reqPage-1)*pageSize)
		if err != nil {
			// R6-2: partial-only endpoint — always render fragment, never full Layout (even non-HTMX).
			log.Printf("dashboard: projects list store error: %v", err)
			renderComponentStatus(w, r, http.StatusBadGateway, EmptyState("Service Unavailable", "Dashboard data is temporarily unavailable."))
			return
		}
		// Degrade gracefully: if controls fail, render without badges.
		if ctrls, err := h.cfg.Store.ListProjectSyncControls(); err == nil {
			controlsMap = controlsByProject(ctrls)
		}
	}
	// R4-3: re-clamp to real total; re-fetch if requested page was beyond last page.
	// R5-3: add tier-3 fallback — if clamped re-fetch fails AND rows are empty, attempt page 1.
	pg, needsRefetch := reclampPagination(reqPage, pageSize, total)
	if needsRefetch && h.cfg.Store != nil {
		if refetched, _, err := h.cfg.Store.ListProjectsPaginated(query, pageSize, pg.Offset()); err == nil {
			rows = refetched
		} else {
			log.Printf("dashboard: re-fetch projects list page %d: %v (using first-page rows)", pg.Page, err)
			// R5-3: tier-3 fallback to page 1 when re-fetch fails and rows are empty.
			if len(rows) == 0 {
				if fallback, _, fallbackErr := h.cfg.Store.ListProjectsPaginated(query, pageSize, 0); fallbackErr == nil {
					rows = fallback
				} else {
					log.Printf("dashboard: fallback projects list page 1: %v", fallbackErr)
				}
			}
		}
	}
	renderComponent(w, r, ProjectsListPartial(rows, controlsMap, pg))
}

// handleProjectObservationsPartial handles GET /dashboard/projects/{name}/observations.
func (h *handlers) handleProjectObservationsPartial(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	obsType := strings.TrimSpace(r.URL.Query().Get("type"))
	rows := make([]cloudstore.DashboardObservationRow, 0)
	if h.cfg.Store != nil {
		var err error
		rows, _, err = h.cfg.Store.ListRecentObservationsPaginated(name, query, obsType, 50, 0)
		if err != nil {
			h.renderStoreError(w, r, "projects", "Project observations", err)
			return
		}
	}
	renderComponent(w, r, ObservationsPartial(rows, Pagination{}))
}

// handleProjectSessionsPartial handles GET /dashboard/projects/{name}/sessions.
func (h *handlers) handleProjectSessionsPartial(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	rows := make([]cloudstore.DashboardSessionRow, 0)
	if h.cfg.Store != nil {
		var err error
		rows, _, err = h.cfg.Store.ListRecentSessionsPaginated(name, query, 50, 0)
		if err != nil {
			h.renderStoreError(w, r, "projects", "Project sessions", err)
			return
		}
	}
	renderComponent(w, r, SessionsPartial(rows, Pagination{}))
}

// handleProjectPromptsPartial handles GET /dashboard/projects/{name}/prompts.
func (h *handlers) handleProjectPromptsPartial(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	rows := make([]cloudstore.DashboardPromptRow, 0)
	if h.cfg.Store != nil {
		var err error
		rows, _, err = h.cfg.Store.ListRecentPromptsPaginated(name, query, 50, 0)
		if err != nil {
			h.renderStoreError(w, r, "projects", "Project prompts", err)
			return
		}
	}
	renderComponent(w, r, PromptsPartial(rows, Pagination{}))
}

// handleAdminUsers handles GET /dashboard/admin/users — shell only.
func (h *handlers) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	component := AdminUsersPage()
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Usuarios", p.DisplayName(), "admin", p.Roles(), component))
}

// handleAdminUsersList handles GET /dashboard/admin/users/list — tabla de users desde AdminUsers service.
func (h *handlers) handleAdminUsersList(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AdminUsers == nil {
		renderComponent(w, r, AdminUsersListPartial(nil, "user management is not configured"))
		return
	}
	users, err := h.cfg.AdminUsers.ListUsers(r.Context())
	if err != nil {
		log.Printf("dashboard: admin users list error: %v", err)
		renderComponent(w, r, AdminUsersListPartial(nil, "no se pudo cargar la lista de usuarios"))
		return
	}
	renderComponent(w, r, AdminUsersListPartial(users, ""))
}

// handleAdminUserCreate handles POST /dashboard/admin/users/create.
// Acepta múltiples valores en form field "roles" (checkboxes). Si está vacío, default ["dev"].
func (h *handlers) handleAdminUserCreate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AdminUsers == nil {
		http.Error(w, "user management not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.PostForm.Get("email"))
	name := strings.TrimSpace(r.PostForm.Get("name"))
	roles := r.PostForm["roles"]
	if len(roles) == 0 {
		roles = []string{"dev"}
	}
	password := r.PostForm.Get("password")
	sendWelcome := r.PostForm.Get("send_welcome") == "on" || r.PostForm.Get("send_welcome") == "true"
	if err := h.cfg.AdminUsers.CreateUser(r.Context(), email, name, roles, password); err != nil {
		renderWithToast(w, r, AdminUsersListPartial(nil, fmt.Sprintf("error: %v", err)), "No se pudo crear el usuario: "+err.Error(), "error")
		return
	}

	// Welcome email opcional. No bloquea creación si falla.
	mailNote := ""
	if sendWelcome {
		if h.cfg.WelcomeMailer == nil {
			mailNote = " (email no enviado: mailer no configurado)"
		} else {
			creator := "un admin de iTechDev"
			if h.cfg.GetDisplayName != nil {
				if n := strings.TrimSpace(h.cfg.GetDisplayName(r)); n != "" {
					creator = n
				}
			}
			err := h.cfg.WelcomeMailer.SendWelcome(r.Context(), WelcomeParams{
				Name:      name,
				Email:     email,
				Password:  password,
				Roles:     roles,
				CreatedBy: creator,
			})
			if err != nil {
				log.Printf("dashboard: send welcome email failed: %v", err)
				mailNote = " (email no enviado: " + err.Error() + ")"
			} else {
				mailNote = " · email de bienvenida enviado"
			}
		}
	}

	users, err := h.cfg.AdminUsers.ListUsers(r.Context())
	if err != nil {
		renderWithToast(w, r, AdminUsersListPartial(nil, "user creado pero no se pudo recargar la lista"), "Usuario creado"+mailNote+", pero no pudo recargar la lista", "info")
		return
	}
	renderWithToast(w, r, AdminUsersListPartial(users, ""), "Usuario "+email+" creado"+mailNote, "success")
}

// handleAdminUserAddRole POST /dashboard/admin/users/{uid}/roles/{role}/add
func (h *handlers) handleAdminUserAddRole(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AdminUsers == nil {
		http.Error(w, "user management not configured", http.StatusServiceUnavailable)
		return
	}
	uid := r.PathValue("uid")
	role := r.PathValue("role")
	if err := h.cfg.AdminUsers.AddRole(r.Context(), uid, role); err != nil {
		http.Error(w, fmt.Sprintf("add role: %v", err), http.StatusBadRequest)
		return
	}
	h.renderSingleUserRow(w, r, uid)
}

// handleAdminUserRemoveRole POST /dashboard/admin/users/{uid}/roles/{role}/remove
func (h *handlers) handleAdminUserRemoveRole(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AdminUsers == nil {
		http.Error(w, "user management not configured", http.StatusServiceUnavailable)
		return
	}
	uid := r.PathValue("uid")
	role := r.PathValue("role")
	if err := h.cfg.AdminUsers.RemoveRole(r.Context(), uid, role); err != nil {
		http.Error(w, fmt.Sprintf("remove role: %v", err), http.StatusBadRequest)
		return
	}
	h.renderSingleUserRow(w, r, uid)
}

// handleAdminUserSetActive returns a handler that toggles is_active.
func (h *handlers) handleAdminUserSetActive(active bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.cfg.AdminUsers == nil {
			http.Error(w, "user management not configured", http.StatusServiceUnavailable)
			return
		}
		uid := r.PathValue("uid")
		if err := h.cfg.AdminUsers.SetActive(r.Context(), uid, active); err != nil {
			http.Error(w, fmt.Sprintf("set active: %v", err), http.StatusBadRequest)
			return
		}
		h.renderSingleUserRow(w, r, uid)
	}
}

// handleAdminUserChangePassword handles POST /dashboard/admin/users/{uid}/password.
func (h *handlers) handleAdminUserChangePassword(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AdminUsers == nil {
		http.Error(w, "user management not configured", http.StatusServiceUnavailable)
		return
	}
	uid := r.PathValue("uid")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	password := r.PostForm.Get("password")
	if err := h.cfg.AdminUsers.ChangePassword(r.Context(), uid, password); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<div class="login-error" role="alert">%s</div>`, html.EscapeString(err.Error()))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<div class="muted">password actualizado para %s</div>`, html.EscapeString(uid))
}

// handleAdminInviteCreate POST /dashboard/admin/users/invite — admin only.
// Genera magic-link invite + dispara email. Devuelve fragmento HTML con flash.
func (h *handlers) handleAdminInviteCreate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Invites == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<div class="login-error" role="alert">El módulo de invitaciones no está configurado.</div>`)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	emailAddr := strings.TrimSpace(strings.ToLower(r.PostForm.Get("email")))
	roles := r.PostForm["roles"]
	if emailAddr == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<div class="login-error" role="alert">Email es requerido.</div>`)
		return
	}
	if len(roles) == 0 {
		roles = []string{"dev"}
	}
	// Resolver el invitedBy desde el principal de la sesión.
	invitedByUID := ""
	invitedByEmail := ""
	if h.cfg.GetDisplayName != nil {
		invitedByEmail = h.cfg.GetDisplayName(r)
	}
	link, emailSent, info, err := h.cfg.Invites.CreateAndSend(r.Context(), emailAddr, roles, invitedByUID, invitedByEmail)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err != nil {
		_, _ = fmt.Fprintf(w, `<div class="login-error" role="alert">No se pudo crear la invitación: %s</div>`, html.EscapeString(err.Error()))
		_ = ToastOOB("No se pudo crear la invitación: "+err.Error(), "error").Render(r.Context(), w)
		return
	}
	if emailSent {
		_, _ = fmt.Fprintf(w, `<div class="muted" role="status">Invitación enviada a <strong>%s</strong>. Link: <code>%s</code></div>`,
			html.EscapeString(emailAddr), html.EscapeString(link))
		_ = ToastOOB("Invitación enviada a "+emailAddr, "success").Render(r.Context(), w)
	} else {
		msg := info
		if strings.TrimSpace(msg) == "" {
			msg = "El email no se envió (módulo no configurado). Copiá el link manualmente:"
		}
		_, _ = fmt.Fprintf(w, `<div class="muted" role="status">%s <code>%s</code></div>`,
			html.EscapeString(msg), html.EscapeString(link))
		_ = ToastOOB("Invitación creada. Email no enviado — copiá el link.", "info").Render(r.Context(), w)
	}
}

// renderSingleUserRow re-fetches the user list and re-renders just the row matching uid.
// Simplification: re-renders entire table. Lo justo y suficiente para HTMX outerHTML.
func (h *handlers) renderSingleUserRow(w http.ResponseWriter, r *http.Request, uid string) {
	users, err := h.cfg.AdminUsers.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "list users: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for _, u := range users {
		if u.UID == uid {
			renderComponent(w, r, adminUserRow(u))
			return
		}
	}
	http.Error(w, "user not found", http.StatusNotFound)
}

// handleAdminHealth handles GET /dashboard/admin/health.
func (h *handlers) handleAdminHealth(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	if !p.IsAdmin() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var health *cloudstore.DashboardSystemHealth
	if h.cfg.Store != nil {
		if sh, err := h.cfg.Store.SystemHealth(); err == nil {
			health = &sh
		}
	}
	component := AdminHealthPage(health)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Salud del Sistema", p.DisplayName(), "admin", p.Roles(), component))
}

// handleAdminSyncTogglePost handles POST /dashboard/admin/projects/{name}/sync.
// Admin-gated. Sets sync enabled/disabled for the project. Satisfies REQ-112, AD-6.
func (h *handlers) handleAdminSyncTogglePost(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	if !p.IsAdmin() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	enabledRaw := strings.TrimSpace(r.FormValue("enabled"))
	if enabledRaw != "true" && enabledRaw != "false" {
		http.Error(w, "invalid value for enabled: must be 'true' or 'false'", http.StatusBadRequest)
		return
	}
	enabled := enabledRaw == "true"
	reason := strings.TrimSpace(r.FormValue("reason"))
	if h.cfg.Store != nil {
		if err := h.cfg.Store.SetProjectSyncEnabled(name, enabled, p.DisplayName(), reason); err != nil {
			http.Error(w, "store error", http.StatusInternalServerError)
			return
		}
	}
	redirectURL := "/dashboard/admin/projects"
	// R2-3: For HTMX requests, return 200 + HX-Redirect only.
	// http.Redirect writes a 303 regardless; HTMX intercepts 303 and follows natively,
	// making HX-Redirect irrelevant. For plain browser forms, keep the 303.
	if isHTMXRequest(r) {
		w.Header().Set("HX-Redirect", redirectURL)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

// handleAdminSyncToggleForm handles GET /dashboard/admin/projects/{name}/sync/form.
func (h *handlers) handleAdminSyncToggleForm(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	if !p.IsAdmin() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	ctrl := cloudstore.ProjectSyncControl{Project: name, SyncEnabled: true}
	if h.cfg.Store != nil {
		if c, err := h.cfg.Store.GetProjectSyncControl(name); err == nil && c != nil {
			ctrl = *c
		}
	}
	renderComponent(w, r, AdminSyncToggleFormPartial(ctrl))
}

// handleSessionDetail handles GET /dashboard/sessions/{project}/{sessionID}.
// Satisfies REQ-106, Design Decision 3, Design Decision 5.
func (h *handlers) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	project := strings.TrimSpace(r.PathValue("project"))
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	if project == "" || sessionID == "" || len(sessionID) > 128 {
		renderComponentStatus(w, r, http.StatusNotFound, Layout("Session", p.DisplayName(), "browser", p.Roles(), EmptyState("Session Not Found", "Invalid session identifier.")))
		return
	}
	var sess *cloudstore.DashboardSessionRow
	var obs []cloudstore.DashboardObservationRow
	var prompts []cloudstore.DashboardPromptRow
	if h.cfg.Store != nil {
		s, o, pr, err := h.cfg.Store.GetSessionDetail(project, sessionID)
		if err != nil {
			h.renderStoreError(w, r, "browser", "Session detail", err)
			return
		}
		sess = &s
		obs = o
		prompts = pr
	}
	component := SessionDetailPage(sess, obs, prompts)
	renderComponent(w, r, Layout("Detalle de Sesión", p.DisplayName(), "browser", p.Roles(), component))
}

// handleObservationDetail handles GET /dashboard/observations/{project}/{sessionID}/{syncID}.
func (h *handlers) handleObservationDetail(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	project := strings.TrimSpace(r.PathValue("project"))
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	syncID := strings.TrimSpace(r.PathValue("syncID"))
	if project == "" || sessionID == "" || syncID == "" || len(syncID) > 128 {
		renderComponentStatus(w, r, http.StatusNotFound, Layout("Observation", p.DisplayName(), "browser", p.Roles(), EmptyState("Observation Not Found", "Invalid observation identifier.")))
		return
	}
	var obs *cloudstore.DashboardObservationRow
	var sess *cloudstore.DashboardSessionRow
	var related []cloudstore.DashboardObservationRow
	if h.cfg.Store != nil {
		o, s, rel, err := h.cfg.Store.GetObservationDetail(project, sessionID, syncID)
		if err != nil {
			h.renderStoreError(w, r, "browser", "Observation detail", err)
			return
		}
		obs = &o
		sess = &s
		related = rel
	}
	component := ObservationDetailPage(obs, sess, related)
	renderComponent(w, r, Layout("Detalle de Observación", p.DisplayName(), "browser", p.Roles(), component))
}

// handlePromptDetail handles GET /dashboard/prompts/{project}/{sessionID}/{syncID}.
func (h *handlers) handlePromptDetail(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	project := strings.TrimSpace(r.PathValue("project"))
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	syncID := strings.TrimSpace(r.PathValue("syncID"))
	if project == "" || sessionID == "" || syncID == "" || len(syncID) > 128 {
		renderComponentStatus(w, r, http.StatusNotFound, Layout("Prompt", p.DisplayName(), "browser", p.Roles(), EmptyState("Prompt Not Found", "Invalid prompt identifier.")))
		return
	}
	var prompt *cloudstore.DashboardPromptRow
	var sess *cloudstore.DashboardSessionRow
	var related []cloudstore.DashboardPromptRow
	if h.cfg.Store != nil {
		pr, s, rel, err := h.cfg.Store.GetPromptDetail(project, sessionID, syncID)
		if err != nil {
			h.renderStoreError(w, r, "browser", "Prompt detail", err)
			return
		}
		prompt = &pr
		sess = &s
		related = rel
	}
	component := PromptDetailPage(prompt, sess, related)
	renderComponent(w, r, Layout("Detalle de Prompt", p.DisplayName(), "browser", p.Roles(), component))
}

// renderObservationsTable removed in Batch 6 REFACTOR — replaced by ObservationsPartial templ component.

func (h *handlers) renderStoreError(w http.ResponseWriter, r *http.Request, activeTab string, contextLabel string, err error) {
	status, headline, message := classifyStoreError(contextLabel, err)
	log.Printf("dashboard: %s store error: %v", strings.ToLower(strings.TrimSpace(contextLabel)), err)
	fragment := fmt.Sprintf(`<div class="empty-state" role="alert"><h3>%s</h3><p>%s</p></div>`, html.EscapeString(headline), html.EscapeString(message))
	if isHTMXRequest(r) {
		renderHTMLStatus(w, status, fragment)
		return
	}
	// R5-1: Use templ Layout for non-HTMX error pages so status-ribbon and shell-footer
	// are always present regardless of which handler generated the error.
	p := h.principalFromRequest(r)
	body := fmt.Sprintf(`<section class="frame-section"><p class="section-kicker">DEGRADED</p><h2>%s</h2>%s</section>`, html.EscapeString(contextLabel), fragment)
	renderComponentStatus(w, r, status, Layout(contextLabel, p.DisplayName(), activeTab, p.Roles(), templ.Raw(body)))
}

func classifyStoreError(contextLabel string, err error) (int, string, string) {
	switch {
	case errors.Is(err, cloudstore.ErrDashboardProjectInvalid):
		return http.StatusNotFound, "Project not found", "No replicated dashboard data exists for that project."
	case errors.Is(err, cloudstore.ErrDashboardProjectForbidden):
		return http.StatusForbidden, "Project access denied", "You are not allowed to access that project scope."
	case errors.Is(err, cloudstore.ErrDashboardProjectNotFound):
		return http.StatusNotFound, "Project not found", "No replicated dashboard data exists for that project."
	// R4-7: Contributor not found must produce a contributor-specific message, not "Project not found".
	case errors.Is(err, cloudstore.ErrDashboardContributorNotFound):
		return http.StatusNotFound, "Contributor not found", "No contributor with that name has been seen in this cloud workspace."
	// R5-4: Session/observation/prompt not found — entity-specific error pages.
	case errors.Is(err, cloudstore.ErrDashboardSessionNotFound):
		return http.StatusNotFound, "Session not found", "No session with that identifier exists in the specified project."
	case errors.Is(err, cloudstore.ErrDashboardObservationNotFound):
		return http.StatusNotFound, "Observation not found", "No observation with that identifier exists in the specified session."
	case errors.Is(err, cloudstore.ErrDashboardPromptNotFound):
		return http.StatusNotFound, "Prompt not found", "No prompt with that identifier exists in the specified session."
	default:
		heading := strings.TrimSpace(contextLabel)
		if heading == "" {
			heading = "Dashboard data"
		}
		return http.StatusServiceUnavailable, heading + " unavailable", "Cloud dashboard data is temporarily unavailable."
	}
}

func isHTMXRequest(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("HX-Request")), "true")
}

// renderBrowserBody, renderProjectSessions, renderProjectObservations, renderProjectPrompts
// removed in Batch 6 REFACTOR — replaced by templ partials (BrowserPage, SessionsPartial,
// ObservationsPartial, PromptsPartial, ProjectDetailPage).
// renderPageOrHTMX removed in R5-1 REFACTOR — callers now use renderComponent(w, r, Layout(...)).

// renderLoginPage removed in Batch 6 REFACTOR — replaced by LoginPage templ component.
// renderLayout, shellNavLink removed in R5-1 REFACTOR — all handlers now use the templ Layout component
// which includes status-ribbon, shell-footer, and CLOUD ACTIVE pill.

func renderHTML(w http.ResponseWriter, body string) {
	renderHTMLStatus(w, http.StatusOK, body)
}

func renderHTMLStatus(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// ─── Audit Log Handlers ───────────────────────────────────────────────────────

// handleAdminAuditLog handles GET /dashboard/admin/audit-log (shell, admin-gated).
// REQ-408: renders the AdminAuditLogPage templ component.
// JW2: filter is parsed and forwarded to the initial hx-get URL for deep-linking.
// JW6: invalid time formats yield 400.
func (h *handlers) handleAdminAuditLog(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	if !p.IsAdmin() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	filter, filterErr := parseAuditFilter(r)
	if filterErr != "" {
		http.Error(w, filterErr, http.StatusBadRequest)
		return
	}
	component := AdminAuditLogPage(p.DisplayName(), filter)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Bitácora de Auditoría", p.DisplayName(), "admin", p.Roles(), component))
}

// handleAdminAuditLogList handles GET /dashboard/admin/audit-log/list (partial, admin-gated, HTMX).
// REQ-409: renders AdminAuditLogListPartial with filter and pagination from query params.
// JW6: invalid time format in from/to params yields 400 instead of silent drop.
// N7: partial-only endpoint — always renders fragment, never a full Layout wrapper
// (even for non-HTMX requests). Consistent with R6-2 partial-only contract.
func (h *handlers) handleAdminAuditLogList(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	if !p.IsAdmin() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	filter, filterErr := parseAuditFilter(r)
	if filterErr != "" {
		http.Error(w, filterErr, http.StatusBadRequest)
		return
	}
	reqPage, pageSize := parsePaginationRaw(r)

	var rows []cloudstore.DashboardAuditRow
	var total int
	if h.cfg.Store != nil {
		var err error
		rows, total, err = h.cfg.Store.ListAuditEntriesPaginated(r.Context(), filter, pageSize, (reqPage-1)*pageSize)
		if err != nil {
			log.Printf("dashboard: audit log list store error: %v", err)
			renderComponentStatus(w, r, http.StatusBadGateway, EmptyState("Service Unavailable", "Audit log data is temporarily unavailable."))
			return
		}
	}

	// JW3: three-tier fallback pattern — consistent with other paginated handlers.
	// Tier 1: initial fetch (above). Tier 2: clamped re-fetch on page-out-of-range.
	// Tier 3: page-1 fallback when re-fetch fails and rows are empty.
	pg, needsRefetch := reclampPagination(reqPage, pageSize, total)
	if needsRefetch && h.cfg.Store != nil {
		if refetched, _, err := h.cfg.Store.ListAuditEntriesPaginated(r.Context(), filter, pageSize, pg.Offset()); err == nil {
			rows = refetched
		} else {
			log.Printf("dashboard: re-fetch audit log list page %d: %v (using first-page rows)", pg.Page, err)
			if len(rows) == 0 {
				if fallback, _, fallbackErr := h.cfg.Store.ListAuditEntriesPaginated(r.Context(), filter, pageSize, 0); fallbackErr == nil {
					rows = fallback
				} else {
					log.Printf("dashboard: fallback audit log list page 1: %v", fallbackErr)
				}
			}
		}
	}

	renderComponent(w, r, AdminAuditLogListPartial(rows, pg, filter))
}

// parseAuditTime tries RFC3339 then date-only (2006-01-02) formats.
// Returns an error only when the value is non-empty and unparseable in either format.
// JW6: accepting date-only prevents confusing silent drops while still being lenient.
func parseAuditTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	// Try RFC3339 first (most specific).
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	// Fall back to date-only YYYY-MM-DD (midnight UTC).
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("invalid_time_format: %q is not RFC3339 or YYYY-MM-DD", value)
}

// parseAuditFilter extracts AuditFilter fields from the request query params.
// Text filters are trimmed; time filters accept RFC3339 or date-only (YYYY-MM-DD). REQ-410.
// Returns the filter and an error string; on error, error is non-empty and the caller
// should return a 400 response. JW6 fix.
func parseAuditFilter(r *http.Request) (cloudstore.AuditFilter, string) {
	q := r.URL.Query()
	filter := cloudstore.AuditFilter{
		Contributor: strings.TrimSpace(q.Get("contributor")),
		Project:     strings.TrimSpace(q.Get("project")),
		Outcome:     strings.TrimSpace(q.Get("outcome")),
	}
	if from := strings.TrimSpace(q.Get("from")); from != "" {
		t, err := parseAuditTime(from)
		if err != nil {
			return cloudstore.AuditFilter{}, err.Error()
		}
		filter.OccurredAtFrom = t
	}
	if to := strings.TrimSpace(q.Get("to")); to != "" {
		t, err := parseAuditTime(to)
		if err != nil {
			return cloudstore.AuditFilter{}, err.Error()
		}
		filter.OccurredAtTo = t
	}
	return filter, ""
}
