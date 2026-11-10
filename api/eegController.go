package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

type EegHandler struct {
	db database.Database
}

func NewEegHandler(db database.Database) *EegHandler {
	return &EegHandler{db: db}
}

func InitEegRouter(r *mux.Router, db database.Database) *mux.Router {
	h := NewEegHandler(db)
	s := r.PathPrefix("/eeg").Subrouter()

	s.HandleFunc("", middleware.ConditionProtect(h.getEEG(), h.getEEG())).Methods("GET")
	s.HandleFunc("", middleware.Protect(h.updateEEG())).Methods("POST")
	s.HandleFunc("/settlementinterval", middleware.Protect(h.changeSettlementInterval())).Methods("POST")
	s.HandleFunc("/tariff", middleware.Protect(h.getTariff())).Methods("GET")
	s.HandleFunc("/tariff", middleware.Protect(h.addTariff())).Methods("POST")
	s.HandleFunc("/tariff/{id}", middleware.Protect(h.fetchTariffHistory())).Methods("GET")
	s.HandleFunc("/tariff/{id}", middleware.Protect(h.archiveTariff())).Methods("DELETE")
	s.HandleFunc("/sync/participants/{oid}", middleware.Protect(h.syncParticipantsEda())).Methods("POST")
	s.HandleFunc("/import/masterdata", middleware.Protect(h.uploadMasterData())).Methods("POST")
	// POST carries the optional joiningFeeState list from billing (konzept-beitrittsgebuehr.md); GET stays for older web
	s.HandleFunc("/export/masterdata", middleware.Protect(h.exportMasterData())).Methods("GET", "POST")
	s.HandleFunc("/notifications/{id}", middleware.Protect(h.notifications())).Methods("GET")
	s.HandleFunc("/gridoperators", middleware.Protect(h.gridOperators())).Methods("GET")
	s.HandleFunc("/user/get-user", middleware.Protect(h.getUser())).Methods("GET")
	initMailTemplateRoutes(s, db)

	//_ = InitUserRouter(s)

	return r
}

func (h *EegHandler) getEEG() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		log.WithContext(r.Context()).Info("Query EEG")

		var eeg *model.Eeg
		var err error
		if claims.AccessGroups.IsAdmin() {
			eeg, err = h.db.GetEegById(r.Context(), tenant)
		} else {
			eeg, err = h.db.GetEegByIdForUser(r.Context(), tenant)
		}

		if err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrGetEeg(err))
			return
		}

		if eeg == nil {
			respondWithHttpError(w, http.StatusBadRequest, BadProcessError(1001, fmt.Sprintf("EEG %s is not existing yet!", tenant)))
			return
		}
		respondWithJSON(w, http.StatusOK, eeg)
	}
}

func (h *EegHandler) updateEEG() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		var e map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&e)
		if err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		if err = h.db.UpdateEegPartial(r.Context(), tenant, e); err != nil {
			respondWithHttpError(w, http.StatusBadRequest, BadProcessError(1002, model.UserText(err)))
			return
		}
		eeg, err := h.db.GetEegById(r.Context(), tenant)
		if err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrGetEeg(err))
			return
		}
		respondWithJSON(w, http.StatusOK, eeg)
	}
}

func (h *EegHandler) getTariff() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		tariff, err := h.db.GetTariff(r.Context(), tenant)
		if err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}
		respondWithJSON(w, http.StatusOK, tariff)
	}
}

func (h *EegHandler) addTariff() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		// Try to decode the request body into the struct. If there is an error,
		// respond to the client with the error message and a 400 status code.
		var t model.Tariff
		err := json.NewDecoder(r.Body).Decode(&t)
		if err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}
		log.WithContext(r.Context()).Printf("ADD TARIF: %s (%s)", t.Name, t.Id)

		// ZVT: serverseitige Plausibilitaetspruefung der Zeitfenster
		if err = t.ValidateTimeTariff(); err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrUpdateTariff(err))
			return
		}
		if err = t.ValidateJoiningFee(); err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrUpdateTariff(err))
			return
		}

		if err = h.db.AddTariff(r.Context(), tenant, claims.Username, &t); err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}
		respondWithJSON(w, http.StatusCreated, t)
	}
}

func (h *EegHandler) fetchTariffHistory() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		idStr := vars["id"]

		var data []model.Tariff
		var err error
		if data, err = h.db.GetTariffHistory(r.Context(), tenant, idStr); err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}
		respondWithJSON(w, http.StatusOK, data)
	}
}

func (h *EegHandler) archiveTariff() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		idStr := vars["id"]

		if err := h.db.ArchiveTariff(tenant, idStr); err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}
		respondWithJSON(w, http.StatusAccepted, map[string]interface{}{"status": "ok"})
	}
}

func (h *EegHandler) syncParticipantsEda() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		operatorId := vars["oid"]

		eeg, err := h.db.GetEegById(r.Context(), tenant)
		if err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrGetEeg(err))
			return
		}

		day := time.Now()
		from := time.Date(day.Year(), day.Month(), day.Day()-1, 0, 0, 0, 0, day.Location()).UnixMilli()
		to := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location()).UnixMilli()

		var operators []string
		if model.HasMultipleGridOperators(eeg.Area) {
			if operators, err = h.db.MeteringPointGridOperators(r.Context(), tenant); err != nil {
				respondWith(r.Context(), w, http.StatusInternalServerError, tenant, model.ErrFindMeter(err))
				return
			}
		}
		// an error means the receiver is missing or not one of the community: nothing sent (eegfaktura-dev#56 #7)
		if err = mqttclient.RequestingMeteringPointList(eeg, operatorId, operators, from, to); err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}
		respondWithStatus(w, http.StatusNoContent)
	}
}

