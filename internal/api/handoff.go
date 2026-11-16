package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"

	"github.com/zichuanxu/nimget/internal/download"
	"github.com/zichuanxu/nimget/internal/pairing"
)

type ExtensionHandoffs interface {
	AcceptHandoff(ctx context.Context, pairingID, handoffID string, req download.Request) (download.HandoffReceipt, error)
}

type extensionHandoffRequest struct {
	HandoffID string `json:"handoffId"`
	URL       string `json:"url"`
}

var handoffIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (h *Handler) acceptExtensionHandoff(w http.ResponseWriter, r *http.Request) {
	status, err := h.extensionPairingStatus(r)
	if err != nil {
		h.writePairingError(w, err)
		return
	}
	if status.Status != pairing.Connected {
		writeError(w, http.StatusForbidden, "PAIRING_REQUIRED", download.ErrHandoffPairing.Error())
		return
	}
	if h.handoffs == nil {
		writeError(w, http.StatusServiceUnavailable, "HANDOFF_UNAVAILABLE", "Browser handoff is unavailable")
		return
	}
	var input extensionHandoffRequest
	if !decode(w, r, &input) {
		return
	}
	if !handoffIDPattern.MatchString(input.HandoffID) {
		writeError(w, http.StatusBadRequest, "INVALID_HANDOFF_ID", "handoffId must be a lowercase UUID")
		return
	}
	target, err := url.Parse(input.URL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" || target.User != nil {
		writeError(w, http.StatusBadRequest, "UNSUPPORTED_LINK", "Send an absolute HTTP or HTTPS link without embedded credentials")
		return
	}

	receipt, err := h.handoffs.AcceptHandoff(r.Context(), status.ID, input.HandoffID, download.Request{URL: input.URL})
	switch {
	case errors.Is(err, download.ErrHandoffPairing):
		writeError(w, http.StatusForbidden, "PAIRING_REQUIRED", err.Error())
	case errors.Is(err, download.ErrHandoffConflict):
		writeError(w, http.StatusConflict, "HANDOFF_CONFLICT", err.Error())
	case errors.Is(err, download.ErrInvalidDownload):
		writeError(w, http.StatusBadRequest, "HANDOFF_REJECTED", "NimGet could not queue this download with its current settings")
	case err != nil:
		h.log.Error("persist browser handoff failed", "err", err)
		writeError(w, http.StatusInternalServerError, "HANDOFF_STORAGE_FAILED", "NimGet could not confirm that it saved the download")
	case receipt.Created:
		writeJSON(w, http.StatusCreated, receipt)
	default:
		writeJSON(w, http.StatusOK, receipt)
	}
}
