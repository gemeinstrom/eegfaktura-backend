package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/energyrequest"
	"at.ourproject/vfeeg-backend/model"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"at.ourproject/vfeeg-backend/util"
	"github.com/gorilla/mux"
	"github.com/jjeffery/civil"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

func InitMeteringRouter(r *mux.Router, db database.Database) *mux.Router {
	h := NewMeteringHandler(db)
	s := r.PathPrefix("/meteringpoint").Subrouter()

	s.HandleFunc("/{pid}/update/{mid}", middleware.Protect(h.updateMeteringPoint())).Methods("PUT")
	s.HandleFunc("/v2/{pid}/update/{mid}", middleware.Protect(h.updateMeteringPointPartial())).Methods("PUT")
	s.HandleFunc("/v2/{pid}/updateid/{mid}", middleware.Protect(h.updateMeteringPointId())).Methods("PUT")
	s.HandleFunc("/{pid}/update/{mid}/partfact", middleware.Protect(h.updateMeteringPointPartFact())).Methods("PUT")
	s.HandleFunc("/{spid}/{dpid}/move/{mid}", middleware.Protect(h.moveMeteringPoint())).Methods("PUT")
	s.HandleFunc("/{pid}/remove/{mid}", middleware.Protect(h.removeMeteringPoint())).Methods("DELETE")
	s.HandleFunc("/{pid}/archive/{mid}", middleware.Protect(h.archiveMeteringPoint())).Methods("PUT")
	s.HandleFunc("/{pid}/create", middleware.Protect(h.createMeteringPoint())).Methods("PUT")
	s.HandleFunc("/{pid}/register", middleware.Protect(h.registerMeteringPoint())).Methods("POST")
	s.HandleFunc("/{pid}/revokemeters", middleware.Protect(h.requestRevokeMeteringPoint())).Methods("POST")
	s.HandleFunc("/syncenergy", middleware.Protect(h.requestMeteringPointValues())).Methods("POST")
	s.HandleFunc("/changepartitionfactor", middleware.Protect(h.requestChangePartitionFactor())).Methods("POST")

	return r
}

type MeteringHandler struct {
	db database.Database
}

func NewMeteringHandler(db database.Database) *MeteringHandler {
	return &MeteringHandler{db: db}
}

func (h *MeteringHandler) createMeteringPoint() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		participantId := vars["pid"]

		var m model.MeteringPoint
		err := json.NewDecoder(r.Body).Decode(&m)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to register metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}
		// The mode only matters where a registration is sent below: a NEW meter (empty becomes NEW on insert) of an
		// active member of an online EEG. There it is rejected before the meter is saved; elsewhere it is ignored.
		if modeErr := checkActivationMode(m.ActivationMode); modeErr != nil && (m.ProcessState == "" || m.ProcessState == model.NEW) {
			eeg, err := h.db.GetEegById(r.Context(), tenant)
			if err != nil {
				respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrGetEeg(err))
				return
			}
			participant, err := h.db.QueryParticipant(r.Context(), tenant, participantId)
			if err != nil {
				log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to register metering point. Cannot find appropriate participant.")
				respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
				return
			}
			if eeg.Online && participant.Status == model.ACTIVE {
				respondWith(r.Context(), w, http.StatusBadRequest, tenant, modeErr)
				return
			}
		}

		m.ModifiedAt = civil.Now()
		if m.Status != model.S_ACTIVE {
			m.RegisteredSince = civil.Today()
		}
		m.ModifiedBy = null.StringFrom(claims.Username)

		err = h.db.RegisterMeteringPoint(r.Context(), tenant, claims.Username, participantId, &m)
		if err != nil {
			// already at this member (platform#247) or active at another member: 409 with the code and a German
			// text instead of 1111; a user error, so a warning in the log, not an error
			var ve *model.VfeegError
			if errors.As(err, &ve) && (ve.Code == model.ErrMeterExistsAtParticipant(nil).Code ||
				ve.Code == model.ErrMeterActiveAtOtherParticipant(nil).Code) {
				log.WithContext(r.Context()).WithField("tenant", tenant).WithField("code", ve.Code).
					Warn("metering point already at this or another member")
				text := model.UserText(ve)
				respondWithHttpError(w, http.StatusConflict, &HttpError{Error: text, Code: ve.Code, Message: text})
				return
			}
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to register metering point.")
			respondWithHttpError(w, http.StatusBadRequest, BadProcessError(1111, err.Error()))
			return
		}

		if m.ProcessState == model.NEW {
			log.WithContext(r.Context()).WithField("tenant", tenant).Infof("register Meter:  %s ", m.MeteringPoint)
			eeg, err := h.db.GetEegById(r.Context(), tenant)
			if err != nil {
				respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrGetEeg(err))
				return
			}

			participant, err := h.db.QueryParticipant(r.Context(), tenant, participantId)
			if err != nil {
				log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to register metering point. Cannot find appropriate participant.")
				respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
				return
			}

			if participant.Status == model.ACTIVE {
				var from int64
				if m.RegisteredSince.After(participant.ParticipantSince.Date) {
					from = util.CalcProcessDate(m.RegisteredSince)
				} else {
					from = util.CalcProcessNullDate(participant.ParticipantSince)
				}

				if err = edaRegisterMeteringpoint(eeg, &m, &from); err != nil {
					respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
					return
				}
			}
		}
		respondWithJSON(w, http.StatusCreated, m)
	}
}

