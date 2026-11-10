package api

import (
	"encoding/json"
	"net/http"
	"time"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

func InitApiRouter(r *mux.Router, db database.Database) *mux.Router {
	h := NewApiHandler(db)
	s := r.PathPrefix("/master").Subrouter()

	s.HandleFunc("/updatepartfact", middleware.ProtectApi(h.updateParticipantFactorAPI())).Methods("POST")
	s.HandleFunc("/masterdata", middleware.ProtectApi(h.fetchMasterDataAPI())).Methods("GET")
	return r
}

type ApiHandler struct {
	db database.Database
}

func NewApiHandler(db database.Database) *ApiHandler {
	return &ApiHandler{db: db}
}

func (h *ApiHandler) updateParticipantFactorAPI() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		var request struct {
			MeteringPoints []*model.ChangePartitionFactorRequest `json:"meteringPoints"`
		}

		err := json.NewDecoder(r.Body).Decode(&request)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to request metering point PRTFACT")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		if request.MeteringPoints == nil || len(request.MeteringPoints) == 0 {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to request metering point PRTFACT")
			respondWithError(w, http.StatusBadRequest, "failed to request metering point PRTFACT")
			return
		}

		if err = assertPartitionFactorMeters(r.Context(), h.db, tenant, request.MeteringPoints); err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}

		eeg, err := h.db.GetEegById(r.Context(), tenant)
		if err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrGetEeg(err))
			return
		}

		if eeg.Online {
			if err = mqttclient.ChangePartitionFactor(eeg, request.MeteringPoints); err != nil {
				log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Errorf("failed to request metering point PRTFACT for %d metering point(s)", len(request.MeteringPoints))
				respondWith(r.Context(), w, http.StatusInternalServerError, tenant, err)
				return
			}
		} else {
			log.WithContext(r.Context()).WithField("tenant", tenant).Warnf("Offline EEG want to change partitions of %d metering point(s)", len(request.MeteringPoints))
			respondWithStatus(w, http.StatusNotFound)
			return
		}
		respondWithStatus(w, http.StatusCreated)
	}
}

func (h *ApiHandler) fetchMasterDataAPI() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {

		participants, err := h.db.GetParticipants(r.Context(), tenant)
		if err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}

		masterdata := make([]model.MasterDataParticipant, len(participants))
		for i := range participants {
			masterdata[i].FirstName = participants[i].FirstName
			masterdata[i].LastName = participants[i].LastName
			masterdata[i].TitleAfter = participants[i].TitleAfter.String
			masterdata[i].TitleBefore = participants[i].TitleBefore.String
			masterdata[i].ParticipantSince = time.Unix(participants[i].ParticipantSince.Date.Unix(), 0)
			masterdata[i].ParticipantNumber = participants[i].ParticipantNumber.String
			masterdata[i].Status = participants[i].Status
			masterdata[i].MeteringPoint = make([]model.MasterDataMeter, len(participants[i].MeteringPoint))
			for j := range participants[i].MeteringPoint {
				masterdata[i].MeteringPoint[j] = model.MasterDataMeter{
					MeteringPoint:    participants[i].MeteringPoint[j].MeteringPoint,
					ConsentId:        participants[i].MeteringPoint[j].ConsentId.String,
					Direction:        participants[i].MeteringPoint[j].Direction,
					Status:           participants[i].MeteringPoint[j].Status,
					EquipmentNumber:  participants[i].MeteringPoint[j].EquipmentNumber.String,
					EquipmentName:    participants[i].MeteringPoint[j].EquipmentName.String,
					InverterId:       participants[i].MeteringPoint[j].InverterId.String,
					RegisteredSince:  participants[i].MeteringPoint[j].RegisteredSince.String(),
					GridOperatorId:   participants[i].MeteringPoint[j].GridOperatorId.String,
					GridOperatorName: participants[i].MeteringPoint[j].GridOperatorName.String,
					ActiveSince:      time.Unix(participants[i].MeteringPoint[j].State.ActiveSince.Date.Unix(), 0),
					InactiveSince:    time.Unix(participants[i].MeteringPoint[j].State.InactiveSince.Date.Unix(), 0),
					PartFact:         participants[i].MeteringPoint[j].PartFact,
					AllocationFactor: participants[i].MeteringPoint[j].AllocationFactor.Float64,
				}
			}
		}
		respondWithJSON(w, http.StatusOK, masterdata)
	}
}
