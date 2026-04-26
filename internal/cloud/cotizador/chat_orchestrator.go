package cotizador

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ChatChannelRouter is the minimal subset of channels.Router needed by the
// orchestrator (kept here to avoid importing the channels package directly,
// which would create a tight coupling and make tests heavier).
type ChatChannelRouter interface {
	Query(ctx context.Context, req ChatRouterRequest) (*ChatRouterResponse, error)
}

// ChatRouterRequest mirrors channels.Request without importing channels.
type ChatRouterRequest struct {
	SystemPrompt string
	Messages     []ChatRouterMessage
	Sensitivity  string
	TimeoutSec   int
	Model        string
}

// ChatRouterMessage mirrors channels.Message.
type ChatRouterMessage struct {
	Role    string
	Content string
}

// ChatRouterResponse mirrors channels.Response.
type ChatRouterResponse struct {
	Content    string
	Model      string
	Channel    string
	DurationMs int
	TokensIn   int
	TokensOut  int
	Error      string
}

// ChatScrubber is the minimum surface of redactor.Service used by the orchestrator.
type ChatScrubber interface {
	// ScrubString returns (output, redactionsJSON). On error returns the
	// original text unmodified.
	ScrubString(ctx context.Context, text string) (string, string)
	// Expand reverses Scrub's tokens back into displayValue.
	Expand(ctx context.Context, text string) (string, error)
	// LogEgress records an audit row (for aria_llm_egress_log).
	LogEgress(ctx context.Context, requestID, observationID, provider, model, clientID, userUID, reason, payloadHash string, payloadSize int, scrubbed bool, redactionsJSON string) error
}

// ChannelCallLogger is the contract used to persist into aria_channel_calls.
type ChannelCallLogger interface {
	LogCall(ctx context.Context, rec ChannelCallRecord) (string, error)
}

// ChannelCallRecord mirrors channels.CallRecord.
type ChannelCallRecord struct {
	Channel              string
	Model                string
	PromptSize           int
	ResponseSize         int
	TokensIn             int
	TokensOut            int
	DurationMs           int
	Sensitivity          string
	InitiatedByUID       string
	RelatedChatSessionID string
	CostEstimateUSD      float64
	Error                string
}

// ChatOrchestrator wires the chat-quote pipeline:
//   - persist user message (with real data)
//   - scrub PII
//   - call the LLM channel router
//   - expand tokens back in the LLM response
//   - persist assistant message
//   - parse the response for `## SECCIÓN: <Title>` blocks → upsert quote_sections
type ChatOrchestrator struct {
	store    *Store
	router   ChatChannelRouter
	scrubber ChatScrubber
	logger   ChannelCallLogger
}

// NewChatOrchestrator returns a chat orchestrator. Any of router/scrubber/
// logger may be nil; the orchestrator degrades:
//   - nil router → returns ErrChatRouterUnavailable on send
//   - nil scrubber → no scrub, raw text goes to LLM (degraded mode)
//   - nil logger → telemetry rows skipped
func NewChatOrchestrator(s *Store, router ChatChannelRouter, scrubber ChatScrubber, logger ChannelCallLogger) *ChatOrchestrator {
	return &ChatOrchestrator{store: s, router: router, scrubber: scrubber, logger: logger}
}

// ErrChatRouterUnavailable is returned when no channel router is configured.
var ErrChatRouterUnavailable = errors.New("chat orchestrator: router not configured")

// SendUserMessageParams is the input tuple for SendUserMessage.
type SendUserMessageParams struct {
	SessionID    string
	UserUID      string
	UserMessage  string
	Sensitivity  string // public|internal|client|confidential — defaults to client
	Attachments  []ChatAttachment
	TemplatePrompt string // optional: override system prompt; otherwise built from session template
	TimeoutSec   int
	ModelHint    string
}

// ChatAttachment is the metadata stored alongside the user message.
type ChatAttachment struct {
	AttachmentID string `json:"attachment_id"`
	Filename     string `json:"filename"`
	MIME         string `json:"mime"`
	Size         int64  `json:"size"`
}

