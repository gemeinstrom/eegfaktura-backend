package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"at.ourproject/vfeeg-backend/util"
	"github.com/gorilla/mux"
	"github.com/pborman/uuid"
	log "github.com/sirupsen/logrus"
)

func InitParticipantRouter(r *mux.Router, db database.Database) *mux.Router {
	h := NewParticipantHandler(db)
	s := r.PathPrefix("/participant").Subrouter()

	s.HandleFunc("", middleware.ConditionProtect(h.fetchParticipantAll(), h.fetchParticipant())).Methods("GET")
	s.HandleFunc("", middleware.Protect(h.registerParticipant())).Methods("POST")
	s.HandleFunc("/{id}", middleware.Protect(h.updateParticipant())).Methods("PUT")
	// Commit a participant to be a member of a EEG
	s.HandleFunc("/{id}/confirm", middleware.Protect(h.confirmParticipant())).Methods("POST")
	s.HandleFunc("/v2/{id}", middleware.Protect(h.updateParticipantPartial())).Methods("PUT")
	s.HandleFunc("/v2/{id}", middleware.Protect(h.deleteParticipant())).Methods("DELETE")

	return r
}

type ParticipantHandler struct {
	db database.Database
}

func NewParticipantHandler(db database.Database) *ParticipantHandler {
	return &ParticipantHandler{db: db}
}

func (h *ParticipantHandler) fetchParticipantAll() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		if claims.AccessGroups.IsAdmin() {
			participant, err := h.db.GetParticipants(r.Context(), tenant)
			if err != nil {
				log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to fetch participant.")
				respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
				return
			}
			respondWithJSON(w, 200, participant)
		}
	}
}

func (h *ParticipantHandler) fetchParticipant() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		participant, err := h.db.GetParticipantByName(r.Context(), tenant, claims.Email)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to fetch participant.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}
		respondWithJSON(w, 200, participant)
	}
}

func (h *ParticipantHandler) updateParticipant() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		participantId := uuid.Parse(mux.Vars(r)["id"])

		var t model.EegParticipant
		err := json.NewDecoder(r.Body).Decode(&t)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update participant.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}
		// The path names the member that is updated; a body for another member is refused.
		if participantId == nil || !uuid.Equal(participantId, t.Id) {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant,
				model.ErrUpdateParticipant(errors.New("participant id in path and body differ")))
			return
		}

		err = h.db.UpdateParticipant(r.Context(), tenant, claims.Username, &t)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update participant.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, participantWriteError(err))
			return
		}
		respondWithJSON(w, http.StatusAccepted, t)
	}
}

// participantWriteError answers a failed member update without database or rollback
// text: "not found" (unknown or foreign id) and invalid input keep their code and
// message, anything else gets a generic message. The full error is logged.
func participantWriteError(err error) *model.VfeegError {
	return participantError(err, model.ErrUpdateParticipant(errors.New("participant could not be updated")))
}

// participantRegisterError is participantWriteError for a failed registration.
func participantRegisterError(err error) *model.VfeegError {
	return participantError(err, model.ErrRegisterParticipant(errors.New("participant could not be registered")))
}

func participantError(err error, generic *model.VfeegError) *model.VfeegError {
	var ve *model.VfeegError
	if errors.As(err, &ve) {
		// invalid input with a text for the user; 1207 and 1112 come from the registration only
		switch ve.Code {
		case model.ErrFindParticipant(nil).Code, model.ErrParseJson(nil).Code, model.ErrInvalidParticipantEmail(nil).Code,
			model.ErrMeterActiveAtOtherParticipant(nil).Code, model.ErrDebitWithoutIban(nil).Code:
			return ve
		}
	}
	return generic
}

func (h *ParticipantHandler) updateParticipantPartial() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		participantId := vars["id"]

		var p map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&p)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update partial participant.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		name, ok := p["path"].(string)
		if !ok {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(errors.New("missing path")))
			return
		}
		// Reject unknown or write-protected paths so a caller-controlled field
		// name never reaches the SQL builder verbatim.
		if !model.IsAllowedParticipantUpdatePath(name) {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrUpdateParticipant(errors.New("field not updatable")))
			return
		}
		value := p["value"]

		err = h.db.UpdateParticipantPartial(r.Context(), tenant, participantId, name, value)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update partial participant.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, participantWriteError(err))
			return
		}

		participant, err := h.db.QueryParticipant(r.Context(), tenant, participantId)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update partial participant.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}
		respondWithJSON(w, http.StatusAccepted, participant)
	}
}

