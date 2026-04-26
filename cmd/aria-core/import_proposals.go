package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cotizador"
)

// adminImportProposals importa las 3 propuestas docx de iTechDev como
// leads + RFPs + quotes con secciones markdown completas.
// Idempotente: usa folio UNIQUE para evitar duplicados.
func adminImportProposals(_ []string) {
	dsn := strings.TrimSpace(os.Getenv("ARIA_CORE_DATABASE_URL"))
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "ARIA_CORE_DATABASE_URL is required")
		exitFunc(1)
		return
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		exitFunc(1)
		return
	}
	defer db.Close()
	store := cotizador.New(db)
	ctx := context.Background()

	imported := 0
	for _, p := range proposalsToImport() {
		if err := importOneProposal(ctx, store, db, p); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s: %v\n", p.Folio, err)
			continue
		}
		fmt.Printf("✓ %s — %s @ %s\n", p.Folio, p.LeadName, p.LeadCompany)
		imported++
	}
	fmt.Printf("\n%d propuestas importadas\n", imported)
}

type proposalImport struct {
	Folio        string
	ProposalType string
	ProductName  string
	ProductSubtitle string
	Tags         []string
	LeadName     string
	LeadCompany  string
	LeadEmail    string
	LeadStatus   string // 'new' por default; 'quoting' si quote draft; 'won' si approved

	PreparedForArea         string
	PreparedForContactName  string
	PreparedForContactEmail string

	IssueDate    time.Time
	ValidDays    int
	Currency     string
	Items        []cotizador.CreateQuoteItemParams
	Sections     []sectionImport
	Justification string
	Terms        string

	RFPText string
}

type sectionImport struct {
	Key       string
	Title     string
	ContentMD string
	SortOrder int
}

func importOneProposal(ctx context.Context, store *cotizador.Store, db *sql.DB, p proposalImport) error {
	// Verificar si folio ya existe (idempotencia)
	var existsCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cotizador_quotes WHERE folio = $1`, p.Folio).Scan(&existsCount); err != nil {
		return fmt.Errorf("check existing folio: %w", err)
	}
	if existsCount > 0 {
		return fmt.Errorf("folio ya existe (skipping)")
	}

	// Crear lead
	lead, err := store.CreateLead(ctx, cotizador.CreateLeadParams{
		Name: p.LeadName, Company: p.LeadCompany, Email: p.LeadEmail,
		Source: "histórico import", Notes: "Importado desde docx histórico iTechDev",
		Role: "cotizador",
	})
	if err != nil {
		return fmt.Errorf("create lead: %w", err)
	}

	// RFP opcional
	rfpID := ""
	if strings.TrimSpace(p.RFPText) != "" {
		rfp, err := store.CreateRFP(ctx, cotizador.CreateRFPParams{
			LeadID: lead.ID, SourceType: "text", SourceContent: p.RFPText,
		})
		if err != nil {
			return fmt.Errorf("create rfp: %w", err)
		}
		rfpID = rfp.ID
	}

	// Crear quote
	validUntil := p.IssueDate.AddDate(0, 0, p.ValidDays)
	q, err := store.CreateQuote(ctx, cotizador.CreateQuoteParams{
		LeadID: lead.ID, RFPID: rfpID, Currency: p.Currency,
		ValidUntil: &validUntil, Terms: p.Terms, Justification: p.Justification,
		Role: "cotizador", Items: p.Items,
	})
	if err != nil {
		return fmt.Errorf("create quote: %w", err)
	}

	// Update header con folio + datos propuesta
	if err := store.UpdateProposalHeader(ctx, q.ID, cotizador.UpdateProposalHeaderParams{
		Folio: p.Folio, ProposalType: p.ProposalType,
		ProductName: p.ProductName, ProductSubtitle: p.ProductSubtitle, Tags: p.Tags,
		PreparedForCompany:      p.LeadCompany,
		PreparedForArea:         p.PreparedForArea,
		PreparedForContactName:  p.PreparedForContactName,
		PreparedForContactEmail: p.PreparedForContactEmail,
		IssueDate:      &p.IssueDate,
		PreparedByName: "Juan Carlos Guajardo",
		PreparedByEmail: "jcguajardo@itechdev.com.mx",
		PreparedByRole: "CEO & Founder",
	}); err != nil {
		return fmt.Errorf("update header: %w", err)
	}

	// Insertar secciones
	for _, sec := range p.Sections {
		if err := store.UpsertSection(ctx, q.ID, sec.Key, sec.Title, sec.ContentMD, sec.SortOrder); err != nil {
			return fmt.Errorf("upsert section %s: %w", sec.Key, err)
		}
	}

	return nil
}
