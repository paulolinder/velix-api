package ws

import (
	"net/http"
	"slices"
	"time"

	"github.com/coder/websocket"

	apipkg "velix/internal/api"
	"velix/internal/domain/auth"
)

// Handler serves the WebSocket upgrade endpoint.
type Handler struct {
	hub    *Hub
	jwtSvc JWTVerifier
}

// JWTVerifier can validate a JWT string and return claims.
type JWTVerifier interface {
	ParseToken(token string) (*auth.Claims, error)
}

// NewHandler creates a WebSocket upgrade handler.
func NewHandler(hub *Hub, jwtSvc JWTVerifier) *Handler {
	return &Handler{hub: hub, jwtSvc: jwtSvc}
}

// ServeHTTP upgrades the connection to WebSocket and streams events.
// Authentication is via ?token=<jwt> query parameter because the WebSocket
// handshake does not support custom headers in browsers.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeUnauthorized, "token query parameter required"))
		return
	}

	claims, err := h.jwtSvc.ParseToken(token)
	if err != nil {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeUnauthorized, "invalid token"))
		return
	}

	// The WS stream carries every workspace event, including received message
	// content — require messages:view (or admin) before allowing the upgrade.
	if claims.Role != auth.RoleAdmin && !slices.Contains(claims.Permissions, string(auth.PermMessagesView)) {
		apipkg.WriteError(w, r, apipkg.ErrForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Allow connections from any origin — auth is enforced via JWT token.
		InsecureSkipVerify: true,
	})
	if err != nil {
		h.hub.log.Error().Err(err).Msg("ws: failed to upgrade connection")
		return
	}
	defer conn.CloseNow()

	c := &client{
		workspaceID: claims.WorkspaceID,
		send:        make(chan []byte, 256),
	}
	h.hub.register(c)
	defer h.hub.unregister(c)

	ctx := r.Context()

	// Ping loop — keeps connection alive and detects dead peers.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := conn.Ping(ctx); err != nil {
					return
				}
			}
		}
	}()

	// Write loop — forward hub messages to the WebSocket client.
	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "server shutdown")
			return
		case msg, ok := <-c.send:
			if !ok {
				conn.Close(websocket.StatusNormalClosure, "closed")
				return
			}
			if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
				return
			}
		}
	}
}

