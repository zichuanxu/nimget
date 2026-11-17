package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/amemiya02/nimget/internal/pairing"
)

// The public manifest key gives the unpacked Chrome and Edge builds one ID.
const extensionOrigin = "chrome-extension://cbpkcafcmablmlkpkmmeincekjglfafb"

type ExtensionPairings interface {
	RequestExtensionPairing(ctx context.Context, token, browser string) (pairing.Status, error)
	ExtensionPairingStatus(ctx context.Context, token string) (pairing.Status, error)
}

func (h *Handler) guardExtension(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		origins := r.Header.Values("Origin")
		if len(origins) != 1 || origins[0] != extensionOrigin {
			writeError(w, http.StatusForbidden, "FORBIDDEN_ORIGIN", "Only the NimGet extension can use this route")
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", extensionOrigin)
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodOptions {
			if r.Header.Get("Access-Control-Request-Method") != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Extension commands use POST")
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if h.pairings == nil {
			writeError(w, http.StatusServiceUnavailable, "PAIRING_UNAVAILABLE", "Extension pairing is unavailable")
			return
		}
		if h.authorized(r) {
			h.writePairingError(w, pairing.ErrCredential)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func extensionCredential(r *http.Request) string {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return token
}

func (h *Handler) requestExtensionPairing(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Browser string `json:"browser"`
	}
	if !decode(w, r, &input) {
		return
	}
	status, err := h.pairings.RequestExtensionPairing(r.Context(), extensionCredential(r), input.Browser)
	if err != nil {
		h.writePairingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// extensionPairingStatus has no side effects. Browser commands must check for
// pairing.Connected again on every request and scope it to status.ID.
func (h *Handler) extensionPairingStatus(r *http.Request) (pairing.Status, error) {
	return h.pairings.ExtensionPairingStatus(r.Context(), extensionCredential(r))
}

func (h *Handler) readExtensionPairing(w http.ResponseWriter, r *http.Request) {
	status, err := h.extensionPairingStatus(r)
	if err != nil {
		h.writePairingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *Handler) writePairingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pairing.ErrCredential):
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "EXTENSION_CREDENTIAL_REQUIRED", err.Error())
	case errors.Is(err, pairing.ErrBrowser):
		writeError(w, http.StatusBadRequest, "INVALID_BROWSER", err.Error())
	case errors.Is(err, pairing.ErrPendingLimit):
		writeError(w, http.StatusTooManyRequests, "PAIRING_LIMIT", err.Error())
	default:
		h.log.Error("extension pairing operation failed", "err", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Unable to process extension pairing")
	}
}
