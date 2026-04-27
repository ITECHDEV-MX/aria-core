package knowledgebase

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PandocBinaryEnv permite override del binary path (testing / docker).
const PandocBinaryEnv = "ARIA_CORE_PANDOC_BIN"

// pandocRunner abstrae la invocación de pandoc para que los tests
// puedan inyectar un fake.
type pandocRunner interface {
	// Run convierte el input (HTML o markdown) al output binario (DOCX)
	// usando reference doc opcional. Retorna stdout (los bytes DOCX).
	Run(ctx context.Context, fromFormat, toFormat string, input []byte, referenceDoc string) ([]byte, error)
	// Available retorna nil si pandoc se puede ejecutar.
	Available(ctx context.Context) error
}

// realPandoc invoca el binary real (env override > $PATH).
type realPandoc struct{}

func (realPandoc) binary() string {
	if v := strings.TrimSpace(os.Getenv(PandocBinaryEnv)); v != "" {
		return v
	}
	return "pandoc"
}

func (r realPandoc) Available(ctx context.Context) error {
	bin := r.binary()
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("%w: looked up %q (set %s to override): %v",
			ErrPandocNotAvailable, bin, PandocBinaryEnv, err)
	}
	cmd := exec.CommandContext(ctx, bin, "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: pandoc --version exit error: %v", ErrPandocNotAvailable, err)
	}
	return nil
}

func (r realPandoc) Run(ctx context.Context, fromFormat, toFormat string, input []byte, referenceDoc string) ([]byte, error) {
	bin := r.binary()
	args := []string{"-f", fromFormat, "-t", toFormat, "-o", "-"}
	if strings.TrimSpace(referenceDoc) != "" {
		args = append(args, "--reference-doc="+referenceDoc)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = bytes.NewReader(input)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("knowledgebase: pandoc convert (%s → %s) failed: %v: %s",
			fromFormat, toFormat, err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

// DOCXConverter encapsula la conversión Markdown/HTML → DOCX con un
// reference template iTechDev.
type DOCXConverter struct {
	pandoc            pandocRunner
	referenceDocPath  string
	autoBootstrapPath string // si referenceDoc no existe, generar default acá la primera vez
}

// NewDOCXConverter crea un convertidor con el reference template ubicado
// en referenceDocPath. Si el archivo no existe pero autoBootstrapPath está
// seteado, la primera invocación intentará generar uno default usando
// pandoc.
func NewDOCXConverter(referenceDocPath, autoBootstrapPath string) *DOCXConverter {
	return &DOCXConverter{
		pandoc:            realPandoc{},
		referenceDocPath:  strings.TrimSpace(referenceDocPath),
		autoBootstrapPath: strings.TrimSpace(autoBootstrapPath),
	}
}

// VerifyAvailable retorna error si pandoc no está disponible. Pensado para
// ser invocado al startup del cloudserver (logging warning solamente).
func (c *DOCXConverter) VerifyAvailable(ctx context.Context) error {
	if c == nil || c.pandoc == nil {
		return ErrPandocNotAvailable
	}
	return c.pandoc.Available(ctx)
}

// ConvertMarkdown ejecuta pandoc -f markdown -t docx con el reference doc.
func (c *DOCXConverter) ConvertMarkdown(ctx context.Context, markdown []byte) ([]byte, error) {
	if c == nil || c.pandoc == nil {
		return nil, ErrPandocNotAvailable
	}
	if err := c.pandoc.Available(ctx); err != nil {
		return nil, err
	}
	refDoc, err := c.resolveReferenceDoc(ctx)
	if err != nil {
		return nil, err
	}
	return c.pandoc.Run(ctx, "markdown", "docx", markdown, refDoc)
}

// ConvertHTML ejecuta pandoc -f html -t docx con el reference doc.
func (c *DOCXConverter) ConvertHTML(ctx context.Context, html []byte) ([]byte, error) {
	if c == nil || c.pandoc == nil {
		return nil, ErrPandocNotAvailable
	}
	if err := c.pandoc.Available(ctx); err != nil {
		return nil, err
	}
	refDoc, err := c.resolveReferenceDoc(ctx)
	if err != nil {
		return nil, err
	}
	return c.pandoc.Run(ctx, "html", "docx", html, refDoc)
}

// resolveReferenceDoc retorna el path al reference doc, generando uno
// default con pandoc si autoBootstrapPath está seteado y el archivo no
// existe. Si el reference doc no es accesible y no hay bootstrap,
// retorna "" — pandoc usará su default.
func (c *DOCXConverter) resolveReferenceDoc(ctx context.Context) (string, error) {
	if c.referenceDocPath == "" && c.autoBootstrapPath == "" {
		return "", nil
	}
	candidate := c.referenceDocPath
	if candidate == "" {
		candidate = c.autoBootstrapPath
	}
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	// Si no existe Y tenemos un bootstrap path, generamos default.
	if c.autoBootstrapPath == "" {
		// No hay donde escribir el bootstrap, devolver "" → pandoc usa default.
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(c.autoBootstrapPath), 0o755); err != nil {
		return "", fmt.Errorf("knowledgebase: create reference doc dir: %w", err)
	}
	// Generar reference doc default usando pandoc.
	docxBytes, err := c.pandoc.Run(ctx, "markdown", "docx",
		[]byte("# iTechDev — propuesta\n\nReference template auto-generado.\n"),
		"",
	)
	if err != nil {
		return "", fmt.Errorf("knowledgebase: bootstrap reference doc: %w", err)
	}
	if err := os.WriteFile(c.autoBootstrapPath, docxBytes, 0o644); err != nil {
		return "", fmt.Errorf("knowledgebase: write reference doc: %w", err)
	}
	return c.autoBootstrapPath, nil
}

// withRunner permite a los tests inyectar un pandocRunner fake.
func (c *DOCXConverter) withRunner(runner pandocRunner) *DOCXConverter {
	c.pandoc = runner
	return c
}
