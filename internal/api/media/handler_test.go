package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"velix/internal/domain/auth"
	"velix/internal/server/middleware"
)

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	return NewHandler(t.TempDir())
}

func withClaims(r *http.Request, wsID string) *http.Request {
	ctx := middleware.WithClaims(r.Context(), &auth.Claims{WorkspaceID: wsID})
	return r.WithContext(ctx)
}

func withChiParam(r *http.Request, key, val string) *http.Request {
	rc := chi.NewRouteContext()
	rc.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
}

func buildMultipart(t *testing.T, fileName string, content []byte) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = fw.Write(content)
	mw.Close()
	return &buf, mw.FormDataContentType()
}

// ---------------------------------------------------------------------------
// Upload tests
// ---------------------------------------------------------------------------

func TestUpload_AllowedExtensions(t *testing.T) {
	h := newTestHandler(t)
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp", ".mp4", ".mp3", ".ogg", ".pdf", ".docx", ".csv"} {
		t.Run(ext, func(t *testing.T) {
			body, ct := buildMultipart(t, "file"+ext, []byte("fake"))
			req := httptest.NewRequest(http.MethodPost, "/v1/media", body)
			req.Header.Set("Content-Type", ct)
			req = withClaims(req, "ws-id")
			w := httptest.NewRecorder()
			h.Upload(w, req)
			if w.Code != http.StatusCreated {
				t.Errorf("%s: got %d, want 201; body: %s", ext, w.Code, w.Body.String())
			}
		})
	}
}

func TestUpload_BlockedExtensions(t *testing.T) {
	h := newTestHandler(t)
	for _, ext := range []string{".html", ".htm", ".js", ".svg", ".php", ".sh", ".bat", ".exe", ".py"} {
		t.Run(ext, func(t *testing.T) {
			body, ct := buildMultipart(t, "bad"+ext, []byte("<script>alert(1)</script>"))
			req := httptest.NewRequest(http.MethodPost, "/v1/media", body)
			req.Header.Set("Content-Type", ct)
			req = withClaims(req, "ws-id")
			w := httptest.NewRecorder()
			h.Upload(w, req)
			if w.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s: got %d, want 422", ext, w.Code)
			}
			if !strings.Contains(w.Body.String(), "not allowed") {
				t.Errorf("%s: response should mention 'not allowed': %s", ext, w.Body.String())
			}
		})
	}
}

func TestUpload_Unauthenticated(t *testing.T) {
	h := newTestHandler(t)
	body, ct := buildMultipart(t, "img.jpg", []byte("fake"))
	req := httptest.NewRequest(http.MethodPost, "/v1/media", body)
	req.Header.Set("Content-Type", ct)
	// No claims in context.
	w := httptest.NewRecorder()
	h.Upload(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", w.Code)
	}
}

func TestUpload_MissingFileField(t *testing.T) {
	h := newTestHandler(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/media", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = withClaims(req, "ws-id")
	w := httptest.NewRecorder()
	h.Upload(w, req)
	if w.Code == http.StatusCreated {
		t.Error("expected non-201 when 'file' field is absent")
	}
}

// ---------------------------------------------------------------------------
// Download tests
// ---------------------------------------------------------------------------

func TestDownload_ImageServedInline(t *testing.T) {
	h := newTestHandler(t)
	wsDir := h.workspaceDir("ws-id")
	id := "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb"
	path := fmt.Sprintf("%s/%s.png", wsDir, id)
	_ = os.WriteFile(path, []byte("\x89PNG"), 0o644)

	req := httptest.NewRequest(http.MethodGet, "/v1/media/"+id, nil)
	req = withClaims(req, "ws-id")
	req = withChiParam(req, "mediaID", id)

	w := httptest.NewRecorder()
	h.Download(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatal("file not found")
	}
	cd := w.Header().Get("Content-Disposition")
	if strings.Contains(cd, "attachment") {
		t.Errorf("PNG should not force download, got Content-Disposition: %q", cd)
	}
}

func TestDownload_DocumentForcesAttachment(t *testing.T) {
	h := newTestHandler(t)
	wsDir := h.workspaceDir("ws-id")
	id := "11112222-3333-4444-5555-666677778888"
	path := fmt.Sprintf("%s/%s.docx", wsDir, id)
	_ = os.WriteFile(path, []byte("PK fake docx"), 0o644)

	req := httptest.NewRequest(http.MethodGet, "/v1/media/"+id, nil)
	req = withClaims(req, "ws-id")
	req = withChiParam(req, "mediaID", id)

	w := httptest.NewRecorder()
	h.Download(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatal("file not found")
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("docx should force download, got Content-Disposition: %q", cd)
	}
}

func TestDownload_NotFound(t *testing.T) {
	h := newTestHandler(t)
	id := "00000000-0000-0000-0000-000000000000"

	req := httptest.NewRequest(http.MethodGet, "/v1/media/"+id, nil)
	req = withClaims(req, "ws-id")
	req = withChiParam(req, "mediaID", id)

	w := httptest.NewRecorder()
	h.Download(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", w.Code)
	}
}

func TestDownload_InvalidMediaID(t *testing.T) {
	h := newTestHandler(t)

	// Path traversal attempt.
	req := httptest.NewRequest(http.MethodGet, "/v1/media/../../etc/passwd", nil)
	req = withClaims(req, "ws-id")
	req = withChiParam(req, "mediaID", "../../etc/passwd")

	w := httptest.NewRecorder()
	h.Download(w, req)
	if w.Code == http.StatusOK {
		t.Error("path traversal attempt should not succeed")
	}
}
