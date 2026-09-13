package chatwoot

import (
	"testing"

	"velix/internal/engine"
)

// ---------------------------------------------------------------------------
// extFromMIME
// ---------------------------------------------------------------------------

func TestExtFromMIME(t *testing.T) {
	cases := []struct {
		mime string
		want string
	}{
		// Audio
		{"audio/ogg", ".ogg"},
		{"audio/ogg; codecs=opus", ".ogg"},
		{"audio/opus", ".ogg"},
		{"audio/mpeg", ".mp3"},
		{"audio/mp3", ".mp3"},
		{"audio/aac", ".aac"},
		{"audio/amr", ".amr"},
		{"audio/wav", ".wav"},
		{"audio/x-wav", ".wav"},
		// Image
		{"image/jpeg", ".jpg"},
		{"image/png", ".png"},
		{"image/webp", ".webp"},
		{"image/gif", ".gif"},
		// Video
		{"video/mp4", ".mp4"},
		{"video/3gpp", ".3gp"},
		// Documents
		{"application/pdf", ".pdf"},
		{"application/vnd.ms-excel", ".xlsx"},
		{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".xlsx"},
		{"application/msword", ".docx"},
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx"},
		// Unknown falls back to .bin
		{"application/x-unknown", ".bin"},
		{"text/plain", ".bin"},
		{"", ".bin"},
	}

	for _, c := range cases {
		got := extFromMIME(c.mime)
		if got != c.want {
			t.Errorf("extFromMIME(%q) = %q, want %q", c.mime, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// jidToPhone
// ---------------------------------------------------------------------------

func TestJIDToPhone(t *testing.T) {
	cases := []struct {
		jid  string
		want string
	}{
		{"5511999990000@s.whatsapp.net", "5511999990000"},
		{"5521987654321@s.whatsapp.net", "5521987654321"},
		{"5511999990000", "5511999990000"}, // no @ → return as-is
		{"", ""},
		{"120363012345@g.us", "120363012345"},
	}

	for _, c := range cases {
		got := jidToPhone(c.jid)
		if got != c.want {
			t.Errorf("jidToPhone(%q) = %q, want %q", c.jid, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// isOutgoingMessage
// ---------------------------------------------------------------------------

func TestIsOutgoingMessage(t *testing.T) {
	cases := []struct {
		raw  any
		want bool
	}{
		// Chatwoot v2 sends string
		{"outgoing", true},
		{"incoming", false},
		{"", false},
		// Chatwoot v3 sends numeric type
		{float64(1), true},   // outgoing
		{float64(0), false},  // incoming
		{float64(2), false},  // activity
		// Other types
		{nil, false},
		{true, false},
		{int(1), false}, // int (not float64) is not matched
	}

	for _, c := range cases {
		got := isOutgoingMessage(c.raw)
		if got != c.want {
			t.Errorf("isOutgoingMessage(%v) = %v, want %v", c.raw, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// buildMessageContent
// ---------------------------------------------------------------------------

func TestBuildMessageContent(t *testing.T) {
	cases := []struct {
		name string
		p    *engine.MessagePayload
		want string
	}{
		{
			name: "plain text",
			p:    &engine.MessagePayload{Text: "hello"},
			want: "hello",
		},
		{
			name: "media with caption",
			p: &engine.MessagePayload{
				Type:  "image",
				Media: &engine.MediaInfo{Caption: "foto legal"},
			},
			want: "[IMAGE] foto legal",
		},
		{
			name: "media without caption",
			p: &engine.MessagePayload{
				Type:  "video",
				Media: &engine.MediaInfo{},
			},
			want: "[VIDEO]",
		},
		{
			name: "audio without caption",
			p: &engine.MessagePayload{
				Type:  "audio",
				Media: &engine.MediaInfo{},
			},
			want: "[AUDIO]",
		},
		{
			name: "location with name",
			p: &engine.MessagePayload{
				Location: &engine.LocationInfo{
					Name:      "Pão de Açúcar",
					Latitude:  -22.948,
					Longitude: -43.157,
				},
			},
			want: "[Localização: Pão de Açúcar — -22.948000, -43.157000]",
		},
		{
			name: "location without name",
			p: &engine.MessagePayload{
				Location: &engine.LocationInfo{
					Latitude:  -23.5505,
					Longitude: -46.6333,
				},
			},
			want: "[Localização: -23.550500, -46.633300]",
		},
		{
			name: "vcard",
			p: &engine.MessagePayload{
				VCard: &engine.VCardInfo{DisplayName: "João Silva"},
			},
			want: "[Contato: João Silva]",
		},
		{
			name: "empty payload",
			p:    &engine.MessagePayload{},
			want: "",
		},
		{
			name: "text takes priority over media",
			p: &engine.MessagePayload{
				Text:  "olá",
				Type:  "image",
				Media: &engine.MediaInfo{Caption: "foto"},
			},
			want: "olá",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildMessageContent(c.p)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// fileTypeToMediaType
// ---------------------------------------------------------------------------

func TestFileTypeToMediaType(t *testing.T) {
	cases := []struct {
		fileType string
		mimeType string
		want     engine.MediaType
	}{
		// Explicit file_type from Chatwoot
		{"image", "image/jpeg", engine.MediaTypeImage},
		{"IMAGE", "image/png", engine.MediaTypeImage}, // case-insensitive
		{"video", "video/mp4", engine.MediaTypeVideo},
		{"audio", "audio/ogg", engine.MediaTypeAudio},
		{"sticker", "image/webp", engine.MediaTypeSticker},
		// "file" type — derive from MIME
		{"file", "image/jpeg", engine.MediaTypeImage},
		{"file", "video/mp4", engine.MediaTypeVideo},
		{"file", "audio/mpeg", engine.MediaTypeAudio},
		{"file", "application/pdf", engine.MediaTypeDocument},
		{"file", "application/zip", engine.MediaTypeDocument},
		// Unknown file_type — fall through to MIME-based detection
		{"", "image/png", engine.MediaTypeImage},
		{"unknown", "audio/ogg", engine.MediaTypeAudio},
		{"", "application/octet-stream", engine.MediaTypeDocument},
	}

	for _, c := range cases {
		got := fileTypeToMediaType(c.fileType, c.mimeType)
		if got != c.want {
			t.Errorf("fileTypeToMediaType(%q, %q) = %q, want %q", c.fileType, c.mimeType, got, c.want)
		}
	}
}