func (h *ParticipantHandler) registerParticipant() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		var t model.EegParticipant
		err := json.NewDecoder(r.Body).Decode(&t)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to register participant.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		err = h.db.RegisterParticipant(r.Context(), tenant, claims.Username, &t)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to register participant.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, participantRegisterError(err))
			return
		}
		respondWithJSON(w, http.StatusCreated, t)
	}
}

func (h *ParticipantHandler) confirmParticipant() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {

		vars := mux.Vars(r)
		participantId := vars["id"]

		var meters []*model.MeteringPoint

		err := json.NewDecoder(r.Body).Decode(&meters)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to confirm participant.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}
		for _, qm := range meters {
			if qm == nil {
				respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(errors.New("null metering point in request")))
				return
			}
		}

		eeg, err := h.db.GetEegById(r.Context(), tenant)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to confirm participant.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrGetEeg(err))
			return
		}
		// only an online EEG sends registrations; check every meter before the participant is confirmed
		if eeg.Online {
			for _, qm := range meters {
				if err = checkActivationMode(qm.ActivationMode); err != nil {
					respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
					return
				}
			}
		}
		participant, err := h.db.QueryParticipant(r.Context(), tenant, participantId)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to confirm participant.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}

		participantMetersMap := make(map[string]*model.MeteringPoint)
		for _, meter := range participant.MeteringPoint {
			participantMetersMap[meter.MeteringPoint] = meter
		}
		// every registration needs a grid operator: check all before anything is written or sent
		if eeg.Online {
			for _, qm := range meters {
				if m, ok := participantMetersMap[qm.MeteringPoint]; ok {
					if _, err = mqttclient.ReceiverFor(eeg, m); err != nil {
						respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
						return
					}
				}
			}
		}

		if err = h.db.ConfirmParticipant(r.Context(), tenant, claims.Username, participantId); err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to confirm participant.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}
		participant.Status = model.ACTIVE

		if eeg.Online {
			for _, qm := range meters {
				if m, ok := participantMetersMap[qm.MeteringPoint]; ok {
					var from int64
					if qm.RegisteredSince.After(participant.ParticipantSince.Date) {
						from = util.CalcProcessDate(qm.RegisteredSince)
					} else {
						from = util.CalcProcessNullDate(participant.ParticipantSince)
					}

					log.WithContext(r.Context()).WithField("tenant", tenant).Infof("Start Meteringpoint %s registration - Active at: %+v", m.MeteringPoint, from)
					if err = mqttclient.RegistrationForParticipation(eeg, m, &from); err != nil {
						log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to confirm participant.")
						respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
						return
					}
				}
			}
		} else {
			meterIds := []string{}
			for _, m := range participant.MeteringPoint {
				meterIds = append(meterIds, m.MeteringPoint)
				m.Status = model.S_ACTIVE
			}
			err := h.db.MeteringPointsSetStatus(r.Context(), tenant, model.ACTIVE, nil, meterIds, nil, nil)
			if err != nil {
				log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to confirm participant.")
				respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
				return
			}
		}
		respondWithJSON(w, http.StatusCreated, participant)
	}
}

func (h *ParticipantHandler) deleteParticipant() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		idStr := vars["id"]

		if err := h.db.DeleteParticipant(r.Context(), tenant, idStr); err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to delete participant.")
			status := http.StatusBadRequest
			var ve *model.VfeegError
			if errors.As(err, &ve) && ve.Code == model.ErrParticipantHasActivatedMeter(nil).Code {
				status = http.StatusConflict
			}
			respondWith(r.Context(), w, status, tenant, err)
			return
		}
		respondWithJSON(w, http.StatusAccepted, map[string]interface{}{"id": idStr})
	}
}