// ChatTurn is the result of one orchestrated round trip.
type ChatTurn struct {
	UserMessage      *ChatMessage
	AssistantMessage *ChatMessage
	ChannelCallID    string
	Channel          string
	Model            string
	DurationMs       int
	SectionsUpdated  []string
	FallbackNote     string
}

// SendUserMessage runs the full pipeline. Returns the user+assistant rows.
func (o *ChatOrchestrator) SendUserMessage(ctx context.Context, p SendUserMessageParams) (*ChatTurn, error) {
	if o == nil || o.store == nil {
		return nil, errors.New("chat orchestrator: not initialized")
	}
	if o.router == nil {
		return nil, ErrChatRouterUnavailable
	}
	sess, err := o.store.GetChatSession(ctx, p.SessionID)
	if err != nil {
		return nil, err
	}
	sens := strings.TrimSpace(strings.ToLower(p.Sensitivity))
	if sens == "" {
		sens = "client"
	}
	userMsg := strings.TrimSpace(p.UserMessage)
	if userMsg == "" {
		return nil, fmt.Errorf("%w: empty user message", ErrChatMessageInvalid)
	}

	// 1) Persist user message with real data.
	attsJSON, _ := json.Marshal(p.Attachments)
	if len(p.Attachments) == 0 {
		attsJSON = []byte("[]")
	}
	scrubbedUser := userMsg
	if o.scrubber != nil {
		scrubbedUser, _ = o.scrubber.ScrubString(ctx, userMsg)
	}
	userRow, err := o.store.AppendChatMessage(ctx, AppendChatMessageParams{
		SessionID:       sess.ID,
		Role:            "user",
		ContentMD:       userMsg,
		ScrubbedPayload: scrubbedUser,
		AttachmentsJSON: string(attsJSON),
	})
	if err != nil {
		return nil, fmt.Errorf("persist user msg: %w", err)
	}

	// 2) Build chat history for the LLM (use scrubbed payloads).
	prior, err := o.store.ListChatMessages(ctx, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}
	systemPrompt := strings.TrimSpace(p.TemplatePrompt)
	if systemPrompt == "" {
		systemPrompt = defaultChatSystemPrompt(sess.TemplateKey)
	}
	msgs := make([]ChatRouterMessage, 0, len(prior))
	for _, m := range prior {
		role := strings.ToLower(m.Role)
		var content string
		if m.ScrubbedPayload.Valid && strings.TrimSpace(m.ScrubbedPayload.String) != "" {
			content = m.ScrubbedPayload.String
		} else {
			content = m.ContentMD
		}
		msgs = append(msgs, ChatRouterMessage{Role: role, Content: content})
	}

	// 3) Call the router.
	start := time.Now()
	resp, err := o.router.Query(ctx, ChatRouterRequest{
		SystemPrompt: systemPrompt,
		Messages:     msgs,
		Sensitivity:  sens,
		TimeoutSec:   p.TimeoutSec,
		Model:        p.ModelHint,
	})
	elapsed := time.Since(start)
	if err != nil {
		// Persist a system message documenting the failure so the chat UI
		// shows it. The user message stays — JC can retry.
		_, _ = o.store.AppendChatMessage(ctx, AppendChatMessageParams{
			SessionID: sess.ID,
			Role:      "system",
			ContentMD: "⚠️ La IA no pudo responder en este intento. Detalle: " + err.Error(),
		})
		return nil, fmt.Errorf("router query: %w", err)
	}

	// 4) Log channel call telemetry.
	callID := ""
	if o.logger != nil {
		id, lerr := o.logger.LogCall(ctx, ChannelCallRecord{
			Channel:              resp.Channel,
			Model:                resp.Model,
			PromptSize:           sumPromptSize(systemPrompt, msgs),
			ResponseSize:         len(resp.Content),
			TokensIn:             resp.TokensIn,
			TokensOut:            resp.TokensOut,
			DurationMs:           int(elapsed / time.Millisecond),
			Sensitivity:          sens,
			InitiatedByUID:       strings.TrimSpace(p.UserUID),
			RelatedChatSessionID: sess.ID,
			CostEstimateUSD:      0,
			Error:                resp.Error,
		})
		if lerr == nil {
			callID = id
		}
	}

	// 5) Audit egress (re-uses redactor.LogEgress so the existing dashboard
	// /audit/egress page surfaces these calls alongside legacy ones).
	if o.scrubber != nil {
		hash := sha256Hex(strings.TrimSpace(scrubbedUser))
		_ = o.scrubber.LogEgress(ctx, callID, "", providerForChannel(resp.Channel), resp.Model, "", strings.TrimSpace(p.UserUID), "quote_chat_send", hash, len(scrubbedUser), o.scrubber != nil, "[]")
	}

	// 6) Expand tokens in the assistant content for display.
	displayContent := resp.Content
	if o.scrubber != nil {
		expanded, eerr := o.scrubber.Expand(ctx, resp.Content)
		if eerr == nil {
			displayContent = expanded
		}
	}

	// 7) Parse `## SECCIÓN: ...` blocks for live quote preview.
	sections := ParseSectionBlocks(displayContent)
	parsedJSON, _ := json.Marshal(map[string]any{"sections": sections})
	if len(sections) == 0 {
		parsedJSON = []byte("{}")
	}

	// Persist assistant message — content_md = real values, scrubbed_payload = LLM raw.
	assistantRow, err := o.store.AppendChatMessage(ctx, AppendChatMessageParams{
		SessionID:          sess.ID,
		Role:               "assistant",
		ContentMD:          displayContent,
		ScrubbedPayload:    resp.Content,
		ChannelCallID:      callID,
		ParsedQuoteUpdates: string(parsedJSON),
	})
	if err != nil {
		return nil, fmt.Errorf("persist assistant msg: %w", err)
	}

	// 8) If the session is bound to a quote_id, upsert sections live.
	updatedKeys := []string{}
	if sess.QuoteID.Valid && len(sections) > 0 {
		for i, sec := range sections {
			key := slugifySectionKey(sec.Title)
			if err := o.store.UpsertSection(ctx, sess.QuoteID.String, key, sec.Title, sec.Content, i+1); err == nil {
				updatedKeys = append(updatedKeys, key)
			}
		}
	}

	turn := &ChatTurn{
		UserMessage:      userRow,
		AssistantMessage: assistantRow,
		ChannelCallID:    callID,
		Channel:          resp.Channel,
		Model:            resp.Model,
		DurationMs:       int(elapsed / time.Millisecond),
		SectionsUpdated:  updatedKeys,
		FallbackNote:     resp.Error,
	}
	return turn, nil
}

