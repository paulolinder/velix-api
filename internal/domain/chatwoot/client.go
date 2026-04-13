// Package chatwoot provides the integration between WhatsApp instances and Chatwoot inboxes.
package chatwoot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"time"
)

// Client wraps the Chatwoot REST API for a single account.
type Client struct {
	base       string // e.g. "https://app.chatwoot.com/api/v1/accounts/42"
	token      string
	httpClient *http.Client
}

// NewClient creates a Chatwoot API client for the given account.
func NewClient(chatwootURL, token string, accountID int64) *Client {
	return &Client{
		base:       fmt.Sprintf("%s/api/v1/accounts/%d", chatwootURL, accountID),
		token:      token,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// --- Contact -----------------------------------------------------------------

// FindOrCreateContact returns the Chatwoot contact ID for the given phone number,
// creating the contact if it does not exist.
func (c *Client) FindOrCreateContact(ctx context.Context, name, phone string, inboxID int64) (int64, error) {
	if id, err := c.searchContact(ctx, phone); err == nil {
		return id, nil
	}
	return c.createContact(ctx, name, phone, inboxID)
}

func (c *Client) searchContact(ctx context.Context, phone string) (int64, error) {
	data, err := c.do(ctx, http.MethodGet,
		c.base+"/contacts/search?q="+url.QueryEscape(phone)+"&include_contacts=true",
		nil)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Payload []struct {
			ID int64 `json:"id"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, err
	}
	if len(resp.Payload) == 0 {
		return 0, fmt.Errorf("contact not found")
	}
	return resp.Payload[0].ID, nil
}

func (c *Client) createContact(ctx context.Context, name, phone string, inboxID int64) (int64, error) {
	body := map[string]any{
		"name":         name,
		"phone_number": phone,
	}
	if inboxID != 0 {
		body["inbox_id"] = inboxID
	}
	data, err := c.do(ctx, http.MethodPost, c.base+"/contacts", body)
	if err != nil {
		return 0, err
	}
	// Chatwoot wraps the contact differently across versions.
	var v1 struct {
		ID int64 `json:"id"`
	}
	var v2 struct {
		Payload struct {
			Contact struct {
				ID int64 `json:"id"`
			} `json:"contact"`
		} `json:"payload"`
	}
	_ = json.Unmarshal(data, &v1)
	_ = json.Unmarshal(data, &v2)
	if v2.Payload.Contact.ID != 0 {
		return v2.Payload.Contact.ID, nil
	}
	if v1.ID != 0 {
		return v1.ID, nil
	}
	return 0, fmt.Errorf("chatwoot: could not create contact, response: %s", string(data))
}

// --- Conversation ------------------------------------------------------------

// FindOrCreateConversation returns the Chatwoot conversation ID for the given
// contact and inbox, creating one if it does not exist.
// pending=true starts the conversation in "pending" status (awaiting agent).
func (c *Client) FindOrCreateConversation(ctx context.Context, contactID, inboxID int64, pending bool) (int64, error) {
	if id, err := c.findConversation(ctx, contactID, inboxID); err == nil {
		return id, nil
	}
	return c.createConversation(ctx, contactID, inboxID, pending)
}

func (c *Client) findConversation(ctx context.Context, contactID, inboxID int64) (int64, error) {
	data, err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("%s/contacts/%d/conversations", c.base, contactID), nil)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Payload []struct {
			ID      int64  `json:"id"`
			InboxID int64  `json:"inbox_id"`
			Status  string `json:"status"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, err
	}
	for _, conv := range resp.Payload {
		if conv.InboxID == inboxID {
			return conv.ID, nil
		}
	}
	return 0, fmt.Errorf("conversation not found")
}

func (c *Client) createConversation(ctx context.Context, contactID, inboxID int64, pending bool) (int64, error) {
	body := map[string]any{
		"contact_id": contactID,
		"inbox_id":   inboxID,
	}
	if pending {
		body["status"] = "pending"
	}
	data, err := c.do(ctx, http.MethodPost,
		c.base+"/conversations",
		body)
	if err != nil {
		return 0, err
	}
	var resp struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, err
	}
	if resp.ID == 0 {
		return 0, fmt.Errorf("chatwoot: could not create conversation, response: %s", string(data))
	}
	return resp.ID, nil
}

// ReopenConversation sets the conversation status back to "open".
func (c *Client) ReopenConversation(ctx context.Context, convID int64) error {
	_, err := c.do(ctx, http.MethodPatch,
		fmt.Sprintf("%s/conversations/%d", c.base, convID),
		map[string]any{"status": "open"})
	return err
}

// --- Messages ----------------------------------------------------------------

// PostIncomingMessage posts a customer text message (WhatsApp inbound) to a Chatwoot conversation.
func (c *Client) PostIncomingMessage(ctx context.Context, convID int64, content string) error {
	_, err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/conversations/%d/messages", c.base, convID),
		map[string]any{
			"content":      content,
			"message_type": "incoming",
			"private":      false,
		})
	return err
}

// PostIncomingMedia uploads a media file as an incoming message attachment.
// fileName is the file name shown to the agent; mimeType is the MIME type; data is the file bytes.
func (c *Client) PostIncomingMedia(ctx context.Context, convID int64, caption, fileName, mimeType string, data []byte) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	_ = mw.WriteField("message_type", "incoming")
	_ = mw.WriteField("private", "false")
	if caption != "" {
		_ = mw.WriteField("content", caption)
	}

	// Derive file extension from MIME type when no filename available.
	if fileName == "" {
		exts, _ := mime.ExtensionsByType(mimeType)
		ext := ".bin"
		if len(exts) > 0 {
			ext = exts[0]
		}
		fileName = "attachment" + ext
	}

	part, err := mw.CreateFormFile("attachments[]", filepath.Base(fileName))
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	_ = mw.Close()

	rawURL := fmt.Sprintf("%s/conversations/%d/messages", c.base, convID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("api_access_token", c.token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("chatwoot upload %d → %d: %s", convID, resp.StatusCode, string(body))
	}
	return nil
}

// DeleteMessage marks a message as deleted in Chatwoot.
func (c *Client) DeleteMessage(ctx context.Context, convID, msgID int64) error {
	_, err := c.do(ctx, http.MethodDelete,
		fmt.Sprintf("%s/conversations/%d/messages/%d", c.base, convID, msgID),
		nil)
	return err
}

// UpdateContactAvatar sets the profile picture for a contact.
func (c *Client) UpdateContactAvatar(ctx context.Context, contactID int64, avatarURL string) error {
	_, err := c.do(ctx, http.MethodPatch,
		fmt.Sprintf("%s/contacts/%d", c.base, contactID),
		map[string]any{"avatar_url": avatarURL})
	return err
}

// --- HTTP --------------------------------------------------------------------

func (c *Client) do(ctx context.Context, method, rawURL string, body any) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("api_access_token", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("chatwoot api %s %s → %d: %s", method, rawURL, resp.StatusCode, string(data))
	}
	return data, nil
}
