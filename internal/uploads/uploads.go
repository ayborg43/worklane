// Package uploads holds the file-saving logic shared by every feature that
// accepts a multipart upload (project/task attachments, chat attachments) —
// sanitizing the filename, avoiding collisions, and enforcing a size cap.
package uploads

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"regexp"
)

const MaxSize = 10 << 20 // 10MB

var unsafeFilenameChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func SanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = unsafeFilenameChars.ReplaceAllString(name, "_")
	if name == "" || name == "." {
		return "file"
	}
	return name
}

func randomHex(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// Save writes an uploaded multipart file to baseDir with a random-hex-prefixed
// sanitized name (avoiding both collisions and directory traversal), and
// returns the resulting path and byte count.
func Save(baseDir string, file multipart.File, header *multipart.FileHeader) (path string, size int64, err error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", 0, err
	}
	suffix, err := randomHex(8)
	if err != nil {
		return "", 0, err
	}
	path = filepath.Join(baseDir, suffix+"_"+SanitizeFilename(header.Filename))
	dst, err := os.Create(path)
	if err != nil {
		return "", 0, err
	}
	defer dst.Close()
	size, err = io.Copy(dst, file)
	if err != nil {
		return "", 0, err
	}
	return path, size, nil
}

// FormatSize renders a byte count as a human-readable size (e.g. "4.2 MB").
func FormatSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