func (h *MeteringHandler) updateMeteringPoint() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		participantId := vars["pid"]
		meterId := vars["mid"]

		m := model.MeteringPoint{}
		err := json.NewDecoder(r.Body).Decode(&m)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		m.ModifiedAt = civil.Now()
		m.ModifiedBy = null.StringFrom(claims.Username)
		err = h.db.UpdateMeteringPoint(r.Context(), tenant, claims.Username, participantId, meterId, &m)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}
		respondWithJSON(w, http.StatusAccepted, m)
	}
}

func (h *MeteringHandler) updateMeteringPointPartial() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		pId := vars["pid"]
		mId := vars["mid"]

		var v map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&v)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update partial participant.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		name, ok := v["path"].(string)
		if !ok {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrUpdateMeter(errors.New("missing path")))
			return
		}
		// Resolve the client-supplied field name to a real, updatable column;
		// reject anything unknown so it can never reach the SQL builder verbatim.
		column, ok := model.AllowedUpdateColumn(model.MeteringPoint{}, name)
		// The grid operator is derived from the metering point number (platform#107).
		if column == "grid_operator_id" || column == "grid_operator_name" {
			ok = false
		}
		if !ok {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrUpdateMeter(errors.New("field not updatable")))
			return
		}
		value := v["value"]

		if err := h.db.UpdateMeteringPointPartial(r.Context(), tenant, claims.Username, pId, mId, map[string]interface{}{column: value}); err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrUpdateMeter(err))
			return
		}

		updatedMeter, err := h.db.FindAssignedMeteringById(r.Context(), tenant, mId)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrUpdateMeter(err))
			return
		}
		respondWithJSON(w, http.StatusAccepted, updatedMeter)
	}
}

func (h *MeteringHandler) updateMeteringPointId() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		pId := vars["pid"]
		mId := vars["mid"]

		var v map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&v)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update partial participant.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		value, ok := v["newId"].(string)
		if !ok || value == "" {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrUpdateMeter(errors.New("newId is missing")))
			return
		}

		if err := h.db.UpdateMeteringPointPartial(r.Context(), tenant, claims.Username, pId, mId, map[string]interface{}{"metering_point_id": value}); err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrUpdateMeter(err))
			return
		}

		updatedMeter, err := h.db.FindAssignedMeteringById(r.Context(), tenant, value)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrUpdateMeter(err))
			return
		}
		respondWithJSON(w, http.StatusAccepted, updatedMeter)
	}
}

func (h *MeteringHandler) updateMeteringPointPartFact() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		participantId := vars["pid"]
		meterId := vars["mid"]

		pf := struct {
			PartFact int `json:"partFact"`
		}{}
		err := json.NewDecoder(r.Body).Decode(&pf)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update metering point partition factor.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		ms, err := h.db.FindAllMeteringByTenant(r.Context(), tenant, participantId, []string{meterId})
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update metering point partition factor.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}

		var meters []model.Meter
		for _, m := range ms {
			meters = append(meters, model.Meter{
				MeteringPoint: m.MeteringPoint,
				Direction:     m.Direction,
				PartFact:      pf.PartFact,
			})
		}

		if len(meters) == 1 {
			err = h.db.MeteringPointChangePartFactor(r.Context(), tenant, participantId, meters)
			if err != nil {
				log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to update metering point partition factor.")
				respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
				return
			}
			ms[0].PartFact = pf.PartFact
			respondWithJSON(w, http.StatusAccepted, ms[0])
		} else {
			log.WithContext(r.Context()).WithField("tenant", tenant).Errorf("failed to update metering point partition factor. Err: No PRTFACT specified for %s", meterId)
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, &model.VfeegError{Code: 999, Err: errors.New(fmt.Sprintf("No metering factor found N:%d", len(meters)))})
		}
	}
}

