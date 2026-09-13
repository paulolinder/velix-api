package media

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	apipkg "velix/internal/api"
	"velix/internal/domain/auth"
)

const maxUploadSize = 64 << 20 // 64 MiB

// allowedExtensions is the whitelist of file extensions accepted for upload.
// This prevents storing HTML/JS/SVG files that could be served inline and used for XSS.
var allowedExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".mp4": true, ".mp3": true, ".ogg": true,
	".opus": true, ".aac": true, ".avi": true, ".mov": true,
	".pdf": true, ".doc": true, ".docx": true, ".xls": true,
	".xlsx": true, ".ppt": true, ".pptx": true, ".txt": true,
	".zip": true, ".csv": true,
}

// inlineExtensions are types that browsers can safely display inline.
var inlineExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".mp4": true, ".mp3": true, ".ogg": true,
	".opus": true, ".aac": true, ".pdf": true,
}

// UploadResponse is returned after a successful media upload.
type UploadResponse struct {
	MediaID  string `json:"media_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
	URL      string `json:"url"` // local URL to download/serve this media
}

// Handler serves media upload and retrieval.
type Handler struct {
	storePath string // e.g. ./data/media
}

// NewHandler creates a media handler.
func NewHandler(storePath string) *Handler {
	_ = os.MkdirAll(storePath, 0o755)
	return &Handler{storePath: storePath}
}

// workspaceDir returns the workspace-scoped subdirectory for media storage.
// Each workspace gets its own subdirectory to prevent cross-tenant media access (IDOR).
func (h *Handler) workspaceDir(workspaceID string) string {
	// Sanitize workspaceID — it is a UUID so only alphanumeric + hyphen are valid.
	safe := sanitizeID(workspaceID)
	dir := filepath.Join(h.storePath, safe)
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// Upload handles POST /v1/media (multipart/form-data, field "file").
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil || claims.WorkspaceID == "" {
		apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
		return
	}

	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "file too large or invalid multipart form"))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "field 'file' is required"))
		return
	}
	defer file.Close()

	if header.Size > maxUploadSize {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "file exceeds 64 MiB limit"))
		return
	}

	mediaID := uuid.NewString()
	// Sanitize filename.
	origName := filepath.Base(header.Filename)
	if origName == "." || origName == "" {
		origName = "file"
	}
	ext := strings.ToLower(filepath.Ext(origName))
	if ext != "" && !allowedExtensions[ext] {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation,
			"file type not allowed: "+ext))
		return
	}
	storedName := mediaID + ext

	// Store inside the workspace-scoped subdirectory.
	destPath := filepath.Join(h.workspaceDir(claims.WorkspaceID), storedName)

	out, err := os.Create(destPath)
	if err != nil {
		apipkg.LogAndFail(w, r, fmt.Errorf("create file: %w", err), "media upload")
		return
	}
	defer out.Close()

	written, err := io.Copy(out, file)
	if err != nil {
		apipkg.LogAndFail(w, r, fmt.Errorf("write file: %w", err), "media upload")
		return
	}

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = mimeFromExt(ext)
	}

	apipkg.WriteJSON(w, r, http.StatusCreated, &UploadResponse{
		MediaID:  mediaID,
		FileName: origName,
		MimeType: mimeType,
		Size:     written,
		URL:      "/v1/media/" + mediaID,
	})
}

// Download handles GET /v1/media/{mediaID}.
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil || claims.WorkspaceID == "" {
		apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
		return
	}

	mediaID := apipkg.Param(r, "mediaID")
	// Sanitize — only allow alphanumeric + hyphen (UUID format).
	if !isValidID(mediaID) {
		http.Error(w, "invalid media id", http.StatusBadRequest)
		return
	}

	wsDir := h.workspaceDir(claims.WorkspaceID)

	serveMedia := func(path string) {
		ext := strings.ToLower(filepath.Ext(path))
		// Force download for types that are not safe to display inline,
		// preventing stored-XSS via malicious HTML/JS served from 'self'.
		if !inlineExtensions[ext] {
			w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(path))
		}
		http.ServeFile(w, r, path)
	}

	// Find file with any extension in the workspace directory.
	matches, err := filepath.Glob(filepath.Join(wsDir, mediaID+".*"))
	if err == nil && len(matches) > 0 {
		serveMedia(matches[0])
		return
	}
	// Also try no extension.
	p := filepath.Join(wsDir, mediaID)
	if _, err := os.Stat(p); err == nil {
		serveMedia(p)
		return
	}

	http.NotFound(w, r)
}

// isValidID returns true when s contains only lowercase hex digits and hyphens
// (i.e. is a valid UUID string). This prevents path traversal attacks.
func isValidID(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'f') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

// sanitizeID removes any character that is not alphanumeric or a hyphen,
// so workspace IDs (UUIDs) can be used safely as directory names.
func sanitizeID(id string) string {
	var b strings.Builder
	for _, c := range id {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func mimeFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}
