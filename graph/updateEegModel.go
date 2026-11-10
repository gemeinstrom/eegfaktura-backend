package graph

import (
	"context"
	"errors"

	log "github.com/sirupsen/logrus"
)

// refuseUpdateEegModel logs a refused updateEegModel once as a warning with an event (eegfaktura-dev#54 #13: the
// access line says success, the GraphQL answer carries the error) and returns the error for the answer.
func refuseUpdateEegModel(ctx context.Context, reason string) error {
	log.WithContext(ctx).WithFields(log.Fields{"event.action": "graphql.update_eeg_model.refused",
		"event.outcome": "failure", "event.reason": reason}).Warn("updateEegModel refused")
	return errors.New(reason)
}
