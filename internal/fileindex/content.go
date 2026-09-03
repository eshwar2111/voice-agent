package fileindex

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
)

// maxExtractBytes caps the amount of text pulled out of any one file so a
// huge log or generated source blob can't blow up the embed/FTS pipeline.
const maxExtractBytes = 1 << 20 // ~1 MB

// plainTextExt is the whitelist of extensions read directly as UTF-8 text
// (code files included — they're just text with syntax).
var plainTextExt = map[string]bool{
	"txt": true, "md": true, "markdown": true, "json": true, "csv": true,
	"log": true, "yaml": true, "yml": true, "toml": true, "ini": true,
	"go": true, "py": true, "js": true, "ts": true, "jsx": true, "tsx": true,
	"java": true, "c": true, "h": true, "cpp": true, "hpp": true, "cs": true,
	"rb": true, "rs": true, "php": true, "sh": true, "ps1": true, "sql": true,
	"html": true, "htm": true, "css": true, "xml": true,
}

// isEmbeddable reports whether extractText knows how to pull text out of a
// file with this extension (lowercase, no leading dot).
func isEmbeddable(ext string) bool {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	if plainTextExt[ext] {
		return true
	}
	switch ext {
	case "pdf", "docx", "pptx", "xlsx":
		return true
	}
	return false
}

// extractText returns the textual content of path (capped to
// maxExtractBytes) and true, or ("", false) if the file's extension isn't
// on the embeddable whitelist or extraction failed.
func extractText(path string) (string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	if !isEmbeddable(ext) {
		return "", false
	}

	if plainTextExt[ext] {
		return extractPlainText(path)
	}

	switch ext {
	case "pdf":
		return extractPDF(path)
	case "docx":
		return extractOfficeZip(path, "word/document.xml")
	case "pptx":
		return extractOfficeZipPrefix(path, "ppt/slides/")
	case "xlsx":
		return extractOfficeZip(path, "xl/sharedStrings.xml")
	}
	return "", false
}

func capText(s string) string {
	if len(s) > maxExtractBytes {
		return s[:maxExtractBytes]
	}
	return s
}

func extractPlainText(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	buf := make([]byte, maxExtractBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", false
	}
	return string(buf[:n]), true
}

func extractPDF(path string) (string, bool) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	rd, err := r.GetPlainText()
	if err != nil {
		return "", false
	}
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, rd, maxExtractBytes); err != nil && err != io.EOF {
		if buf.Len() == 0 {
			return "", false
		}
	}
	txt := buf.String()
	if strings.TrimSpace(txt) == "" {
		return "", false
	}
	return capText(txt), true
}

// extractOfficeZip pulls plain text out of a single XML entry inside an
// OOXML zip (docx's word/document.xml, xlsx's xl/sharedStrings.xml).
func extractOfficeZip(path, entryName string) (string, bool) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", false
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.Name == entryName {
			txt, ok := readZipEntryText(f)
			if !ok {
				return "", false
			}
			return capText(txt), true
		}
	}
	return "", false
}

// extractOfficeZipPrefix concatenates the text from every XML entry whose
// name starts with prefix (pptx's ppt/slides/slide1.xml, slide2.xml, ...).
func extractOfficeZipPrefix(path, prefix string) (string, bool) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", false
	}
	defer zr.Close()

	var sb strings.Builder
	found := false
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, prefix) || !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		txt, ok := readZipEntryText(f)
		if !ok {
			continue
		}
		found = true
		sb.WriteString(txt)
		sb.WriteString("\n")
		if sb.Len() >= maxExtractBytes {
			break
		}
	}
	if !found {
		return "", false
	}
	return capText(sb.String()), true
}

// readZipEntryText opens a zip file entry containing OOXML markup and
// returns its stripped text content (all character data concatenated).
func readZipEntryText(f *zip.File) (string, bool) {
	rc, err := f.Open()
	if err != nil {
		return "", false
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, maxExtractBytes*4))
	if err != nil && len(data) == 0 {
		return "", false
	}

	var sb strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if cd, ok := tok.(xml.CharData); ok {
			sb.Write(cd)
			sb.WriteString(" ")
		}
	}
	txt := strings.TrimSpace(sb.String())
	if txt == "" {
		return "", false
	}
	return txt, true
}