func (h *MeteringHandler) moveMeteringPoint() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		sParticipantId := vars["spid"]
		dParticipantId := vars["dpid"]
		meterId := vars["mid"]

		m := model.MeteringPoint{}
		err := json.NewDecoder(r.Body).Decode(&m)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to move metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		m.ModifiedAt = civil.Now()
		m.ModifiedBy = null.StringFrom(claims.Username)
		err = h.db.MoveMeteringPoint(r.Context(), tenant, claims.Username, sParticipantId, dParticipantId, meterId)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to move metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}
		respondWithJSON(w, http.StatusAccepted, m)
	}
}

type registerMeterRequestType struct {
	MeteringPoint  string                 `json:"meteringPoint"`
	Direction      model.DirectionType    `json:"direction"`
	From           int64                  `json:"from"`
	To             int64                  `json:"to"`
	ActivationMode model.RegistrationMode `json:"activationMode,omitempty"`
}

// registerMeteringPoint activates existing meter at the net operator
//
// Here the registration only perform an online EDA communication
func (h *MeteringHandler) registerMeteringPoint() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		participantId := vars["pid"]

		request := registerMeterRequestType{}
		err := json.NewDecoder(r.Body).Decode(&request)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to register metering point")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		eeg, err := h.db.GetEegById(r.Context(), tenant)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to register metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrGetEeg(err))
			return
		}
		// only an online EEG sends a registration; elsewhere the mode is ignored
		if eeg.Online {
			if err = checkActivationMode(request.ActivationMode); err != nil {
				respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
				return
			}
		}
		participant, err := h.db.QueryParticipant(r.Context(), tenant, participantId)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to register metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}

		// Check Meter available in Participant
		var meter *model.MeteringPoint
		for _, p := range participant.MeteringPoint {
			if p.MeteringPoint == request.MeteringPoint {
				meter = p
				break
			}
		}
		if meter == nil {
			respondWith(r.Context(), w, http.StatusNotFound, tenant,
				model.ErrFindMeter(fmt.Errorf("metering point %s not found at participant %s", request.MeteringPoint, participantId)))
			return
		}

		var from int64
		if meter.RegisteredSince.After(participant.ParticipantSince.Date) {
			from = util.CalcProcessDate(meter.RegisteredSince)
		} else {
			from = util.CalcProcessNullDate(participant.ParticipantSince)
		}

		if err = edaRegisterMeteringpoint(eeg, meter, &from); err != nil {
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}

		log.WithContext(r.Context()).WithField("tenant", tenant).Infof("register metering point. PID: %s, meter: %s", participantId, request.MeteringPoint)
		respondWithJSON(w, http.StatusCreated, participant)
	}
}

