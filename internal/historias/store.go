package historias

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	manifestName     = "MANIFEST.yaml"
	artifactPattern  = `^(\d+)-([a-z0-9\-]+)\.md$`
)

var (
	artifactRe = regexp.MustCompile(artifactPattern)
	slugRe     = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

	ErrSlugInvalid     = errors.New("historias: slug must match ^[a-z0-9]+(-[a-z0-9]+)*$")
	ErrPositionTaken   = errors.New("historias: position already has an artifact (use overwrite=true)")
	ErrManifestMissing = errors.New("historias: MANIFEST.yaml missing")
	ErrArtifactMissing = errors.New("historias: artifact not found at position")
)

// SaveArtifactArgs is the input to SaveArtifact.
type SaveArtifactArgs struct {
	Root       string   // /Historias root (caller-supplied; usually <repo>/Historias)
	Slug       string   // historia slug
	Position   int      // 0-based chain position
	Filename   string   // "<position>-<short-slug>.md" (must match position prefix)
	Skill      string   // "skill-name@1.0.0"
	AgentModel string   // e.g. "claude-opus-4-7"
	Inputs     []string // filenames this artifact consumed (must already exist)
	Content    string   // markdown body
	DurationMs int64
	CreatedBy  string // initial CreatedBy if the chain is new
	Overwrite  bool   // if true, replace existing artifact at Position
}

// SaveArtifact writes the artifact to disk and updates MANIFEST.yaml.
// Idempotent only if Overwrite=true. Caller chooses; default is
// "first writer wins" so out-of-order or re-tries are safe.
func SaveArtifact(args SaveArtifactArgs) (ChainEntry, error) {
	if !slugRe.MatchString(args.Slug) {
		return ChainEntry{}, ErrSlugInvalid
	}
	if !artifactRe.MatchString(args.Filename) {
		return ChainEntry{}, fmt.Errorf("historias: filename %q must match %s", args.Filename, artifactPattern)
	}
	matches := artifactRe.FindStringSubmatch(args.Filename)
	pos, _ := strconv.Atoi(matches[1])
	if pos != args.Position {
		return ChainEntry{}, fmt.Errorf("historias: filename position %d != Position arg %d", pos, args.Position)
	}

	dir := filepath.Join(args.Root, args.Slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ChainEntry{}, fmt.Errorf("mkdir: %w", err)
	}

	artifactPath := filepath.Join(dir, args.Filename)
	if !args.Overwrite {
		if _, err := os.Stat(artifactPath); err == nil {
			return ChainEntry{}, ErrPositionTaken
		}
	}

	// Verify all inputs exist
	for _, in := range args.Inputs {
		if _, err := os.Stat(filepath.Join(dir, in)); err != nil {
			return ChainEntry{}, fmt.Errorf("input %q not found in %s", in, dir)
		}
	}

	// Write artifact
	if err := os.WriteFile(artifactPath, []byte(args.Content), 0o644); err != nil {
		return ChainEntry{}, fmt.Errorf("write artifact: %w", err)
	}

	// Compute hash
	sum := sha256.Sum256([]byte(args.Content))
	entry := ChainEntry{
		Position:      args.Position,
		Artifact:      args.Filename,
		Skill:         args.Skill,
		AgentModel:    args.AgentModel,
		ContentSHA256: hex.EncodeToString(sum[:]),
		Inputs:        args.Inputs,
		DurationMs:    args.DurationMs,
		CreatedAt:     nowUTC(),
	}

	// Load or initialize manifest
	manifestPath := filepath.Join(dir, manifestName)
	var m ChainManifest
	if data, err := os.ReadFile(manifestPath); err == nil {
		if uErr := yaml.Unmarshal(data, &m); uErr != nil {
			return entry, fmt.Errorf("parse existing manifest: %w", uErr)
		}
	} else {
		m = ChainManifest{
			Slug:      args.Slug,
			CreatedAt: nowUTC(),
			CreatedBy: args.CreatedBy,
			Status:    StatusInProgress,
		}
	}

	// Upsert entry by Position
	replaced := false
	for i, e := range m.Chain {
		if e.Position == args.Position {
			m.Chain[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		m.Chain = append(m.Chain, entry)
	}
	sort.Slice(m.Chain, func(i, j int) bool { return m.Chain[i].Position < m.Chain[j].Position })

	if err := writeManifest(manifestPath, m); err != nil {
		return entry, err
	}
	return entry, nil
}

// GetArtifact returns the content of an artifact at slug+position.
func GetArtifact(root, slug string, position int) (ChainEntry, string, error) {
	dir := filepath.Join(root, slug)
	m, err := readManifest(filepath.Join(dir, manifestName))
	if err != nil {
		return ChainEntry{}, "", err
	}
	var entry ChainEntry
	for _, e := range m.Chain {
		if e.Position == position {
			entry = e
		}
	}
	if entry.Artifact == "" {
		return ChainEntry{}, "", ErrArtifactMissing
	}
	body, err := os.ReadFile(filepath.Join(dir, entry.Artifact))
	if err != nil {
		return entry, "", err
	}
	return entry, string(body), nil
}

// ListChain returns the manifest for a slug. Useful for the orchestrator
// to know what artifacts already exist before invoking the next agent.
func ListChain(root, slug string) (ChainManifest, error) {
	dir := filepath.Join(root, slug)
	return readManifest(filepath.Join(dir, manifestName))
}

// ListSlugs walks root and returns all directories that contain a
// MANIFEST.yaml (i.e., started chains).
func ListSlugs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read root: %w", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, name, manifestName)); err == nil {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// CompleteChain marks a chain as completed and records its final
// artifact filename.
func CompleteChain(root, slug, finalArtifact string) error {
	dir := filepath.Join(root, slug)
	manifestPath := filepath.Join(dir, manifestName)
	m, err := readManifest(manifestPath)
	if err != nil {
		return err
	}
	m.Status = StatusCompleted
	m.FinalArtifact = finalArtifact
	return writeManifest(manifestPath, m)
}

func readManifest(path string) (ChainManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ChainManifest{}, ErrManifestMissing
		}
		return ChainManifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var m ChainManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return ChainManifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	return m, nil
}

func writeManifest(path string, m ChainManifest) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	header := []byte("# /Historias chain manifest. Updated by aria_artifact_save.\n")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(header, data...), 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
