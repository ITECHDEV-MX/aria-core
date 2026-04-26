package attachments

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ThumbnailMaxDim is the bounding box (px) for generated previews. We keep the
// aspect ratio and never upscale.
const ThumbnailMaxDim = 400

// ThumbnailJPEGQuality is the JPEG output quality (1..100).
const ThumbnailJPEGQuality = 80

// GenerateThumbnail reads an original image or PDF from src and writes a JPEG
// preview to dst (both absolute paths). Returns nil on success or a descriptive
// error. PDF rendering uses pdftoppm if available, otherwise mutool, otherwise
// returns ErrNoPDFRenderer.
//
// This function is meant to run in a goroutine — it does not modify any DB row.
// Callers update aria_page_attachments.thumbnail_path after a successful return.
func GenerateThumbnail(ctx context.Context, mime, src, dst string) error {
	switch {
	case mime == "application/pdf":
		return renderPDFThumbnail(ctx, src, dst)
	case len(mime) > 6 && mime[:6] == "image/":
		return renderImageThumbnail(src, dst)
	default:
		return fmt.Errorf("attachments: thumbnail: unsupported mime %q", mime)
	}
}

func renderImageThumbnail(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open image: %w", err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("decode image: %w", err)
	}
	thumb := scaleToFit(img, ThumbnailMaxDim, ThumbnailMaxDim)
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("open thumb dst: %w", err)
	}
	defer out.Close()
	if err := jpeg.Encode(out, thumb, &jpeg.Options{Quality: ThumbnailJPEGQuality}); err != nil {
		return fmt.Errorf("jpeg encode: %w", err)
	}
	return os.Chmod(dst, 0o640)
}

// ErrNoPDFRenderer indicates neither pdftoppm nor mutool are installed; the caller
// can fall back to a generic SVG icon.
var ErrNoPDFRenderer = fmt.Errorf("attachments: no PDF renderer available (install poppler-utils or mupdf-tools)")

func renderPDFThumbnail(ctx context.Context, src, dst string) error {
	tmpDir, err := os.MkdirTemp("", "aria-pdf-thumb-")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if path, _ := exec.LookPath("pdftoppm"); path != "" {
		// pdftoppm src out -jpeg -r 100 -f 1 -l 1
		out := filepath.Join(tmpDir, "thumb")
		cmdCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cmdCtx, path, "-jpeg", "-r", "100", "-f", "1", "-l", "1", src, out)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("pdftoppm: %w", err)
		}
		// pdftoppm appends "-1.jpg" to the output prefix.
		generated := out + "-1.jpg"
		if _, err := os.Stat(generated); err != nil {
			// Some pdftoppm versions use "-01.jpg".
			alt := out + "-01.jpg"
			if _, e2 := os.Stat(alt); e2 == nil {
				generated = alt
			} else {
				return fmt.Errorf("pdftoppm: expected output not found")
			}
		}
		return resampleAndSave(generated, dst)
	}
	if path, _ := exec.LookPath("mutool"); path != "" {
		out := filepath.Join(tmpDir, "thumb.png")
		cmdCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cmdCtx, path, "draw", "-o", out, "-r", "100", src, "1")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("mutool: %w", err)
		}
		return resampleAndSave(out, dst)
	}
	return ErrNoPDFRenderer
}

// resampleAndSave decodes src (any image format), scales to thumbnail box, and
// writes a JPEG to dst. Used to normalize PDF renderer output (which is often
// 100dpi+ → far larger than 400px).
func resampleAndSave(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("decode rendered: %w", err)
	}
	thumb := scaleToFit(img, ThumbnailMaxDim, ThumbnailMaxDim)
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if err := jpeg.Encode(out, thumb, &jpeg.Options{Quality: ThumbnailJPEGQuality}); err != nil {
		return err
	}
	return os.Chmod(dst, 0o640)
}

// scaleToFit returns an image scaled into the maxW×maxH bounding box, preserving
// aspect ratio. If the image already fits, it is returned unchanged. Uses a
// simple bilinear downsample implemented in pure stdlib so the build stays
// dependency-free.
func scaleToFit(src image.Image, maxW, maxH int) image.Image {
	b := src.Bounds()
	w := b.Dx()
	h := b.Dy()
	if w <= maxW && h <= maxH {
		return src
	}
	scale := float64(maxW) / float64(w)
	if alt := float64(maxH) / float64(h); alt < scale {
		scale = alt
	}
	newW := int(float64(w) * scale)
	newH := int(float64(h) * scale)
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	xRatio := float64(w) / float64(newW)
	yRatio := float64(h) / float64(newH)
	for y := 0; y < newH; y++ {
		srcY := b.Min.Y + int(float64(y)*yRatio)
		for x := 0; x < newW; x++ {
			srcX := b.Min.X + int(float64(x)*xRatio)
			r, g, bl, a := src.At(srcX, srcY).RGBA()
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(bl >> 8), A: uint8(a >> 8),
			})
		}
	}
	return dst
}