func (h *MeteringHandler) requestMeteringPointValues() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {

		request := struct {
			MeteringPoints []struct {
				Meter     string              `json:"meter"`
				Direction model.DirectionType `json:"direction"`
				// optional period of this metering point (ms); the period of the request applies without it
				From *int64 `json:"from"`
				To   *int64 `json:"to"`
			} `json:"meteringPoints"`
			From int64 `json:"from"`
			To   int64 `json:"to"`
			// metering points web found complete: recorded as skipped (COMPLETE), not requested
			Complete []string `json:"complete"`
		}{}
		err := json.NewDecoder(r.Body).Decode(&request)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to request metering point values.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		eeg, err := h.db.GetEegById(r.Context(), tenant)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to request metering point values.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrGetEeg(err))
			return
		}

		fromDate := util.TruncateToStartOfDay(time.UnixMilli(request.From)).UnixMilli()
		toDate := util.TruncateToEndOfDay(time.UnixMilli(request.To)).UnixMilli()

		type period struct{ from, to int64 }
		periods := map[string]period{}
		var meters []string
		perMeter := false
		for _, m := range request.MeteringPoints {
			meters = append(meters, m.Meter)
			perMeter = perMeter || m.From != nil || m.To != nil
			p := period{from: fromDate, to: toDate}
			if m.From != nil {
				p.from = util.TruncateToStartOfDay(time.UnixMilli(*m.From)).UnixMilli()
			}
			if m.To != nil {
				p.to = util.TruncateToEndOfDay(time.UnixMilli(*m.To)).UnixMilli()
			}
			if q, ok := periods[m.Meter]; ok {
				p = period{from: min(p.from, q.from), to: max(p.to, q.to)}
			}
			periods[m.Meter] = p
		}

		// a request where web found every metering point complete has only complete
		if meters == nil && len(request.Complete) == 0 {
			log.WithContext(r.Context()).WithField("tenant", tenant).Errorf("Request meter values - no Meter selected")
			respondWith(r.Context(), w, http.StatusInternalServerError, tenant, model.Wrap(errors.New("no Meter selected"), 3100))
			return
		}

		if eeg.Online {
			var errorList []string
			requested := append(append([]string{}, meters...), request.Complete...)
			// no ids would find every metering point of the tenant
			var found []*model.MeteringPoint
			if len(meters) > 0 {
				found, err = h.db.FindMeteringByIds(r.Context(), tenant, meters)
			}
			meters := found
			if err != nil {
				log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to request metering point values.")
				respondWith(r.Context(), w, http.StatusInternalServerError, tenant, err)
				return
			}
			// platform#173: record the run (requested and skipped metering points) before sending, so the answers
			// can be summarised
			run := energyrequest.NewRun(eeg.CommunityId, fromDate, toDate)
			// meteringPoints wins: a metering point listed there too is requested, locked or skipped for its own reason
			for _, c := range request.Complete {
				if _, listed := periods[c]; !listed {
					run.Skip(c, energyrequest.ReasonComplete)
				}
			}
			// the static checks first: the lock is taken and the rows are read only when a metering point is left
			type energyRequest struct {
				meter    *model.MeteringPoint
				from, to int64
				skip     string // the reason of a static check, empty for a candidate
			}
			var checked []energyRequest
			candidates := 0
			for _, m := range meters {
				switch {
				// the old member's entry after an owner change (moved) is requested for its own period too (user
				// decision 2026-10-24: the billing needs all current data); an archived entry is not, also one still
				// in INIT, so its reason never decides over the current entry's
				case m.State.Flag != model.F_ASSIGNED && m.State.Flag != model.F_MOVED:
					checked = append(checked, energyRequest{meter: m, skip: energyrequest.ReasonNotAssigned})
					continue
				case m.Status == model.S_INIT:
					checked = append(checked, energyRequest{meter: m, skip: energyrequest.ReasonNotRegistered})
					continue
				case !m.State.ActiveSince.Valid || !m.State.InactiveSince.Valid:
					checked = append(checked, energyRequest{meter: m, skip: energyrequest.ReasonNoActivePeriod})
					continue
				}

				// whole days in local time, like the request period: inactivesince is the last active day, so it is
				// requested up to its end (before: UTC midnight, which left out the last day)
				since, until := m.State.ActiveSince.Date, m.State.InactiveSince.Date
				_activeSince := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, time.Local).UnixMilli()
				_inactiveSince := util.TruncateToEndOfDay(time.Date(until.Year(), until.Month(), until.Day(), 0, 0, 0, 0, time.Local)).UnixMilli()

				p, ok := periods[m.MeteringPoint]
				if !ok {
					p = period{from: fromDate, to: toDate}
				}
				if _activeSince > p.to || _inactiveSince < p.from {
					checked = append(checked, energyRequest{meter: m, skip: energyrequest.ReasonNotActiveInPeriod})
					continue
				}

				from := util.MaxTimeStamp(_activeSince, p.from)
				to := util.MinTimeStamp(_inactiveSince, p.to)
				checked = append(checked, energyRequest{meter: m, from: from, to: to})
				candidates++
			}
			// lock: a metering point requested in the last hour for an overlapping period and not rejected is
			// skipped. Lock check and saving the run are serialised per tenant.
			unlock := func() {}
			locks := energyrequest.Locks{}
			if candidates > 0 {
				unlock = energyrequest.LockTenant(tenant)
				lockRows, lockTenants, err := h.db.EnergyRequestCommunity(r.Context(), tenant)
				if err != nil {
					unlock()
					log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to request metering point values.")
					respondWith(r.Context(), w, http.StatusInternalServerError, tenant, err)
					return
				}
				locks = energyrequest.NewLocks(lockRows, tenant, lockTenants, time.Now())
			}
			// Skip ranks the reasons of several participations (independent of their order). The lock is checked per
			// participation: of an owner change, a part requested within the hour is not sent again
			var free []energyRequest
			for _, c := range checked {
				m := c.meter
				if c.skip != "" {
					run.Skip(m.MeteringPoint, c.skip)
					continue
				}
				if until := locks.Until(m.MeteringPoint, c.from, c.to); until > 0 {
					run.SkipUntil(m.MeteringPoint, energyrequest.ReasonAlreadyRequested, until)
					continue
				}
				free = append(free, c)
			}
			// one request per metering point and merged period: parts of several participations that overlap or touch
			// go out once (day-granular: to is the end or an inactive date of a day). The run records the span (Add merges)
			byMeter := map[string][]energyRequest{}
			var order []string
			for _, c := range free {
				if _, ok := byMeter[c.meter.MeteringPoint]; !ok {
					order = append(order, c.meter.MeteringPoint)
				}
				byMeter[c.meter.MeteringPoint] = append(byMeter[c.meter.MeteringPoint], c)
			}
			var send []energyRequest
			for _, mp := range order {
				parts := byMeter[mp]
				sort.Slice(parts, func(i, j int) bool { return parts[i].from < parts[j].from })
				var merged []energyRequest
				for _, c := range parts {
					n := len(merged)
					if n > 0 && energyrequest.Touch(merged[n-1].to, c.from) {
						merged[n-1].to = max(merged[n-1].to, c.to)
						// sent with the current entry, else the later one (BEG: its grid operator is the receiver)
						if merged[n-1].meter.State.Flag != model.F_ASSIGNED {
							merged[n-1].meter = c.meter
						}
						continue
					}
					merged = append(merged, c)
				}
				for _, c := range merged {
					run.Add(mp, c.from, c.to)
					send = append(send, c)
				}
			}
			if perMeter || request.From == 0 {
				run.FitPeriod() // the summary shows the period actually requested
			}
			// the run is stored before sending, so every answer comes after it; a failed send is taken out again below.
			// Detached: a client that gives up after the lock check must not leave the run unstored
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
			runRowId, err := energyrequest.Save(ctx, h.db, tenant, requested, run)
			cancel()
			unlock()
			if err != nil {
				log.WithContext(ctx).WithField("tenant", tenant).WithError(err).Error("failed to request metering point values: cannot save run")
				respondWith(r.Context(), w, http.StatusInternalServerError, tenant, model.ErrRequestEnergyData(err))
				return
			}

			var sent []energyrequest.MeterRange
			for _, s := range send {
				m, from, to := s.meter, s.from, s.to
				if err = mqttclient.RequestingEnergyData(eeg, m, from, to); err != nil {
					log.WithContext(ctx).WithField("tenant", tenant).Errorf("request Metering values %s (%s - %s)", m.MeteringPoint,
						time.UnixMilli(from).String(), time.UnixMilli(to).String())
					errorList = append(errorList, fmt.Sprintf("%s: %s", m.MeteringPoint, err.Error()))
				} else {
					sent = append(sent, energyrequest.MeterRange{MeteringPoint: m.MeteringPoint, From: from, To: to})
				}
			}
			if errorList != nil && len(errorList) > 0 {
				// only what was sent stays in the run: the rest is not locked, not summarised. Detached: a client that
				// gives up must not leave unsent metering points locked
				ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
				energyrequest.KeepSent(ctx, h.db, runRowId, run, sent)
				cancel()
				log.WithContext(ctx).WithField("tenant", tenant).Errorf("failed to request metering point values. Err: %v", errorList)
				respondWith(r.Context(), w, http.StatusInternalServerError, tenant, model.ErrRequestEnergyData(errors.New(strings.Join(errorList, "; "))))
				return
			}
			// the metering points requested from the grid operator and the skipped ones with their reason
			respondWithJSON(w, http.StatusOK, struct {
				Requested int                     `json:"requested"`
				Skipped   []energyrequest.Skipped `json:"skipped"`
			}{Requested: len(run.Meters), Skipped: run.Skipped})
			return
		}
		respondWithStatus(w, http.StatusNoContent)
	}
}

