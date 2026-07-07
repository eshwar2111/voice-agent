package vision

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// OCRResult represents a piece of text found on the screen.
type OCRResult struct {
	Text string
	X    int
	Y    int
	W    int
	H    int
}

// RunOCR captures text from a screenshot using Tesseract.
func RunOCR(ctx context.Context, imgBytes []byte) (string, error) {
	tmpDir, err := os.MkdirTemp("", "ocr_scan")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	imgPath := filepath.Join(tmpDir, "screen.png")
	if err := os.WriteFile(imgPath, imgBytes, 0644); err != nil {
		return "", err
	}

	outBase := filepath.Join(tmpDir, "out")
	
	// Tesseract 5+ command: tesseract input outputbase [options]
	cmd := exec.CommandContext(ctx, "tesseract", imgPath, outBase)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tesseract failed: %w", err)
	}

	outPath := outBase + ".txt"
	txtData, err := os.ReadFile(outPath)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(txtData)), nil
}
