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
)

const maxUploadSize = 64 << 20 // 64 MiB

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

// Upload handles POST /v1/media (multipart/form-data, field "file").
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
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
	ext := filepath.Ext(origName)
	storedName := mediaID + ext
	destPath := filepath.Join(h.storePath, storedName)

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
	mediaID := apipkg.Param(r, "mediaID")
	// Sanitize — only allow alphanumeric + hyphen.
	for _, c := range mediaID {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			http.Error(w, "invalid media id", http.StatusBadRequest)
			return
		}
	}

	// Find file with any extension.
	matches, err := filepath.Glob(filepath.Join(h.storePath, mediaID+".*"))
	if err != nil || len(matches) == 0 {
		// Also try no extension.
		p := filepath.Join(h.storePath, mediaID)
		if _, err := os.Stat(p); err == nil {
			http.ServeFile(w, r, p)
			return
		}
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, matches[0])
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