func (h *MeteringHandler) requestRevokeMeteringPoint() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		participantId := vars["pid"]
		log.WithContext(r.Context()).WithField("tenant", tenant).Infof("Revoke meteringpoint in participant %s", participantId)

		request := struct {
			MeteringPoints []struct {
				Meter     string              `json:"meter"`
				Direction model.DirectionType `json:"direction"`
				ConsentId string              `json:"consentId"`
			} `json:"meteringPoints"`
			From   int64  `json:"from"`
			Reason string `json:"to"`
		}{}
		err := json.NewDecoder(r.Body).Decode(&request)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to revoke metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrParseJson(err))
			return
		}

		eeg, err := h.db.GetEegById(r.Context(), tenant)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to revoke metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, model.ErrGetEeg(err))
			return
		}
		participant, err := h.db.QueryParticipant(r.Context(), tenant, participantId)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to revoke metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}

		fromDate := util.TruncateToStartOfDay(time.UnixMilli(request.From)).UnixMilli()
		var reason *string
		if len(strings.TrimSpace(request.Reason)) > 0 {
			reason = &request.Reason
		}

		var meters []string
		for _, m := range request.MeteringPoints {
			meters = append(meters, m.Meter)
		}

		if meters == nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).Error("failed to revoke metering point. Err: No Meter selected")
			respondWith(r.Context(), w, http.StatusInternalServerError, tenant, model.Wrap(errors.New("no Meter selected"), 3100))
			return
		}

		log.WithContext(r.Context()).WithField("tenant", tenant).Infof("revoke Metering %v (%s)", meters, time.UnixMilli(fromDate).String())
		if eeg.Online {
			errorList := []string{}
			meters, err := h.db.FindActiveMeteringByIds(r.Context(), tenant, meters)
			if err != nil {
				respondWith(r.Context(), w, http.StatusInternalServerError, tenant, err)
				return
			}
			for _, m := range meters {
				if err = mqttclient.RevokeMeteringPoint(eeg, m, fromDate, reason); err != nil {
					log.WithContext(r.Context()).WithField("tenant", tenant).Errorf("request revoke Metering %s (%d)", m.MeteringPoint, fromDate)
					errorList = append(errorList, fmt.Sprintf("%s: %s", m.MeteringPoint, err.Error()))
				}
			}
			if errorList != nil && len(errorList) > 0 {
				log.WithContext(r.Context()).WithField("tenant", tenant).Errorf("failed to revoke metering point. Err: %v", errorList)
				respondWith(r.Context(), w, http.StatusInternalServerError, tenant, model.ErrRevokeMeter(errors.New(strings.Join(errorList, "; "))))
				return
			}
		}
		respondWithJSON(w, http.StatusCreated, participant)
	}
}

