package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/httporigin"
	"github.com/amemiya02/nimget/internal/pairing"
	"github.com/amemiya02/nimget/internal/settings"
)

type ExtensionHandoffs interface {
	AcceptHandoff(ctx context.Context, pairingID, handoffID, pairingCredential string, req download.Request, captureProof string) (download.HandoffReceipt, error)
	LookupHandoff(ctx context.Context, pairingID, handoffID string) (download.HandoffReceipt, error)
}

type extensionHandoffRequest struct {
	HandoffID      string                   `json:"handoffId"`
	URL            string                   `json:"url"`
	CaptureProof   string                   `json:"captureProof,omitempty"`
	WebsiteSession *download.WebsiteSession `json:"websiteSession,omitempty"`
}

var captureProofPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

var handoffIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (h *Handler) authorizedHandoffPairing(w http.ResponseWriter, r *http.Request) string {
	status, err := h.extensionPairingStatus(r)
	if err != nil {
		h.writePairingError(w, err)
		return ""
	}
	if status.Status != pairing.Connected {
		writeError(w, http.StatusForbidden, "PAIRING_REQUIRED", download.ErrHandoffPairing.Error())
		return ""
	}
	if h.handoffs == nil {
		writeError(w, http.StatusServiceUnavailable, "HANDOFF_UNAVAILABLE", "Browser handoff is unavailable")
		return ""
	}
	return status.ID
}

func (h *Handler) acceptExtensionHandoff(w http.ResponseWriter, r *http.Request) {
	pairingID := h.authorizedHandoffPairing(w, r)
	if pairingID == "" {
		return
	}
	var input extensionHandoffRequest
	if !decodeHandoff(w, r, &input, &input.HandoffID) {
		return
	}
	target, err := url.Parse(input.URL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" || target.User != nil {
		writeError(w, http.StatusBadRequest, "UNSUPPORTED_LINK", "Send an absolute HTTP or HTTPS link without embedded credentials")
		return
	}
	if input.WebsiteSession != nil && !validWebsiteSession(target, input.WebsiteSession) {
		writeError(w, http.StatusBadRequest, "INVALID_WEBSITE_SESSION", "The website session could not be used. Try sending the link again.")
		return
	}

	if input.CaptureProof != "" && !captureProofPattern.MatchString(input.CaptureProof) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Invalid automatic capture metadata")
		return
	}

	req := download.Request{URL: input.URL, WebsiteSession: input.WebsiteSession}
	receipt, err := h.handoffs.AcceptHandoff(r.Context(), pairingID, input.HandoffID, extensionCredential(r), req, input.CaptureProof)
	h.writeHandoffReceipt(w, receipt, err)
}

func (h *Handler) lookupExtensionHandoff(w http.ResponseWriter, r *http.Request) {
	pairingID := h.authorizedHandoffPairing(w, r)
	if pairingID == "" {
		return
	}
	var input struct {
		HandoffID string `json:"handoffId"`
	}
	if !decodeHandoff(w, r, &input, &input.HandoffID) {
		return
	}
	receipt, err := h.handoffs.LookupHandoff(r.Context(), pairingID, input.HandoffID)
	h.writeHandoffReceipt(w, receipt, err)
}

func decodeHandoff(w http.ResponseWriter, r *http.Request, input any, id *string) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		// JSON errors can contain unknown field names from the request. Never
		// quote the input of an endpoint that receives embedded credentials.
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Invalid handoff request body")
		return false
	}
	if !handoffIDPattern.MatchString(*id) {
		writeError(w, http.StatusBadRequest, "INVALID_HANDOFF_ID", "handoffId must be a lowercase UUID")
		return false
	}
	return true
}

func validWebsiteSession(target *url.URL, session *download.WebsiteSession) bool {
	if strings.TrimSpace(session.Cookie) == "" || len(session.Cookie) > 32<<10 || len(session.UserAgent) > 512 ||
		session.ExpiresAt <= 0 || session.ExpiresAt > time.Now().Add(24*time.Hour+time.Minute).Unix() {
		return false
	}
	if settings.ValidateHeader("Cookie", session.Cookie) != nil || settings.ValidateHeader("User-Agent", session.UserAgent) != nil {
		return false
	}
	if session.Referer == "" {
		return true
	}
	origin, err := url.Parse(session.Referer)
	return err == nil && origin.User == nil && origin.EscapedPath() == "/" && origin.RawQuery == "" &&
		!origin.ForceQuery && origin.Fragment == "" && session.Referer == origin.Scheme+"://"+origin.Host+"/" &&
		httporigin.Key(origin) == httporigin.Key(target)
}

func (h *Handler) writeHandoffReceipt(w http.ResponseWriter, receipt download.HandoffReceipt, err error) {
	switch {
	case errors.Is(err, download.ErrHandoffPairing):
		writeError(w, http.StatusForbidden, "PAIRING_REQUIRED", err.Error())
	case errors.Is(err, download.ErrHandoffConflict):
		writeError(w, http.StatusConflict, "HANDOFF_CONFLICT", err.Error())
	case errors.Is(err, download.ErrCaptureNotReplayable):
		writeError(w, http.StatusBadRequest, "CAPTURE_NOT_REPLAYABLE", err.Error())
	case errors.Is(err, download.ErrInvalidDownload):
		writeError(w, http.StatusBadRequest, "HANDOFF_REJECTED", "NimGet could not queue this download with its current settings")
	case err != nil:
		h.log.Error("persist browser handoff failed", "err", err)
		writeError(w, http.StatusInternalServerError, "HANDOFF_STORAGE_FAILED", "NimGet could not confirm that it saved the download")
	case receipt.Created:
		writeJSON(w, http.StatusCreated, receipt)
	case receipt.TaskID == "":
		writeError(w, http.StatusNotFound, "HANDOFF_NOT_FOUND", "NimGet has no receipt for this handoff")
	default:
		writeJSON(w, http.StatusOK, receipt)
	}
}
