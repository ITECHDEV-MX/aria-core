package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/ITECHDEV-MX/aria-core/internal/store"
)

// sessionFile representa el JSON cacheado en ~/.aria-core/session.json.
type sessionFile struct {
	Server    string    `json:"server"`
	Token     string    `json:"token"`
	UID       string    `json:"uid"`
	Email     string    `json:"email"`
	Roles     []string  `json:"roles"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func sessionFilePath(cfg store.Config) string {
	return filepath.Join(cfg.DataDir, "session.json")
}

// loadSession lee el session.json. Retorna nil sin error si no existe.
func loadSession(cfg store.Config) (*sessionFile, error) {
	b, err := os.ReadFile(sessionFilePath(cfg))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var s sessionFile
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveSession(cfg store.Config, s *sessionFile) error {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sessionFilePath(cfg), b, 0o600)
}

// cmdLogin: aria-core login [--email X] [--server URL]
// Pide password interactivamente (oculto). Hace POST /v1/auth/login al cloud,
// guarda el JWT en ~/.aria-core/session.json (chmod 600).
func cmdLogin(cfg store.Config) {
	server := strings.TrimSpace(os.Getenv("ARIA_CORE_CLOUD_SERVER"))
	if cc, _ := loadCloudConfig(cfg); cc != nil && cc.ServerURL != "" {
		if server == "" {
			server = cc.ServerURL
		}
	}
	email := ""
	passwordArg := ""
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--email":
			if i+1 < len(os.Args) {
				email = strings.TrimSpace(os.Args[i+1])
				i++
			}
		case "--server":
			if i+1 < len(os.Args) {
				server = strings.TrimSpace(os.Args[i+1])
				i++
			}
		case "--password":
			if i+1 < len(os.Args) {
				passwordArg = os.Args[i+1]
				i++
			}
		}
	}
	if server == "" {
		fmt.Fprintln(os.Stderr, "error: cloud server URL not configured. Use 'aria-core cloud config --server <url>' o pasá --server.")
		exitFunc(1)
		return
	}
	reader := bufio.NewReader(os.Stdin)
	if email == "" {
		fmt.Print("Email: ")
		line, _ := reader.ReadString('\n')
		email = strings.TrimSpace(line)
	}
	if email == "" {
		fmt.Fprintln(os.Stderr, "email is required")
		exitFunc(1)
		return
	}
	password := passwordArg
	if password == "" {
		fmt.Print("Password: ")
		if term.IsTerminal(int(syscall.Stdin)) {
			pwBytes, err := term.ReadPassword(int(syscall.Stdin))
			fmt.Println()
			if err != nil {
				fmt.Fprintf(os.Stderr, "read password: %v\n", err)
				exitFunc(1)
				return
			}
			password = string(pwBytes)
		} else {
			line, _ := reader.ReadString('\n')
			password = strings.TrimRight(line, "\r\n")
		}
	}
	if password == "" {
		fmt.Fprintln(os.Stderr, "password is required")
		exitFunc(1)
		return
	}

	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(server, "/")+"/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "build request: %v\n", err)
		exitFunc(1)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	httpClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "POST %s: %v\n", req.URL, err)
		exitFunc(1)
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "login failed (HTTP %d): %s\n", resp.StatusCode, strings.TrimSpace(string(respBody)))
		exitFunc(1)
		return
	}
	var loginResp struct {
		Token     string   `json:"token"`
		ExpiresIn int      `json:"expires_in_seconds"`
		UID       string   `json:"uid"`
		Email     string   `json:"email"`
		Roles     []string `json:"roles"`
	}
	if err := json.Unmarshal(respBody, &loginResp); err != nil {
		fmt.Fprintf(os.Stderr, "parse response: %v\n", err)
		exitFunc(1)
		return
	}
	now := time.Now().UTC()
	sess := &sessionFile{
		Server:    server,
		Token:     loginResp.Token,
		UID:       loginResp.UID,
		Email:     loginResp.Email,
		Roles:     loginResp.Roles,
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Duration(loginResp.ExpiresIn) * time.Second),
	}
	if err := saveSession(cfg, sess); err != nil {
		fmt.Fprintf(os.Stderr, "save session: %v\n", err)
		exitFunc(1)
		return
	}
	fmt.Printf("✓ logged in as %s\n  uid:     %s\n  roles:   %s\n  server:  %s\n  expires: %s\n",
		sess.Email, sess.UID, strings.Join(sess.Roles, ","), sess.Server, sess.ExpiresAt.Local().Format("02 Jan 2006 15:04"))
}

// cmdLogout: borra ~/.aria-core/session.json.
func cmdLogout(cfg store.Config) {
	path := sessionFilePath(cfg)
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("No active session.")
			return
		}
		fmt.Fprintf(os.Stderr, "remove session: %v\n", err)
		exitFunc(1)
		return
	}
	fmt.Println("✓ logged out (session removed)")
}

// cmdWhoami: imprime info del session.json actual.
func cmdWhoami(cfg store.Config) {
	sess, err := loadSession(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load session: %v\n", err)
		exitFunc(1)
		return
	}
	if sess == nil {
		fmt.Println("Not logged in. Run 'aria-core login' first.")
		return
	}
	now := time.Now().UTC()
	expired := now.After(sess.ExpiresAt)
	fmt.Printf("email:      %s\nuid:        %s\nroles:      %s\nserver:     %s\nissued:     %s\nexpires:    %s",
		sess.Email, sess.UID, strings.Join(sess.Roles, ","), sess.Server,
		sess.IssuedAt.Local().Format("02 Jan 2006 15:04"),
		sess.ExpiresAt.Local().Format("02 Jan 2006 15:04"),
	)
	if expired {
		fmt.Println("  (EXPIRED — re-run 'aria-core login')")
	} else {
		fmt.Println()
	}
}