func (h *MeteringHandler) removeMeteringPoint() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		participantId := vars["pid"]
		meterId := vars["mid"]

		err := h.db.RemoveMeteringPoint(r.Context(), tenant, participantId, meterId)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to remove metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}
		respondWithJSON(w, http.StatusAccepted, map[string]interface{}{"meteringpoint": meterId})
	}
}

func (h *MeteringHandler) archiveMeteringPoint() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		vars := mux.Vars(r)
		meterId := vars["mid"]
		participantId := vars["pid"]

		err := h.db.ArchiveMeteringPoint(r.Context(), tenant, participantId, meterId)
		if err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Error("failed to archive metering point.")
			respondWith(r.Context(), w, http.StatusBadRequest, tenant, err)
			return
		}
		respondWithJSON(w, http.StatusAccepted, map[string]interface{}{"meteringpoint": meterId})
	}
}

func (h *MeteringHandler) requestChangePartitionFactor() middleware.JWTHandlerFunc {
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
		respondWithStatus(w, http.StatusNoContent)
	}
}

// offlineRegistrationUnsupported is the answer to activationMode OFFLINE (decision 2026-10-30).
const offlineRegistrationUnsupported = "Offline-Registrierung wird von den Netzbetreibern nicht mehr unterstützt"

// checkActivationMode accepts a missing activationMode and ONLINE (both register online).
// OFFLINE and any other value are rejected. Callers check only where a registration would be sent (online EEG),
// before they write or send anything.
func checkActivationMode(mode model.RegistrationMode) error {
	switch mode {
	case "", model.ONLINE:
		return nil
	case model.OFFLINE:
		return model.ErrOfflineRegistrationUnsupported(errors.New(offlineRegistrationUnsupported))
	default:
		return model.ErrUnknownActivationMode(fmt.Errorf("unknown activationMode %q", mode))
	}
}

// edaRegisterMeteringpoint sends the online registration (ANFORDERUNG_ECON) for an online EEG.
func edaRegisterMeteringpoint(eeg *model.Eeg, meter *model.MeteringPoint, from *int64) error {
	if eeg.Online && meter != nil {
		return mqttclient.RegistrationForParticipation(eeg, meter, from)
	}
	return nil
}
