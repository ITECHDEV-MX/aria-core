package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/historias"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerAriaHistoriasSync wires aria_artifact_sync_to_pages.
//
// Reads /Historias/<slug>/ and POSTs each artifact to the configured
// aria_pages cloud endpoint. Env-gated:
//
//	ARIA_CLOUD_URL    e.g. https://ariacore.itechdev.com.mx
//	ARIA_CLOUD_TOKEN  JWT from aria-core login (or service token)
//
// When either env var is unset, the tool returns a no-op success
// indicating sync was skipped — never blocks the artifact pipeline.
func registerAriaHistoriasSync(srv *server.MCPServer) {
	srv.AddTool(mcp.NewTool("aria_artifact_sync_to_pages",
		mcp.WithTitleAnnotation("Sync Historia chain to aria_pages cloud"),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDescription("Mirror the artifacts in /Historias/<slug>/ to the cloud aria_pages tree. Each artifact becomes a child page under the historia parent. Requires ARIA_CLOUD_URL + ARIA_CLOUD_TOKEN env vars; skips silently if unset. F2.1 follow-up to F2."),
		mcp.WithString("slug", mcp.Required()),
		mcp.WithString("parent_page_id", mcp.Description("Optional aria_pages parent. If empty, creates a new top-level page named after the slug.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		slug := strings.TrimSpace(asString(req.GetArguments(), "slug"))
		if slug == "" {
			return mcp.NewToolResultError("slug is required"), nil
		}

		cloudURL := strings.TrimRight(strings.TrimSpace(os.Getenv("ARIA_CLOUD_URL")), "/")
		cloudToken := strings.TrimSpace(os.Getenv("ARIA_CLOUD_TOKEN"))
		if cloudURL == "" || cloudToken == "" {
			return jsonResult(map[string]any{
				"slug":    slug,
				"synced":  0,
				"skipped": true,
				"reason":  "ARIA_CLOUD_URL or ARIA_CLOUD_TOKEN unset",
			})
		}

		m, err := historias.ListChain(historiasRoot(), slug)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("read chain: %v", err)), nil
		}

		client := &http.Client{Timeout: 10 * time.Second}

		// Create parent page if not supplied.
		parentID := strings.TrimSpace(asString(req.GetArguments(), "parent_page_id"))
		if parentID == "" {
			parentID, err = createPage(ctx, client, cloudURL, cloudToken, "", "Historia: "+slug, fmt.Sprintf("Chain manifest for /Historias/%s/.", slug))
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("create parent page: %v", err)), nil
			}
		}

		synced := 0
		var pageIDs []string
		for _, e := range m.Chain {
			_, body, gerr := historias.GetArtifact(historiasRoot(), slug, e.Position)
			if gerr != nil {
				continue
			}
			title := fmt.Sprintf("%02d — %s", e.Position, e.Artifact)
			pid, perr := createPage(ctx, client, cloudURL, cloudToken, parentID, title, body)
			if perr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("sync position %d: %v", e.Position, perr)), nil
			}
			pageIDs = append(pageIDs, pid)
			synced++
		}

		return jsonResult(map[string]any{
			"slug":           slug,
			"parent_page_id": parentID,
			"page_ids":       pageIDs,
			"synced":         synced,
			"skipped":        false,
		})
	})
}

// createPage POSTs a single page to the cloud. Returns the new page id.
func createPage(ctx context.Context, client *http.Client, baseURL, token, parentID, title, content string) (string, error) {
	payload := map[string]any{"title": title, "content": content}
	if parentID != "" {
		payload["parent_id"] = parentID
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/pages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out struct{ ID string `json:"id"` }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.ID, nil
}
