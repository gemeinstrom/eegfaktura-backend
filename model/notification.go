package model

import (
	"time"

	"github.com/jmoiron/sqlx/types"
)

type NotificationType string
type NotificationProcess string

const (
	N_PROCESS_IMPORT_EXCEL NotificationProcess = "EXCEL_IMPORT"
	N_PROCESS_EDA_PROCESS  NotificationProcess = "EDA_PROCESS"
	// hidden rows of the energy request summary (platform#173), not listed in Messages
	N_PROCESS_ENERGY_REQUEST NotificationProcess = "ENERGY_REQUEST"

	N_TYPE_NOTIFICATION NotificationType = "NOTIFICATION"
	N_TYPE_ERROR        NotificationType = "ERROR"
	N_TYPE_MESSAGE      NotificationType = "MESSAGE"
)

type EegNotification struct {
	Id      int64               `json:"id"`
	MsgType string              `json:"type" db:"type"`
	Process NotificationProcess `json:"process" db:"process"`
	Message types.JSONText      `json:"message" db:"notification"`
	Date    time.Time           `json:"date"`
}
