package api

import (
	"context"
	"encoding/json"
	"net/http"

	"at.ourproject/vfeeg-backend/model"
	log "github.com/sirupsen/logrus"
)

type HttpError struct {
	Code    int    `json:"code"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

func BadProcessError(code int, message string) *HttpError {
	return &HttpError{
		code,
		"Bad Process",
		message,
	}
}

func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	response, _ := json.Marshal(payload)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(response)
}

func respondWithError(w http.ResponseWriter, httpCode int, message string) {
	respondWithJSON(w, httpCode, map[string]string{"error": message})
}

func respondWithStatus(w http.ResponseWriter, code int) {
	w.WriteHeader(code)
}

func respondWithHttpError(w http.ResponseWriter, httpCode int, error *HttpError) {
	respondWithJSON(w, httpCode, map[string]interface{}{"error": error})
}

func respondWith(ctx context.Context, w http.ResponseWriter, httpCode int, tenant string, data interface{}) {
	switch e := data.(type) {
	case *model.VfeegError:
		log.WithContext(ctx).WithField("tenant", tenant).WithField("code", e.Code).WithError(e).Error(e.Error())
		text := model.UserText(e)
		respondWithHttpError(w, httpCode, &HttpError{Error: text, Code: e.Code, Message: text})
		return
	default:
		respondWithJSON(w, httpCode, data)
	}
}
