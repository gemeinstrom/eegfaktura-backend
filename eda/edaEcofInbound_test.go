package eda

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Offline registration is no longer sent (decision 2026-10-30), but answers to ANFORDERUNG_ECOF
// requests sent before that still arrive on EC_REQ_OFF and must complete the pending case. The message
// goes through the subscription table, so the EC_REQ_OFF routing is covered too.
func TestProtocolEcReqOff_inboundStillProcessed(t *testing.T) {
	const tenant = "TE000174"
	const ecId = "AT00300000000TC000174000000000001"
	_, err := testDB.DbInstance.Exec(`INSERT INTO base.eeg
		(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
		 street, "streetNumber", city, zip, email)
		VALUES ($1, 'EEG-174', 'Test 174', 'TE000174', 'LOCAL', 'AT003000', 'Netz OÖ', $2,
		 'Weg', '1', 'Linz', '4020', 'test-174@example.org') ON CONFLICT (tenant) DO NOTHING`, tenant, ecId)
	require.NoError(t, err)

	var handle func(model.SubscribeMessage)
	for _, s := range getSubsriptions(context.Background()) {
		if s.Protocol == model.EC_REQ_OFF {
			handle = s.Handler
		}
	}
	require.NotNil(t, handle, "EC_REQ_OFF must stay subscribed")

	tests := []struct {
		name      string
		meter     string
		code      model.EbMsMessageType
		wireCode  string
		respCode  int16
		wantState model.ProcessStatusType
	}{
		{name: "ZUSTIMMUNG_ECOF approves", meter: "AT0030000000000000000000000174001",
			code: model.EBMS_OFFLINE_REG_APPROVAL, wireCode: "ZUSTIMMUNG_ECOF", respCode: CONSENT_GRANTED, wantState: model.APPROVED},
		{name: "ABLEHNUNG_ECOF rejects", meter: "AT0030000000000000000000000174002",
			code: model.EBMS_OFFLINE_REG_REJECTION, wireCode: "ABLEHNUNG_ECOF", respCode: 159, wantState: model.INVALID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := testDB.DbInstance.Exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction,
				status, process_state, "modifiedAt", "modifiedBy", "registeredSince", flag)
				VALUES ($1, 'ea1142dc-03da-15ee-b82b-5a985b4b033a', $2, 'CONSUMPTION', 'INIT', 'PENDING', now(), 'test',
				 '2026-10-01', 1)`, tt.meter, tenant)
			require.NoError(t, err)
			t.Cleanup(func() { // so the test also passes with -count=2
				_, _ = testDB.DbInstance.Exec(`DELETE FROM base.notification WHERE tenant = $1`, tenant)
				_, _ = testDB.DbInstance.Exec(`DELETE FROM base.meteringpoint WHERE tenant = $1 AND metering_point_id = $2`, tenant, tt.meter)
			})

			msg := model.SubscribeMessage{MessageCode: tt.code, Protocol: model.EC_REQ_OFF, Tenant: strings.ToLower(tenant)}
			require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"conversationId":"RC000174202610300000000000000%d","messageId":"AT003000202610300000000000000%d","sender":"AT003000","receiver":"TE000174","messageCode":"%s","messageCodeVersion":"01.12","requestId":"F174","ecId":"%s","responseData":[{"meteringPoint":"%s","responseCode":[%d],"consentId":"AT003000202610300000000000174"}]}`,
				tt.respCode, tt.respCode, tt.wireCode, ecId, tt.meter, tt.respCode)), &msg.Payload))

			handle(msg)

			var state string
			require.NoError(t, testDB.DbInstance.Get(&state, `SELECT process_state FROM base.meteringpoint WHERE tenant = $1 AND metering_point_id = $2`, tenant, tt.meter))
			assert.Equal(t, string(tt.wantState), state)

			var n int
			require.NoError(t, testDB.DbInstance.Get(&n, `SELECT count(*) FROM base.notification WHERE tenant = $1
				AND notification->>'type' = $2 AND (notification->'meteringPoints')::jsonb ? $3`, tenant, tt.wireCode, tt.meter))
			assert.Equal(t, 1, n)
		})
	}
}
