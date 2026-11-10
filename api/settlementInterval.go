package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	log "github.com/sirupsen/logrus"
)

// settlementIntervalRequest is the body of POST /eeg/settlementinterval (konzept-abrechnungsintervall.md): web passes
// billing's settlementState on as billingState.
type settlementIntervalRequest struct {
	SettlementInterval string              `json:"settlementInterval"`
	DryRun             bool                `json:"dryRun"`
	BillingState       *model.BillingState `json:"billingState"`
}

type settlementIntervalResponse struct {
	model.SettlementIntervalDecision
	Eeg *model.Eeg `json:"eeg"`
}

// maxSettlementIntervalBytes bounds the body: two lists of clearing period identifiers.
const maxSettlementIntervalBytes = 1 << 20

// changeSettlementInterval checks a change of the settlement interval against the rule and, without dryRun, stores
// it with its record. 200 with the decision (allowed false only with dryRun), 409 when the rule blocks a change, 400
// for an invalid request or the current interval. A change and every refusal are logged with an event
// (eegfaktura-dev#54 #5): eeg.settlement_interval.changed, .refused or .failed, tenant from the request, no user name.
func (h *EegHandler) changeSettlementInterval() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		var req settlementIntervalRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSettlementIntervalBytes))
		err := dec.Decode(&req)
		// nothing after the JSON body but white space (eegfaktura-dev#54 #9)
		if err == nil && dec.Decode(&json.RawMessage{}) != io.EOF {
			err = errors.New("data after the JSON body")
		}
		if err != nil {
			// the decoder's text names Go types and fields: only in the log (eegfaktura-dev#54 #12)
			message := "Die Anfrage konnte nicht gelesen werden (kein gültiges JSON)"
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				message = "Die Anfrage ist zu groß"
			}
			refuseSettlementInterval(w, r, http.StatusBadRequest, 2000, message, log.Fields{log.ErrorKey: err})
			return
		}
		if !model.IsSettlementInterval(req.SettlementInterval) {
			refuseSettlementInterval(w, r, http.StatusBadRequest, 1060,
				"Ungültiges Abrechnungsintervall (erlaubt: MONTHLY, QUARTER, BIANNUAL, ANNUAL)", nil)
			return
		}
		fields := log.Fields{"settlementInterval.new": req.SettlementInterval}
		if err := model.CheckBillingState(req.BillingState); err != nil {
			refuseSettlementInterval(w, r, http.StatusBadRequest, 1060, err.Error(), fields)
			return
		}

		decision, err := h.db.ChangeSettlementInterval(r.Context(), tenant, req.SettlementInterval, claims.Username,
			*req.BillingState, req.DryRun)
		if decision.OldInterval != "" {
			fields["settlementInterval.old"] = decision.OldInterval
		}
		switch {
		case errors.Is(err, database.ErrSettlementIntervalUnchanged):
			refuseSettlementInterval(w, r, http.StatusBadRequest, 1061, "Das Abrechnungsintervall ist bereits eingestellt", fields)
			return
		case errors.Is(err, database.ErrSettlementIntervalNoEeg):
			refuseSettlementInterval(w, r, http.StatusBadRequest, 1001, "Die Gemeinschaft ist nicht angelegt", fields)
			return
		case err != nil:
			// no database text to the client
			log.WithContext(r.Context()).WithFields(fields).WithError(err).
				WithFields(log.Fields{"event.action": "eeg.settlement_interval.failed", "event.outcome": "failure", "code": 1063}).
				Error("settlement interval change failed")
			respondWithHttpError(w, http.StatusInternalServerError, BadProcessError(1063, "Das Abrechnungsintervall konnte nicht geändert werden"))
			return
		case !decision.Allowed && !req.DryRun:
			refuseSettlementInterval(w, r, http.StatusConflict, 1062, decision.Message, fields)
			return
		}

		resp := settlementIntervalResponse{SettlementIntervalDecision: decision}
		if req.DryRun {
			respondWithJSON(w, http.StatusOK, resp)
			return
		}
		effectiveFrom := "" // at once
		if decision.EffectiveFrom != nil {
			effectiveFrom = *decision.EffectiveFrom
		}
		fields["settlementInterval.effectiveFrom"] = effectiveFrom
		log.WithContext(r.Context()).WithFields(fields).
			WithFields(log.Fields{"event.action": "eeg.settlement_interval.changed", "event.outcome": "success"}).
			Info("settlement interval changed")
		if resp.Eeg, err = h.db.GetEegById(r.Context(), tenant); err != nil {
			// the change is committed: 200 with the decision and without the EEG, not an error
			log.WithContext(r.Context()).WithError(err).
				WithFields(log.Fields{"event.action": "eeg.settlement_interval.reload_failed", "event.outcome": "failure"}).
				Warn("settlement interval changed, reading the EEG back failed")
			resp.Message += " Die Gemeinschaft konnte danach nicht neu geladen werden, bitte die Seite neu laden."
		}
		respondWithJSON(w, http.StatusOK, resp)
	}
}

// refuseSettlementInterval answers a refused change with status and code and logs it once with
// eeg.settlement_interval.refused and the reason: as a warning for invalid input (400), as info for a change the
// rule blocks (409). Not as an error: the service did nothing wrong (eegfaktura-dev#54 #12).
func refuseSettlementInterval(w http.ResponseWriter, r *http.Request, status, code int, message string, fields log.Fields) {
	level := log.WarnLevel
	if status == http.StatusConflict {
		level = log.InfoLevel
	}
	log.WithContext(r.Context()).WithFields(fields).WithFields(log.Fields{"event.action": "eeg.settlement_interval.refused",
		"event.outcome": "failure", "event.reason": message, "code": code}).Log(level, "settlement interval change refused")
	respondWithHttpError(w, status, BadProcessError(code, message))
}
