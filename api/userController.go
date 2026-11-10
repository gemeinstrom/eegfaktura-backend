package api

import (
	"net/http"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	"github.com/gorilla/mux"
)

func InitUserRouter(r *mux.Router, db database.Database) *mux.Router {
	h := NewUserHandler(db)
	s := r.PathPrefix("/user").Subrouter()

	s.HandleFunc("/get-user", middleware.Protect(h.getUser())).Methods("GET")

	return r
}

type UserHandler struct {
	db database.Database
}

func NewUserHandler(db database.Database) *UserHandler {
	return &UserHandler{db: db}
}

func (h *UserHandler) getUser() middleware.JWTHandlerFunc {
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
