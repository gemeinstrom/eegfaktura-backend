package api

import (
	"encoding/json"
	"net/http"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"at.ourproject/vfeeg-backend/util"
	"github.com/gorilla/mux"
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
				log.WithField("tenant", tenant).WithError(err).Error("failed to fetch participant.")
				respondWith(w, http.StatusBadRequest, tenant, err)
				return
			}
			respondWithData(w, 200, participant)
		}
	}
}

func (h *ParticipantHandler) fetchParticipant() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		participant, err := h.db.GetParticipantByName(r.Context(), tenant, claims.Email)
		if err != nil {
			log.WithField("tenant", tenant).WithError(err).Error("failed to fetch participant.")
			respondWith(w, http.StatusBadRequest, tenant, err)
			return
		}
		respondWithData(w, 200, participant)
	}
}

func (h *ParticipantHandler) updateParticipant() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		//vars := mux.Vars(r)
		//participantId := vars["id"]

		var t model.EegParticipant
		err := json.NewDecoder(r.Body).Decode(&t)
		if err != nil {
			log.WithField("tenant", tenant).WithError(err).Error("failed to update participant.")
			respondWith(w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		err = h.db.UpdateParticipant(r.Context(), tenant, claims.Username, &t)
		if err != nil {
			log.WithField("tenant", tenant).WithError(err).Error("failed to update participant.")
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		respondWithData(w, http.StatusAccepted, t)
	}
}

func (h *ParticipantHandler) updateParticipantPartial() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		participantId := vars["id"]

		var p map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&p)
		if err != nil {
			log.WithField("tenant", tenant).WithError(err).Error("failed to update partial participant.")
			respondWith(w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		name := p["path"].(string)
		value := p["value"]

		err = h.db.UpdateParticipantPartial(r.Context(), tenant, participantId, name, value)
		if err != nil {
			log.WithField("tenant", tenant).WithError(err).Error("failed to update partial participant.")
			respondWith(w, http.StatusInternalServerError, tenant, err)
			return
		}

		participant, err := h.db.QueryParticipant(r.Context(), tenant, participantId)
		if err != nil {
			log.WithField("tenant", tenant).WithError(err).Error("failed to update partial participant.")
			respondWith(w, http.StatusBadRequest, tenant, err)
			return
		}
		respondWithData(w, http.StatusAccepted, participant)
	}
}

func (h *ParticipantHandler) registerParticipant() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		var t model.EegParticipant
		err := json.NewDecoder(r.Body).Decode(&t)
		if err != nil {
			log.WithField("tenant", tenant).WithError(err).Error("failed to register participant.")
			respondWith(w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		err = h.db.RegisterParticipant(r.Context(), tenant, claims.Username, &t)
		if err != nil {
			log.WithField("tenant", tenant).WithError(err).Error("failed to register participant.")
			respondWith(w, http.StatusBadRequest, tenant, err)
			return
		}
		respondWithData(w, http.StatusCreated, t)
	}
}

func (h *ParticipantHandler) confirmParticipant() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {

		vars := mux.Vars(r)
		participantId := vars["id"]

		var meters []*model.MeteringPoint

		err := json.NewDecoder(r.Body).Decode(&meters)
		if err != nil {
			log.WithField("tenant", tenant).WithError(err).Error("failed to confirm participant.")
			respondWith(w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		eeg, err := h.db.GetEegById(r.Context(), tenant)
		if err != nil {
			log.WithField("tenant", tenant).WithError(err).Error("failed to confirm participant.")
			respondWith(w, http.StatusBadRequest, tenant, model.ErrGetEeg(err))
			return
		}
		participant, err := h.db.QueryParticipant(r.Context(), tenant, participantId)
		if err != nil {
			log.WithField("tenant", tenant).WithError(err).Error("failed to confirm participant.")
			respondWith(w, http.StatusBadRequest, tenant, err)
			return
		}

		if err = h.db.ConfirmParticipant(r.Context(), tenant, claims.Username, participantId); err != nil {
			log.WithField("tenant", tenant).WithError(err).Error("failed to confirm participant.")
			respondWith(w, http.StatusBadRequest, tenant, err)
			return
		}
		participant.Status = model.ACTIVE

		participantMetersMap := make(map[string]*model.MeteringPoint)
		for _, meter := range participant.MeteringPoint {
			participantMetersMap[meter.MeteringPoint] = meter
		}

		if eeg.Online {
			for _, qm := range meters {
				if m, ok := participantMetersMap[qm.MeteringPoint]; ok {
					m.ActivationMode = qm.ActivationMode
					m.ActivationCode = qm.ActivationCode
					var from int64
					if qm.RegisteredSince.After(participant.ParticipantSince.Date) {
						from = util.CalcProcessDate(qm.RegisteredSince)
					} else {
						from = util.CalcProcessNullDate(participant.ParticipantSince)
					}

					log.WithField("tenant", tenant).Infof("Start Meteringpoint %s registration - Active at: %+v", m.MeteringPoint, from)
					if m.ActivationMode == model.ONLINE {
						if err = mqttclient.RegistrationForParticipation(eeg, m, &from); err != nil {
							log.WithField("tenant", tenant).WithError(err).Error("failed to confirm participant.")
							respondWith(w, http.StatusBadRequest, tenant, err)
							return
						}
					} else {
						if err = mqttclient.OfflineRegistrationForParticipation(eeg, m, &from); err != nil {
							log.WithField("tenant", tenant).WithError(err).Error("failed to confirm participant.")
							respondWith(w, http.StatusBadRequest, tenant, err)
							return
						}
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
				log.WithField("tenant", tenant).WithError(err).Error("failed to confirm participant.")
				respondWith(w, http.StatusBadRequest, tenant, err)
				return
			}
		}
		respondWithData(w, http.StatusCreated, participant)
	}
}

func (h *ParticipantHandler) deleteParticipant() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		idStr := vars["id"]

		if err := h.db.DeleteParticipant(r.Context(), tenant, idStr); err != nil {
			log.WithField("tenant", tenant).WithError(err).Error("failed to delete participant.")
			respondWith(w, http.StatusBadRequest, tenant, err)
			return
		}
		respondWithData(w, http.StatusAccepted, map[string]interface{}{"id": idStr})
	}
}
