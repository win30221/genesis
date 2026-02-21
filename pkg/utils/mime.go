package utils

import (
	"mime"
	"net/http"
	"os"
)

// DetectFileMimeAndExt analyzes a file on disk to determine both its MIME type and standard extension.
// It returns ("application/octet-stream", ".png") if identification fails.
func DetectFileMimeAndExt(filePath string) (string, string) {
	mimeType := "application/octet-stream"
	if f, err := os.Open(filePath); err == nil {
		defer f.Close()
		buffer := make([]byte, 512)
		if n, err := f.Read(buffer); err == nil && n > 0 {
			mimeType = http.DetectContentType(buffer[:n])
		}
	}
	return mimeType, mimeToExt(mimeType)
}

// DetectMimeAndExt analyzes a byte slice to determine both its MIME type and standard extension.
// It returns ("application/octet-stream", ".png") if identification fails.
func DetectMimeAndExt(data []byte) (string, string) {
	mimeType := "application/octet-stream"
	if len(data) > 0 {
		mimeType = http.DetectContentType(data)
	}
	return mimeType, mimeToExt(mimeType)
}

// mimeToExt converts a MIME type to its first standard extension, defaulting to ".png".
func mimeToExt(mimeType string) string {
	exts, err := mime.ExtensionsByType(mimeType)
	if err != nil || len(exts) == 0 {
		return ".png"
	}
	return exts[0]
}
