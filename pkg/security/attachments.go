package security

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
)

// MaxAttachmentBytes is the largest file the app will send (WhatsApp's media limit for documents is higher,
// but 16 MB keeps uploads reliable on the unofficial connection).
const MaxAttachmentBytes = 16 << 20

// AttachmentValidationResult describes the outcome of an attachment check.
type AttachmentValidationResult struct {
	Valid        bool   `json:"valid"`
	Error        string `json:"error,omitempty"`
	DetectedMIME string `json:"detected_mime,omitempty"`
	CleanName    string `json:"clean_name,omitempty"`
	Size         int64  `json:"size"`
	IsImage      bool   `json:"is_image"`
}

type fileKind struct {
	mime string
	exts []string
	img  bool
}

// sniff identifies a file purely from its leading bytes. Only these four formats are allowed.
func sniff(data []byte) *fileKind {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return &fileKind{"image/png", []string{".png"}, true}
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return &fileKind{"image/jpeg", []string{".jpg", ".jpeg"}, true}
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return &fileKind{"image/webp", []string{".webp"}, true}
	case len(data) >= 5 && string(data[0:5]) == "%PDF-":
		return &fileKind{"application/pdf", []string{".pdf"}, false}
	}
	return nil
}

// ValidateAttachment accepts only PNG, JPEG, WebP and PDF files identified by their
// content signature, enforces maxBytes, and returns a safe file name whose extension
// matches the real content.
func ValidateAttachment(filename string, data []byte, maxBytes int64) AttachmentValidationResult {
	size := int64(len(data))
	if size == 0 {
		return AttachmentValidationResult{Error: "The file is empty."}
	}
	if maxBytes <= 0 {
		maxBytes = MaxAttachmentBytes
	}
	if size > maxBytes {
		return AttachmentValidationResult{Size: size, Error: fmt.Sprintf("The file is %.1f MB. The limit is %d MB.", float64(size)/(1<<20), maxBytes>>20)}
	}

	name := strings.TrimSpace(strings.ReplaceAll(filepath.Base(strings.ReplaceAll(filename, "\\", "/")), "\x00", ""))
	var clean strings.Builder
	for _, r := range name {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			continue
		}
		clean.WriteRune(r)
	}
	name = clean.String()
	if name == "" || name == "." || name == ".." {
		name = "attachment"
	}
	if len(name) > 120 {
		name = name[len(name)-120:]
	}

	kind := sniff(data)
	if kind == nil {
		return AttachmentValidationResult{Size: size, CleanName: name, Error: "Only PNG, JPEG, WebP images and PDF documents can be sent. This file's content is not one of those formats."}
	}

	ext := strings.ToLower(filepath.Ext(name))
	match := false
	for _, e := range kind.exts {
		if ext == e {
			match = true
			break
		}
	}
	if !match {
		name = strings.TrimSuffix(name, filepath.Ext(name)) + kind.exts[0]
	}

	return AttachmentValidationResult{Valid: true, DetectedMIME: kind.mime, CleanName: name, Size: size, IsImage: kind.img}
}
