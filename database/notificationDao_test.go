package database

import (
	"at.ourproject/vfeeg-backend/model"
	"context"
	"encoding/json"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"math"
	"testing"
	"time"
)

func TestFetchEdaHistory(t *testing.T) {
	mDB, mock := newMockDatabase(t)

	start, _ := time.Parse(time.RFC3339Nano, "2023-10-03T17:00:00.000Z")
	end, _ := time.Parse(time.RFC3339Nano, "2023-10-04T18:00:00.000Z")

	//stmt := "SELECT \"conversationId\", \"date\", \"direction\", \"issuer\", \"message\", \"protocol\", \"tenant\", \"type\" FROM \"base\".\"processhistory\" WHERE ((\"tenant\" = 'RC100298') AND (\"protocol\" IS NOT NULL) AND (\"date\" BETWEEN '2023-10-03T19:00:00+02:00' AND '2023-10-04T20:00:00+02:00'))"
	stmt := "SELECT (.+) FROM \"base\".\"processhistory\" WHERE (.+)"

	rows := sqlmock.NewRows([]string{"conversationId", "date", "direction", "issuer", "message", "protocol", "tenant", "type"}).
		AddRow("1", time.Now(), "CONSUMPTION", "ADMIN", "{}", "CR_MSG", "RC100298", model.EBMS_ONLINE_REG_APPROVAL)
	mock.ExpectQuery(stmt).WillReturnRows(rows)
	//res, err := FetchEdaHistory(mockDb.OpenMockDb, "RC100298", (time.Now().Add(25 * time.Hour * -1)).UnixMilli(), time.Now().UnixMilli())
	res, err := mDB.FetchEdaHistory(context.Background(), "RC100298", "", start.UnixMilli(), end.UnixMilli(), 0)
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestFetchEdaHistory_pageSizeBounds(t *testing.T) {
	start, _ := time.Parse(time.RFC3339Nano, "2023-10-03T17:00:00.000Z")
	end, _ := time.Parse(time.RFC3339Nano, "2023-10-04T18:00:00.000Z")

	type result struct {
		History struct {
			Next struct {
				Start    int64 `json:"start"`
				PageSize uint  `json:"page_size"`
			} `json:"next"`
		}
		Data map[string]map[string][]json.RawMessage `json:"data"`
	}
	fetch := func(pageSize uint) result {
		mDB, mock := newMockDatabase(t)
		rows := sqlmock.NewRows([]string{"conversationId", "date", "message", "protocol", "tenant"}).
			AddRow("1", start, "{}", "CR_MSG", "RC100298").
			AddRow("2", start, "{}", "CR_MSG", "RC100298").
			AddRow("3", end, "{}", "CR_MSG", "RC100298")
		mock.ExpectQuery(`SELECT (.+) FROM "base"."processhistory" WHERE (.+)`).WillReturnRows(rows)

		var res interface{}
		var err error
		require.NotPanics(t, func() {
			res, err = mDB.FetchEdaHistory(context.Background(), "RC100298", "CR_MSG", start.UnixMilli(), end.UnixMilli(), pageSize)
		})
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())

		b, err := json.Marshal(res)
		require.NoError(t, err)
		var out result
		require.NoError(t, json.Unmarshal(b, &out))
		return out
	}

	// Page size smaller than the result: first page plus next cursor.
	out := fetch(2)
	require.Len(t, out.Data["CR_MSG"], 2)
	require.Equal(t, uint(2), out.History.Next.PageSize)
	require.Equal(t, end.Unix()*1000, out.History.Next.Start)

	// Page size larger than the result, up to the largest uint: all rows, no next cursor.
	for _, ps := range []uint{3, 1000, math.MaxUint} {
		out = fetch(ps)
		require.Len(t, out.Data["CR_MSG"], 3)
		require.Zero(t, out.History.Next.PageSize)
	}
}