// SummarizeText is a thin wrapper that satisfies the LLMSummarizer contract
// expected by RFPParser: it sends a single user-message via the router with
// the supplied system-prompt and returns the response content.
func (o *ChatOrchestrator) Summarize(ctx context.Context, text, systemPrompt string) (string, error) {
	if o == nil || o.router == nil {
		return "", ErrChatRouterUnavailable
	}
	if strings.TrimSpace(text) == "" {
		return "", errors.New("summarize: empty input")
	}
	sens := "client"
	resp, err := o.router.Query(ctx, ChatRouterRequest{
		SystemPrompt: systemPrompt,
		Messages: []ChatRouterMessage{
			{Role: "user", Content: text},
		},
		Sensitivity: sens,
		TimeoutSec:  120,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}

// ─── Section parsing helpers ─────────────────────────────────────────────────

// ParsedSection is one block extracted from the LLM response.
type ParsedSection struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// sectionStartPattern matches lines like:
//
//	## SECCIÓN: Resumen ejecutivo
//	## Sección: Alcance
//	## SECTION: Pricing
var sectionStartPattern = regexp.MustCompile(`(?im)^##\s+(secci[oó]n|section)\s*:\s*(.+?)\s*$`)

// ParseSectionBlocks scans markdown for `## SECCIÓN: <Title>` blocks and
// returns one ParsedSection per block. Content runs until the next section
// header or end-of-string.
func ParseSectionBlocks(md string) []ParsedSection {
	if strings.TrimSpace(md) == "" {
		return nil
	}
	idxs := sectionStartPattern.FindAllStringSubmatchIndex(md, -1)
	if len(idxs) == 0 {
		return nil
	}
	out := make([]ParsedSection, 0, len(idxs))
	for i, m := range idxs {
		// m[0] = start of header, m[1] = end of header line.
		// Subgroup 1 (kind=secci[oó]n|section) at m[2]:m[3].
		// Subgroup 2 (title) at m[4]:m[5].
		title := strings.TrimSpace(md[m[4]:m[5]])
		bodyStart := m[1]
		bodyEnd := len(md)
		if i+1 < len(idxs) {
			bodyEnd = idxs[i+1][0]
		}
		content := strings.TrimSpace(md[bodyStart:bodyEnd])
		if title == "" {
			continue
		}
		out = append(out, ParsedSection{Title: title, Content: content})
	}
	return out
}

// slugifySectionKey converts a free-form section title into a stable upsert key.
func slugifySectionKey(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	var b strings.Builder
	prevDash := false
	for _, r := range t {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			prevDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == 'á':
			b.WriteRune('a')
			prevDash = false
		case r == 'é':
			b.WriteRune('e')
			prevDash = false
		case r == 'í':
			b.WriteRune('i')
			prevDash = false
		case r == 'ó':
			b.WriteRune('o')
			prevDash = false
		case r == 'ú', r == 'ü':
			b.WriteRune('u')
			prevDash = false
		case r == 'ñ':
			b.WriteRune('n')
			prevDash = false
		default:
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "section"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// defaultChatSystemPrompt produces the system prompt used when the caller did
// not supply a TemplatePrompt explicitly.
func defaultChatSystemPrompt(templateKey string) string {
	tk := strings.TrimSpace(templateKey)
	if tk == "" {
		tk = "itechdev_implementation_v1"
	}
	return `Sos un cotizador iTechDev. Estás ayudando a JC a construir una propuesta para un cliente, sección por sección.

REGLAS:
1. Cuando agregues una sección a la cotización, escribila como bloque markdown:
   ## SECCIÓN: <Título corto>
   <contenido markdown>

2. Los datos del cliente vienen ofuscados con tokens [CLIENT-XXXX] [EMAIL-XXXX] [RFC-XXXX] [QUOTE-XXXX].
   Usá los tokens tal como vienen — son referencias seguras. NO inventes valores reales.

3. Las secciones canónicas iTechDev: resumen-ejecutivo, alcance, entregables, plan-de-trabajo,
   inversion, supuestos, exclusiones, terminos, equipo. Adaptá según el RFP.

4. Después de cada respuesta, si querés actualizar la quote, indicalo con el bloque
   "## SECCIÓN:" — el sistema upserta automáticamente.

Template activo: ` + tk
}

// sumPromptSize sums the size of the system prompt + each message's content.
func sumPromptSize(system string, msgs []ChatRouterMessage) int {
	total := len(system)
	for _, m := range msgs {
		total += len(m.Content)
	}
	return total
}

// providerForChannel maps the channel name to a redactor egress provider tag
// so legacy audit dashboards group calls correctly.
func providerForChannel(channel string) string {
	switch channel {
	case "claude-max-vps":
		return "anthropic"
	case "gemma-local":
		return "ollama-local"
	default:
		if strings.TrimSpace(channel) == "" {
			return "unknown"
		}
		return channel
	}
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ─── DB helpers (light wrapper used by adapters that need raw DB access) ────

// DBHandle returns the underlying *sql.DB so callers can set up additional
// queries without extending the Store interface.
func (s *Store) DBHandle() *sql.DB { return s.db }
