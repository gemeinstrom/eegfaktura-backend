package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

// /eeg/export/masterdata (konzept-beitrittsgebuehr.md): POST with the optional joiningFeeState list, GET stays.
// Tenant from the checked header as before; the list only fills the column of the tenant's own members.
func TestExportMasterdata_joiningFee(t *testing.T) {
	const (
		tenant  = "TE000002"
		member  = "ea9942db-03da-11ee-b82b-5a985b4b033a" // TE000002, 5 metering points
		foreign = "ea9942da-03da-11ee-b82b-5a985b4b033a" // TE000001
	)
	db, err := database.GetDB(context.Background())
	require.NoError(t, err)

	// the member gets an EEG tariff with a joining fee; restored afterwards (fixture of other tests)
	tariff := model.Tariff{Type: model.EEG, Name: "Beitrag mit Beitrittsgebühr", JoiningFee: 49.90}
	require.NoError(t, db.AddTariff(context.Background(), tenant, "test", &tariff))
	var oldTariff *string
	require.NoError(t, testDB.DbInstance.Get(&oldTariff, `SELECT "tariffId"::text FROM base.participant WHERE id = $1`, member))
	_, err = testDB.DbInstance.Exec(`UPDATE base.participant SET "tariffId" = $1 WHERE id = $2`, tariff.Id.String(), member)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testDB.DbInstance.Exec(`UPDATE base.participant SET "tariffId" = $1 WHERE id = $2`, oldTariff, member)
		_, _ = testDB.DbInstance.Exec(`DELETE FROM base.tariff WHERE id = $1`, tariff.Id.String())
	})

	orig := middleware.VerifyTokenClaims
	t.Cleanup(func() { middleware.VerifyTokenClaims = orig })
	var tokenTenants, roles []string
	middleware.VerifyTokenClaims = func(ctx context.Context, token string) (*middleware.PlatformClaims, error) {
		c := &middleware.PlatformClaims{Tenants: tokenTenants, AccessGroups: middleware.AccessGroups{"/EEG_ADMIN"}}
		c.RealmAccess.Roles = roles
		return c, nil
	}
	router := InitEegRouter(mux.NewRouter(), db)
	call := func(method, header, body string) *httptest.ResponseRecorder {
		var req *http.Request
		if body == "" {
			req = httptest.NewRequest(method, "/eeg/export/masterdata", nil)
		} else {
			req = httptest.NewRequest(method, "/eeg/export/masterdata", strings.NewReader(body))
		}
		req.Header.Set("Authorization", middleware.BEARER_SCHEMA+"token")
		if header != "" {
			req.Header.Set("tenant", header)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	// fee and status of every row of the member
	memberCells := func(w *httptest.ResponseRecorder) [][2]string {
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		f, err := excelize.OpenReader(bytes.NewReader(w.Body.Bytes()))
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		rows, err := f.GetRows("Mitglieder", excelize.Options{RawCellValue: true})
		require.NoError(t, err)
		col := -1
		for i, h := range rows[0] {
			if h == "Mitgliedstarif" {
				col = i
			}
		}
		require.GreaterOrEqual(t, col, 0)
		var cells [][2]string
		for _, row := range rows[1:] {
			assert.NotContains(t, strings.Join(row, "|"), "RG-FREMD", "entry of another tenant's member")
			if row[col] == tariff.Name {
				cells = append(cells, [2]string{row[col+1], row[col+2]})
			}
		}
		require.Len(t, cells, 5, "every row of the member")
		return cells
	}
	billedBody := `[{"participantId":"` + member + `","energyBilled":true,"joiningFee":{"documentNumber":"RG202600012","documentDate":"2026-01-15"}},` +
		`{"participantId":"` + foreign + `","energyBilled":true,"joiningFee":{"documentNumber":"RG-FREMD","documentDate":"2026-02-01"}}]`

	tokenTenants, roles = []string{tenant}, nil
	t.Run("POST with the list", func(t *testing.T) {
		for _, c := range memberCells(call(http.MethodPost, tenant, billedBody)) {
			assert.Equal(t, [2]string{"49.9", "verrechnet mit RG202600012 vom 15.01.2026"}, c)
		}
	})
	t.Run("POST with paid outside and requested", func(t *testing.T) {
		body := `[{"participantId":"` + member + `","energyBilled":true,"joiningFee":null,"requested":false,` +
			`"paidExternally":{"paidOn":"2025-03-01","note":"bar beim Beitritt"}}]`
		for _, c := range memberCells(call(http.MethodPost, tenant, body)) {
			assert.Equal(t, [2]string{"49.9", "außerhalb bezahlt am 01.03.2025 (bar beim Beitritt)"}, c)
		}
		body = `[{"participantId":"` + member + `","energyBilled":true,"joiningFee":null,"requested":true,"paidExternally":null}]`
		for _, c := range memberCells(call(http.MethodPost, tenant, body)) {
			assert.Equal(t, [2]string{"49.9", "zur Verrechnung im nächsten Abrechnungslauf vorgemerkt"}, c)
		}
		body = `[{"participantId":"` + member + `","energyBilled":true,"joiningFee":null,"requested":false,"paidExternally":null}]`
		for _, c := range memberCells(call(http.MethodPost, tenant, body)) {
			assert.Equal(t, [2]string{"49.9", "nicht verrechnet – Mitglied wurde schon abgerechnet"}, c)
		}
	})
	t.Run("POST without body", func(t *testing.T) {
		for _, c := range memberCells(call(http.MethodPost, tenant, "")) {
			assert.Equal(t, [2]string{"49.9", ""}, c)
		}
	})
	t.Run("GET stays", func(t *testing.T) {
		for _, c := range memberCells(call(http.MethodGet, tenant, "")) {
			assert.Equal(t, [2]string{"49.9", ""}, c)
		}
	})
	t.Run("malformed body", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, call(http.MethodPost, tenant, `{"participantId":`).Code)
	})
	t.Run("foreign tenant header", func(t *testing.T) {
		assert.Equal(t, http.StatusForbidden, call(http.MethodPost, "TE000004", billedBody).Code)
	})
	t.Run("missing tenant header", func(t *testing.T) {
		assert.Equal(t, http.StatusForbidden, call(http.MethodPost, "", billedBody).Code)
	})
	t.Run("superuser", func(t *testing.T) {
		tokenTenants, roles = []string{"TE000004"}, []string{"superuser"}
		t.Cleanup(func() { tokenTenants, roles = []string{tenant}, nil })
		for _, c := range memberCells(call(http.MethodPost, tenant, billedBody)) {
			assert.Equal(t, [2]string{"49.9", "verrechnet mit RG202600012 vom 15.01.2026"}, c)
		}
	})
}