func (h *EegHandler) uploadMasterData() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		// Parse our multipart form, 10 << 20 specifies a maximum
		// upload of 10 MB files.
		var err error = r.ParseMultipartForm(10 << 20)
		if err != nil {
			respondWithHttpError(w, http.StatusBadRequest, BadProcessError(1050, err.Error()))
			return
		}

		sheet := r.FormValue("sheet")

		file, handler, err := r.FormFile("masterdatafile")
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).Error(err)
			respondWithHttpError(w, http.StatusBadRequest, BadProcessError(1051, err.Error()))
			return
		}
		defer func() { _ = file.Close() }()

		log.WithContext(r.Context()).Infof("--- Upload File: sheet %s\n", sheet)

		if err = h.db.ImportMasterdataFromExcel(r.Context(), file, handler.Filename, sheet, tenant); err != nil {
			// a wrong sheet name: the German text for the user instead of "Bad Process"
			var ve *model.VfeegError
			if errors.As(err, &ve) && ve.Code == model.ErrImportSheetMissing(nil).Code {
				respondWith(r.Context(), w, http.StatusBadRequest, tenant, ve)
				return
			}
			log.WithContext(r.Context()).WithField("tenant", tenant).Error(err)
			respondWithHttpError(w, http.StatusBadRequest, BadProcessError(1052, err.Error()))
		} else {
			log.WithContext(r.Context()).Info("Import File successful")
			respondWithStatus(w, http.StatusNoContent)
		}
	}
}

// maxJoiningFeeStateBytes bounds the list web posts with the member export (about 200 bytes per member).
const maxJoiningFeeStateBytes = 8 << 20

func (h *EegHandler) exportMasterData() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		// optional body: billing's joiningFeeState list, passed on by web; no body = status column stays empty
		var feeStates []model.JoiningFeeState
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJoiningFeeStateBytes)).Decode(&feeStates); err != nil && !errors.Is(err, io.EOF) {
				respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
				return
			}
		}

		eeg, err := h.db.GetEegById(r.Context(), tenant)
		if err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrGetEeg(err))
			return
		}

		participants, err := h.db.GetParticipants(r.Context(), tenant)
		if err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}

		tariffMap, err := h.db.GetTariffNameMap(r.Context(), tenant)
		if err != nil {
			respondWithHttpError(w, http.StatusBadRequest, BadProcessError(1059, err.Error()))
			return
		}

		tariffs, err := h.db.GetTariff(r.Context(), tenant)
		if err != nil {
			respondWithHttpError(w, http.StatusBadRequest, BadProcessError(1059, err.Error()))
			return
		}
		joiningFees := map[string]float64{}
		for _, t := range tariffs {
			if t.Type == model.EEG && t.JoiningFee > 0 {
				joiningFees[t.Id.String()] = t.JoiningFee
			}
		}

		b, err := database.ExportMasterdataToExcel(r.Context(), participants, eeg, tariffMap, joiningFees, feeStates)

		if err != nil {
			log.WithContext(r.Context()).Errorf("Create Energy Export: %v", err.Error())
			respondWithHttpError(w, http.StatusInternalServerError, BadProcessError(1051, err.Error()))
			return
		}
		filename := fmt.Sprintf("%s-EEG-Masterdata-%s",
			tenant,
			time.Now().Format("20060102"),
		)

		w.Header().Set("Content-type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.xlsx"`, filename))
		w.Header().Set("filename", filename)

		if _, err := b.WriteTo(w); err != nil {
			fmt.Fprintf(w, "%s", err)
		}
	}
}

func (h *EegHandler) notifications() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		idStr := vars["id"]

		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			respondWithHttpError(w, http.StatusBadRequest, BadProcessError(1054, err.Error()))
			return
		}

		isAdmin := func() bool {
			for _, a := range claims.AccessGroups {
				if a == "/EEG_ADMIN" {
					return true
				}
			}
			return false
		}

		notifications, err := h.db.GetNotification(r.Context(), tenant, id, isAdmin())
		if err != nil {
			respondWithHttpError(w, http.StatusBadRequest, BadProcessError(1055, err.Error()))
			return
		}
		respondWithJSON(w, http.StatusOK, notifications)
	}
}

func (h *EegHandler) gridOperators() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		o, err := h.db.GetGridOperators(r.Context())
		if err != nil {
			respondWithHttpError(w, http.StatusBadRequest, BadProcessError(1055, err.Error()))
			return
		}
		respondWithJSON(w, http.StatusOK, o)
	}
}

func (h *EegHandler) getUser() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		var tenants []string
		su := middleware.IsSuperuser(claims.RealmAccess.Roles)
		if !su {
			tenants = claims.Tenants
		}

		tn, err := h.db.FetchTenantsName(r.Context(), tenants, su)
		if err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrGetUser(err))
			return
		}
		respondWithJSON(w, http.StatusOK, tn)
	}
}
