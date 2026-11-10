package eda

import (
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/database/dbtest"
	"at.ourproject/vfeeg-backend/model"
	"at.ourproject/vfeeg-backend/services"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jjeffery/civil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"strconv"
	"strings"
	"testing"
)

var testDB *dbtest.TestDatabase

func TestMain(m *testing.M) {
	testDB = dbtest.SetupTestDatabase()
	if err := testDB.Open(context.Background()); err != nil {
		panic(err)
	}
	db := database.NewDatabase(testDB.DbInstance)
	database.SetDB(db)
	// no test talks to a mail server; a test that looks at mails replaces its sender
	noMail := func(string, string, string, *string, *bytes.Buffer, []*services.Attachment, *services.Attachment) error {
		return nil
	}
	sendActivationMail, sendInactiveMail = noMail, noMail
	sendZpListMail = func(string, string, string, *string, *bytes.Buffer, *services.Attachment) error { return nil }
	StartMailWorker()
	// os.Exit skips deferred calls: close and remove the container before it
	code := m.Run()
	_ = db.CloseDB()
	testDB.TearDown()
	os.Exit(code)
}

// cleanupCommunities removes what a test with fixed tenants leaves behind, so it survives -count=2.
func cleanupCommunities(t *testing.T, tenants ...string) {
	t.Cleanup(func() {
		for _, tenant := range tenants {
			for _, table := range []string{"notification", "processhistory", "metering_partition_factor", "meteringpoint", "eeg"} {
				_, err := testDB.DbInstance.Exec(`DELETE FROM base.`+table+` WHERE tenant = $1`, tenant)
				require.NoError(t, err)
			}
		}
	})
}

var extractMeters = func(p model.EbmsMessage, proto model.EbMsMessageType) []string {
	meters := []string{}
	switch proto {
	case model.EBMS_ONLINE_REG_APPROVAL, model.EBMS_ONLINE_REG_ANSWER, model.EBMS_ONLINE_REG_REJECTION:
		_, meters, _, _ = extractResponseCodeAndMeteringPoint(&p)
	default:
		meters = p.Meters()
	}
	return meters
}

func arrayToString(n []int16) string {
	var IDs []string
	for _, i := range n {
		IDs = append(IDs, strconv.FormatInt(int64(i), 10))
	}

	return strings.Join(IDs, ", ")
}

func prepareCPListRecordMock(messageCode model.EbMsMessageType) ( /*recorder *RecorderMock,*/ msg model.SubscribeMessage, err error) {
	msg = model.SubscribeMessage{
		MessageCode: messageCode,
		Protocol:    model.EC_REQ_ONL,
		Tenant:      "TE100001",
		Payload:     model.EbmsMessage{},
	}

	message := fmt.Sprintf(`{"conversationId":"RC100346202406091843475020000046464",
"messageId":"AT003300202406292044374080000009571","sender":"AT003300","receiver":"TE1000001",
"messageCode":"%s","messageCodeVersion":"02.00",
"ecId":"AT00330004600RC100346000000000001",
"meterList":[{"meteringPoint":"AT0030000000000000000000000519928",
"direction":"CONSUMPTION","from":1719612000000,"partFact":100, "consentId":"AT1111122222"}]}`, messageCode)

	err = json.Unmarshal([]byte(message), &msg.Payload)
	return

}

func prepareNotificationRecorderMock(messageCode model.EbMsMessageType, codes []int16,
	consentId *string, meter *model.Meter) (msg model.SubscribeMessage, err error) {
	msg = model.SubscribeMessage{
		MessageCode: messageCode,
		Protocol:    model.EC_REQ_ONL,
		Tenant:      "TE000002",
		Payload: model.EbmsMessage{
			ConversationId: "RC100298202308171692252620000000321",
			MessageId:      "AT003000202307070957427130168201034",
			Sender:         "AT003000",
			Receiver:       "TE000002",
			MessageCode:    messageCode,
			RequestId:      "6P2EU64Z",
			ResponseData: []model.ResponseData{{
				MeteringPoint: "AT0030000000000000000000000410702",
				ResponseCode:  codes,
			}},
		},
	}

	if consentId != nil {
		msg.Payload.ResponseData[0].ConsentId = *consentId
	}

	if meter != nil {
		msg.Payload.Meter = meter
	}
	return
}

func prepareNotificationMessage(message string, msgCode model.EbMsMessageType, msgProto model.EdaProtocol, tenant, ecId string, codes []int16, consentId *string, meter *model.Meter) (msg model.SubscribeMessage, err error) {

	msg = model.SubscribeMessage{
		MessageCode: msgCode,
		Protocol:    msgProto,
		Tenant:      tenant,
		Payload:     model.EbmsMessage{},
	}

	err = json.Unmarshal([]byte(message), &msg.Payload)

	if err != nil {
		return
	}

	if consentId != nil {
		msg.Payload.ResponseData[0].ConsentId = *consentId
	}

	if meter != nil {
		switch msgCode {
		case model.EBMS_ONLINE_REG_INIT:
			msg.Payload.Meter = meter
			msg.Payload.EcId = ecId
		case model.EBMS_ONLINE_REG_COMPLETION:
			msg.Payload.MeterList[0].MeteringPoint = meter.MeteringPoint
			msg.Payload.MeterList[0].Direction = meter.Direction
		default:
			msg.Payload.ResponseData[0].MeteringPoint = meter.MeteringPoint
			msg.Payload.ResponseData[0].ResponseCode = codes
		}
	}

	return
}

func prepareMessage(t *testing.T, messageCode model.EbMsMessageType, codes []int16) model.SubscribeMessage {

	msg := model.SubscribeMessage{
		MessageCode: messageCode,
		Protocol:    model.EC_REQ_ONL,
		Tenant:      "TE100001",
		Payload:     model.EbmsMessage{},
	}
	message := fmt.Sprintf(`{
"conversationId":"RC100298202308171692252620000000321",
"messageId":"AT003000202307070957427130168201034",
"ecId":"AT00300000000TC000001000000000001","sender":"AT003000",
"receiver":"TE100001","messageCode":"%s",
"requestId":"6P2EU64Z","responseData":[{"meteringPoint":"AT0030000000000000000000000410702","responseCode":[%s]}]}`, messageCode, arrayToString(codes))

	err := json.Unmarshal([]byte(message), &msg.Payload)
	require.NoError(t, err)
	return msg
}

func TestProtcolCrMsgHandler(t *testing.T) {
	jsonString := `{
	"messageId":"AT003000202208201421374610104995950",
	"conversationId":"AT003000202208191420233640008300242",
	"sender":"AT003000",
	"receiver":"RC100130",
	"messageCode":"DATEN_CRMSG",
	"meter": {"meteringPoint":"AT0030000000000000000000000200959"},
	"energy": [
		{
			"start":1660773600000,"end":1660860000000,"interval":"QH","nInterval":288,
			"data":[
				{"meterCode":"1-1:1.9.0 G.01","value":[{"from":1660773600000,"to":1660774500000,"method":"L1","value":0.00525},{"from":1660774500000,"to":1660775400000,"method":"L1","value":0.0055},{"from":1660775400000,"to":1660776300000,"method":"L1","value":0.0055},{"from":1660776300000,"to":1660777200000,"method":"L1","value":0.00925},{"from":1660777200000,"to":1660778100000,"method":"L1","value":0.0075},{"from":1660778100000,"to":1660779000000,"method":"L1","value":0.005},{"from":1660779000000,"to":1660779900000,"method":"L1","value":0.006},{"from":1660779900000,"to":1660780800000,"method":"L1","value":0.0055},{"from":1660780800000,"to":1660781700000,"method":"L1","value":0.006},{"from":1660781700000,"to":1660782600000,"method":"L1","value":0.00525},{"from":1660782600000,"to":1660783500000,"method":"L1","value":0.00625},{"from":1660783500000,"to":1660784400000,"method":"L1","value":0.00625},{"from":1660784400000,"to":1660785300000,"method":"L1","value":0.0065},{"from":1660785300000,"to":1660786200000,"method":"L1","value":0.006},{"from":1660786200000,"to":1660787100000,"method":"L1","value":0.006},{"from":1660787100000,"to":1660788000000,"method":"L1","value":0.0085},{"from":1660788000000,"to":1660788900000,"method":"L1","value":0.00875},{"from":1660788900000,"to":1660789800000,"method":"L1","value":0.00975},{"from":1660789800000,"to":1660790700000,"method":"L1","value":0.01},{"from":1660790700000,"to":1660791600000,"method":"L1","value":0.009},{"from":1660791600000,"to":1660792500000,"method":"L1","value":0.008},{"from":1660792500000,"to":1660793400000,"method":"L1","value":0.0065},{"from":1660793400000,"to":1660794300000,"method":"L1","value":0.007},{"from":1660794300000,"to":1660795200000,"method":"L1","value":0.0065},{"from":1660795200000,"to":1660796100000,"method":"L1","value":0.00725},{"from":1660796100000,"to":1660797000000,"method":"L1","value":0.00725},{"from":1660797000000,"to":1660797900000,"method":"L1","value":0.00625},{"from":1660797900000,"to":1660798800000,"method":"L1","value":0.006},{"from":1660798800000,"to":1660799700000,"method":"L1","value":0},{"from":1660799700000,"to":1660800600000,"method":"L1","value":0.00025},{"from":1660800600000,"to":1660801500000,"method":"L1","value":0.00175},{"from":1660801500000,"to":1660802400000,"method":"L1","value":0.00075},{"from":1660802400000,"to":1660803300000,"method":"L1","value":0.00325},{"from":1660803300000,"to":1660804200000,"method":"L1","value":0.00725},{"from":1660804200000,"to":1660805100000,"method":"L1","value":0.01675},{"from":1660805100000,"to":1660806000000,"method":"L1","value":0.0155},{"from":1660806000000,"to":1660806900000,"method":"L1","value":0},{"from":1660806900000,"to":1660807800000,"method":"L1","value":0},{"from":1660807800000,"to":1660808700000,"method":"L1","value":0},{"from":1660808700000,"to":1660809600000,"method":"L1","value":0},{"from":1660809600000,"to":1660810500000,"method":"L1","value":0},{"from":1660810500000,"to":1660811400000,"method":"L1","value":0},{"from":1660811400000,"to":1660812300000,"method":"L1","value":0},{"from":1660812300000,"to":1660813200000,"method":"L1","value":0},{"from":1660813200000,"to":1660814100000,"method":"L1","value":0},{"from":1660814100000,"to":1660815000000,"method":"L1","value":0},{"from":1660815000000,"to":1660815900000,"method":"L1","value":0},{"from":1660815900000,"to":1660816800000,"method":"L1","value":0},{"from":1660816800000,"to":1660817700000,"method":"L1","value":0},{"from":1660817700000,"to":1660818600000,"method":"L1","value":0},{"from":1660818600000,"to":1660819500000,"method":"L1","value":0},{"from":1660819500000,"to":1660820400000,"method":"L1","value":0},{"from":1660820400000,"to":1660821300000,"method":"L1","value":0},{"from":1660821300000,"to":1660822200000,"method":"L1","value":0},{"from":1660822200000,"to":1660823100000,"method":"L1","value":0},{"from":1660823100000,"to":1660824000000,"method":"L1","value":0},{"from":1660824000000,"to":1660824900000,"method":"L1","value":0},{"from":1660824900000,"to":1660825800000,"method":"L1","value":0},{"from":1660825800000,"to":1660826700000,"method":"L1","value":0},{"from":1660826700000,"to":1660827600000,"method":"L1","value":0},{"from":1660827600000,"to":1660828500000,"method":"L1","value":0},{"from":1660828500000,"to":1660829400000,"method":"L1","value":0},{"from":1660829400000,"to":1660830300000,"method":"L1","value":0},{"from":1660830300000,"to":1660831200000,"method":"L1","value":0},{"from":1660831200000,"to":1660832100000,"method":"L1","value":0},{"from":1660832100000,"to":1660833000000,"method":"L1","value":0},{"from":1660833000000,"to":1660833900000,"method":"L1","value":0.0035},{"from":1660833900000,"to":1660834800000,"method":"L1","value":0.00275},{"from":1660834800000,"to":1660835700000,"method":"L1","value":0},{"from":1660835700000,"to":1660836600000,"method":"L1","value":0},{"from":1660836600000,"to":1660837500000,"method":"L1","value":0},{"from":1660837500000,"to":1660838400000,"method":"L1","value":0},{"from":1660838400000,"to":1660839300000,"method":"L1","value":0},{"from":1660839300000,"to":1660840200000,"method":"L1","value":0},{"from":1660840200000,"to":1660841100000,"method":"L1","value":0},{"from":1660841100000,"to":1660842000000,"method":"L1","value":0},{"from":1660842000000,"to":1660842900000,"method":"L1","value":0},{"from":1660842900000,"to":1660843800000,"method":"L1","value":0},{"from":1660843800000,"to":1660844700000,"method":"L1","value":0.0015},{"from":1660844700000,"to":1660845600000,"method":"L1","value":0.00825},{"from":1660845600000,"to":1660846500000,"method":"L1","value":0.0075},{"from":1660846500000,"to":1660847400000,"method":"L1","value":0.00725},{"from":1660847400000,"to":1660848300000,"method":"L1","value":0.00675},{"from":1660848300000,"to":1660849200000,"method":"L1","value":0.0065},{"from":1660849200000,"to":1660850100000,"method":"L1","value":0.0075},{"from":1660850100000,"to":1660851000000,"method":"L1","value":0.006},{"from":1660851000000,"to":1660851900000,"method":"L1","value":0.008},{"from":1660851900000,"to":1660852800000,"method":"L1","value":0.0095},{"from":1660852800000,"to":1660853700000,"method":"L1","value":0.00975},{"from":1660853700000,"to":1660854600000,"method":"L1","value":0.00825},{"from":1660854600000,"to":1660855500000,"method":"L1","value":0.01},{"from":1660855500000,"to":1660856400000,"method":"L1","value":0.009},{"from":1660856400000,"to":1660857300000,"method":"L1","value":0.00625},{"from":1660857300000,"to":1660858200000,"method":"L1","value":0.00575},{"from":1660858200000,"to":1660859100000,"method":"L1","value":0.00625},{"from":1660859100000,"to":1660860000000,"method":"L1","value":0.006}]},
				{"meterCode":"1-1:2.9.0 G.02","value":[{"from":1660773600000,"to":1660774500000,"method":"L1","value":0},{"from":1660774500000,"to":1660775400000,"method":"L1","value":0},{"from":1660775400000,"to":1660776300000,"method":"L1","value":0},{"from":1660776300000,"to":1660777200000,"method":"L1","value":0},{"from":1660777200000,"to":1660778100000,"method":"L1","value":0},{"from":1660778100000,"to":1660779000000,"method":"L1","value":0},{"from":1660779000000,"to":1660779900000,"method":"L1","value":0},{"from":1660779900000,"to":1660780800000,"method":"L1","value":0},{"from":1660780800000,"to":1660781700000,"method":"L1","value":0},{"from":1660781700000,"to":1660782600000,"method":"L1","value":0},{"from":1660782600000,"to":1660783500000,"method":"L1","value":0},{"from":1660783500000,"to":1660784400000,"method":"L1","value":0},{"from":1660784400000,"to":1660785300000,"method":"L1","value":0},{"from":1660785300000,"to":1660786200000,"method":"L1","value":0},{"from":1660786200000,"to":1660787100000,"method":"L1","value":0},{"from":1660787100000,"to":1660788000000,"method":"L1","value":0},{"from":1660788000000,"to":1660788900000,"method":"L1","value":0},{"from":1660788900000,"to":1660789800000,"method":"L1","value":0},{"from":1660789800000,"to":1660790700000,"method":"L1","value":0},{"from":1660790700000,"to":1660791600000,"method":"L1","value":0},{"from":1660791600000,"to":1660792500000,"method":"L1","value":0},{"from":1660792500000,"to":1660793400000,"method":"L1","value":0},{"from":1660793400000,"to":1660794300000,"method":"L1","value":0},{"from":1660794300000,"to":1660795200000,"method":"L1","value":0},{"from":1660795200000,"to":1660796100000,"method":"L1","value":0},{"from":1660796100000,"to":1660797000000,"method":"L1","value":0},{"from":1660797000000,"to":1660797900000,"method":"L1","value":0},{"from":1660797900000,"to":1660798800000,"method":"L1","value":0},{"from":1660798800000,"to":1660799700000,"method":"L1","value":0},{"from":1660799700000,"to":1660800600000,"method":"L1","value":0},{"from":1660800600000,"to":1660801500000,"method":"L1","value":0},{"from":1660801500000,"to":1660802400000,"method":"L1","value":0},{"from":1660802400000,"to":1660803300000,"method":"L1","value":0},{"from":1660803300000,"to":1660804200000,"method":"L1","value":0},{"from":1660804200000,"to":1660805100000,"method":"L1","value":0},{"from":1660805100000,"to":1660806000000,"method":"L1","value":0.0005},{"from":1660806000000,"to":1660806900000,"method":"L1","value":0},{"from":1660806900000,"to":1660807800000,"method":"L1","value":0},{"from":1660807800000,"to":1660808700000,"method":"L1","value":0},{"from":1660808700000,"to":1660809600000,"method":"L1","value":0},{"from":1660809600000,"to":1660810500000,"method":"L1","value":0},{"from":1660810500000,"to":1660811400000,"method":"L1","value":0},{"from":1660811400000,"to":1660812300000,"method":"L1","value":0},{"from":1660812300000,"to":1660813200000,"method":"L1","value":0},{"from":1660813200000,"to":1660814100000,"method":"L1","value":0},{"from":1660814100000,"to":1660815000000,"method":"L1","value":0},{"from":1660815000000,"to":1660815900000,"method":"L1","value":0},{"from":1660815900000,"to":1660816800000,"method":"L1","value":0},{"from":1660816800000,"to":1660817700000,"method":"L1","value":0},{"from":1660817700000,"to":1660818600000,"method":"L1","value":0},{"from":1660818600000,"to":1660819500000,"method":"L1","value":0},{"from":1660819500000,"to":1660820400000,"method":"L1","value":0},{"from":1660820400000,"to":1660821300000,"method":"L1","value":0},{"from":1660821300000,"to":1660822200000,"method":"L1","value":0},{"from":1660822200000,"to":1660823100000,"method":"L1","value":0},{"from":1660823100000,"to":1660824000000,"method":"L1","value":0},{"from":1660824000000,"to":1660824900000,"method":"L1","value":0},{"from":1660824900000,"to":1660825800000,"method":"L1","value":0},{"from":1660825800000,"to":1660826700000,"method":"L1","value":0},{"from":1660826700000,"to":1660827600000,"method":"L1","value":0},{"from":1660827600000,"to":1660828500000,"method":"L1","value":0},{"from":1660828500000,"to":1660829400000,"method":"L1","value":0},{"from":1660829400000,"to":1660830300000,"method":"L1","value":0},{"from":1660830300000,"to":1660831200000,"method":"L1","value":0},{"from":1660831200000,"to":1660832100000,"method":"L1","value":0},{"from":1660832100000,"to":1660833000000,"method":"L1","value":0},{"from":1660833000000,"to":1660833900000,"method":"L1","value":0},{"from":1660833900000,"to":1660834800000,"method":"L1","value":0},{"from":1660834800000,"to":1660835700000,"method":"L1","value":0},{"from":1660835700000,"to":1660836600000,"method":"L1","value":0},{"from":1660836600000,"to":1660837500000,"method":"L1","value":0},{"from":1660837500000,"to":1660838400000,"method":"L1","value":0},{"from":1660838400000,"to":1660839300000,"method":"L1","value":0},{"from":1660839300000,"to":1660840200000,"method":"L1","value":0},{"from":1660840200000,"to":1660841100000,"method":"L1","value":0},{"from":1660841100000,"to":1660842000000,"method":"L1","value":0},{"from":1660842000000,"to":1660842900000,"method":"L1","value":0},{"from":1660842900000,"to":1660843800000,"method":"L1","value":0},{"from":1660843800000,"to":1660844700000,"method":"L1","value":0},{"from":1660844700000,"to":1660845600000,"method":"L1","value":0},{"from":1660845600000,"to":1660846500000,"method":"L1","value":0},{"from":1660846500000,"to":1660847400000,"method":"L1","value":0},{"from":1660847400000,"to":1660848300000,"method":"L1","value":0},{"from":1660848300000,"to":1660849200000,"method":"L1","value":0},{"from":1660849200000,"to":1660850100000,"method":"L1","value":0},{"from":1660850100000,"to":1660851000000,"method":"L1","value":0},{"from":1660851000000,"to":1660851900000,"method":"L1","value":0},{"from":1660851900000,"to":1660852800000,"method":"L1","value":0},{"from":1660852800000,"to":1660853700000,"method":"L1","value":0},{"from":1660853700000,"to":1660854600000,"method":"L1","value":0},{"from":1660854600000,"to":1660855500000,"method":"L1","value":0},{"from":1660855500000,"to":1660856400000,"method":"L1","value":0},{"from":1660856400000,"to":1660857300000,"method":"L1","value":0},{"from":1660857300000,"to":1660858200000,"method":"L1","value":0},{"from":1660858200000,"to":1660859100000,"method":"L1","value":0},{"from":1660859100000,"to":1660860000000,"method":"L1","value":0}]},
				{"meterCode":"1-1:2.9.0 G.03","value":[{"from":1660773600000,"to":1660774500000,"method":"L1","value":0},{"from":1660774500000,"to":1660775400000,"method":"L1","value":0},{"from":1660775400000,"to":1660776300000,"method":"L1","value":0},{"from":1660776300000,"to":1660777200000,"method":"L1","value":0},{"from":1660777200000,"to":1660778100000,"method":"L1","value":0},{"from":1660778100000,"to":1660779000000,"method":"L1","value":0},{"from":1660779000000,"to":1660779900000,"method":"L1","value":0},{"from":1660779900000,"to":1660780800000,"method":"L1","value":0},{"from":1660780800000,"to":1660781700000,"method":"L1","value":0},{"from":1660781700000,"to":1660782600000,"method":"L1","value":0},{"from":1660782600000,"to":1660783500000,"method":"L1","value":0},{"from":1660783500000,"to":1660784400000,"method":"L1","value":0},{"from":1660784400000,"to":1660785300000,"method":"L1","value":0},{"from":1660785300000,"to":1660786200000,"method":"L1","value":0},{"from":1660786200000,"to":1660787100000,"method":"L1","value":0},{"from":1660787100000,"to":1660788000000,"method":"L1","value":0},{"from":1660788000000,"to":1660788900000,"method":"L1","value":0},{"from":1660788900000,"to":1660789800000,"method":"L1","value":0},{"from":1660789800000,"to":1660790700000,"method":"L1","value":0},{"from":1660790700000,"to":1660791600000,"method":"L1","value":0},{"from":1660791600000,"to":1660792500000,"method":"L1","value":0},{"from":1660792500000,"to":1660793400000,"method":"L1","value":0},{"from":1660793400000,"to":1660794300000,"method":"L1","value":0},{"from":1660794300000,"to":1660795200000,"method":"L1","value":0},{"from":1660795200000,"to":1660796100000,"method":"L1","value":0},{"from":1660796100000,"to":1660797000000,"method":"L1","value":0},{"from":1660797000000,"to":1660797900000,"method":"L1","value":0},{"from":1660797900000,"to":1660798800000,"method":"L1","value":0},{"from":1660798800000,"to":1660799700000,"method":"L1","value":0},{"from":1660799700000,"to":1660800600000,"method":"L1","value":0},{"from":1660800600000,"to":1660801500000,"method":"L1","value":0},{"from":1660801500000,"to":1660802400000,"method":"L1","value":0},{"from":1660802400000,"to":1660803300000,"method":"L1","value":0},{"from":1660803300000,"to":1660804200000,"method":"L1","value":0},{"from":1660804200000,"to":1660805100000,"method":"L1","value":0},{"from":1660805100000,"to":1660806000000,"method":"L1","value":0.0005},{"from":1660806000000,"to":1660806900000,"method":"L1","value":0},{"from":1660806900000,"to":1660807800000,"method":"L1","value":0},{"from":1660807800000,"to":1660808700000,"method":"L1","value":0},{"from":1660808700000,"to":1660809600000,"method":"L1","value":0},{"from":1660809600000,"to":1660810500000,"method":"L1","value":0},{"from":1660810500000,"to":1660811400000,"method":"L1","value":0},{"from":1660811400000,"to":1660812300000,"method":"L1","value":0},{"from":1660812300000,"to":1660813200000,"method":"L1","value":0},{"from":1660813200000,"to":1660814100000,"method":"L1","value":0},{"from":1660814100000,"to":1660815000000,"method":"L1","value":0},{"from":1660815000000,"to":1660815900000,"method":"L1","value":0},{"from":1660815900000,"to":1660816800000,"method":"L1","value":0},{"from":1660816800000,"to":1660817700000,"method":"L1","value":0},{"from":1660817700000,"to":1660818600000,"method":"L1","value":0},{"from":1660818600000,"to":1660819500000,"method":"L1","value":0},{"from":1660819500000,"to":1660820400000,"method":"L1","value":0},{"from":1660820400000,"to":1660821300000,"method":"L1","value":0},{"from":1660821300000,"to":1660822200000,"method":"L1","value":0},{"from":1660822200000,"to":1660823100000,"method":"L1","value":0},{"from":1660823100000,"to":1660824000000,"method":"L1","value":0},{"from":1660824000000,"to":1660824900000,"method":"L1","value":0},{"from":1660824900000,"to":1660825800000,"method":"L1","value":0},{"from":1660825800000,"to":1660826700000,"method":"L1","value":0},{"from":1660826700000,"to":1660827600000,"method":"L1","value":0},{"from":1660827600000,"to":1660828500000,"method":"L1","value":0},{"from":1660828500000,"to":1660829400000,"method":"L1","value":0},{"from":1660829400000,"to":1660830300000,"method":"L1","value":0},{"from":1660830300000,"to":1660831200000,"method":"L1","value":0},{"from":1660831200000,"to":1660832100000,"method":"L1","value":0},{"from":1660832100000,"to":1660833000000,"method":"L1","value":0},{"from":1660833000000,"to":1660833900000,"method":"L1","value":0},{"from":1660833900000,"to":1660834800000,"method":"L1","value":0},{"from":1660834800000,"to":1660835700000,"method":"L1","value":0},{"from":1660835700000,"to":1660836600000,"method":"L1","value":0},{"from":1660836600000,"to":1660837500000,"method":"L1","value":0},{"from":1660837500000,"to":1660838400000,"method":"L1","value":0},{"from":1660838400000,"to":1660839300000,"method":"L1","value":0},{"from":1660839300000,"to":1660840200000,"method":"L1","value":0},{"from":1660840200000,"to":1660841100000,"method":"L1","value":0},{"from":1660841100000,"to":1660842000000,"method":"L1","value":0},{"from":1660842000000,"to":1660842900000,"method":"L1","value":0},{"from":1660842900000,"to":1660843800000,"method":"L1","value":0},{"from":1660843800000,"to":1660844700000,"method":"L1","value":0},{"from":1660844700000,"to":1660845600000,"method":"L1","value":0},{"from":1660845600000,"to":1660846500000,"method":"L1","value":0},{"from":1660846500000,"to":1660847400000,"method":"L1","value":0},{"from":1660847400000,"to":1660848300000,"method":"L1","value":0},{"from":1660848300000,"to":1660849200000,"method":"L1","value":0},{"from":1660849200000,"to":1660850100000,"method":"L1","value":0},{"from":1660850100000,"to":1660851000000,"method":"L1","value":0},{"from":1660851000000,"to":1660851900000,"method":"L1","value":0},{"from":1660851900000,"to":1660852800000,"method":"L1","value":0},{"from":1660852800000,"to":1660853700000,"method":"L1","value":0},{"from":1660853700000,"to":1660854600000,"method":"L1","value":0},{"from":1660854600000,"to":1660855500000,"method":"L1","value":0},{"from":1660855500000,"to":1660856400000,"method":"L1","value":0},{"from":1660856400000,"to":1660857300000,"method":"L1","value":0},{"from":1660857300000,"to":1660858200000,"method":"L1","value":0},{"from":1660858200000,"to":1660859100000,"method":"L1","value":0},{"from":1660859100000,"to":1660860000000,"method":"L1","value":0}]}
			]
		}
	]
}`
	msg := model.SubscribeMessage{
		MessageCode: model.EBMS_ENERGY_FILE_RESPONSE,
		Protocol:    model.CR_MSG,
		Tenant:      "TE1000001",
		Payload:     model.EbmsMessage{},
	}
	err := json.Unmarshal([]byte(jsonString), &msg.Payload)
	require.NoError(t, err)

	//historyValue := map[string]interface{}{"meter": msg.Payload.Meter.MeteringPoint, "from": msg.Payload.Energy[0].Start, "to": msg.Payload.Energy[0].End}
	//recorder.Mock.On(
	//	"saveHistory", mock.Anything, "TE1000001", model.EBMS_ENERGY_FILE_RESPONSE, "AT003000202208191420233640008300242", "ADMIN", "IN", model.CR_MSG, historyValue).Return(nil)

	protocolCrMsgHandler(context.Background(), msg)
	//recorder.AssertExpectations(t)
}

func TestProtocolCrReqPtHandler(t *testing.T) {
	type test struct {
		name        string
		message     string
		codes       []int16
		messageType model.EbMsMessageType
	}

	tests := []test{
		{
			name:        "Antwort",
			message:     `{"conversationId":"AT003000202208191420233640008300242","messageId":"AT003000202308140722134490185248575","sender":"AT003000","receiver":"RC100298","messageCode":"ANTWORT_PT","meter":{"meteringPoint":"AT0030000000000000000000000446232","direction":"CONSUMPTION"},"responseData":[{"responseCode":[70]}]}`,
			codes:       []int16{70},
			messageType: model.EBMS_ZP_RES,
		},
		{
			name:        "Ablehnung",
			message:     `{"conversationId":"AT003000202208191420233640008300242","messageId":"AT003000202308140722134490185248575","sender":"AT003000","receiver":"RC100298","messageCode":"ABLEHNUNG_PT","meter":{"meteringPoint":"AT0030000000000000000000000446232","direction":"CONSUMPTION"},"responseData":[{"responseCode":[56]}]}`,
			codes:       []int16{56},
			messageType: model.EBMS_ZP_REJ,
		},
		{
			name:        "Anforderung",
			message:     `{"conversationId":"AT003000202208191420233640008300242","messageId":"RC100298202308141691990530000000319","sender":"RC100298","receiver":"AT003000","messageCode":"ANFORDERUNG_PT","requestId":"JOVM6US5","meter":{"meteringPoint":"AT0030000000000000000000000446232","direction":"CONSUMPTION"},"timeline":{"from":1691445600000,"to":1691703900000}}`,
			codes:       []int16{},
			messageType: model.EBMS_ZP_SYNC,
		},
	}

	for _, m := range tests {
		t.Run(m.name, func(t *testing.T) {
			msg := model.SubscribeMessage{
				MessageCode: m.messageType,
				Protocol:    model.CR_REQ_PT,
				Tenant:      "TE1000001",
				Payload:     model.EbmsMessage{},
			}
			err := json.Unmarshal([]byte(m.message), &msg.Payload)
			require.NoError(t, err)

			//recorder.Mock.On("saveNotification", mock.Anything, "TE1000001", msg.MessageCode, msg.Payload.Meters(), m.codes, model.CR_REQ_PT).Return(nil)
			//// saveNotification(db *sqlx.DB, tenant string, code model.EbMsMessageType, meters []string, errCodes []int16, protocol model.EdaProtocol)
			//
			//recorder.Mock.On("saveHistory", mock.Anything, "TE1000001", msg.MessageCode, "AT003000202208191420233640008300242", "ADMIN", "IN", msg.Protocol, msg.Payload).Return(nil)

			protocolCrReqPtHandler(context.Background(), msg)
			//recorder.AssertExpectations(t)
		})
	}
}

func TestProtocolEcReqOnlHandler(t *testing.T) {

	type test struct {
		name          string
		msg           string
		meterId       string
		expectedState model.StatusType
		prepareMsg    func(t *testing.T, msg string) model.SubscribeMessage
		check         func(t *testing.T, m *model.MeteringPoint)
	}

	meterId := "AT0030000000000000000000030041724"

	tests := []test{
		{
			name:          "Anforderung",
			msg:           `{"conversationId":"RC100104202408221102047770000147214","messageId":"RC100104202408221102047770000147213","sender":"RC100104","receiver":"AT002000","messageCode":"ANFORDERUNG_ECON","messageCodeVersion":"02.00","requestId":"2T4h42Q","meter":{"meteringPoint":"AT0020000000000000000000100437855","direction":"CONSUMPTION","partFact":100},"ecId":"AT00300000000TC000015000000000001"}`,
			meterId:       meterId,
			expectedState: model.S_INIT,
			prepareMsg: func(t *testing.T, msg string) model.SubscribeMessage {
				codes := []int16{0}
				m, err := prepareNotificationMessage(
					msg,
					model.EBMS_ONLINE_REG_INIT,
					model.EC_REQ_ONL,
					"TE000002",
					"AT00300000000TC000002000000000001",
					codes, nil, &model.Meter{
						MeteringPoint: meterId,
						Direction:     "CONSUMPTION",
					})
				require.NoError(t, err)
				return m
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				require.Equal(t, meterId, m.MeteringPoint)
				assert.Nil(t, m.State.ActiveSince.Ptr())
				assert.Nil(t, m.State.InactiveSince.Ptr())
				//assert.Equal(t, civil.DateFor(2025, 4, 24), m.State.InactiveSince.Date)
				assert.Equal(t, model.INIT, m.ProcessState)
				assert.Equal(t, model.S_INIT, m.Status)
			},
		},
		{
			name:          "Zustimmung",
			msg:           `{"conversationId":"RC102925202505291748545670000769433","messageId":"AT003200202506052018051920000038938","sender":"AT003200","receiver":"RC102925","messageCode":"ZUSTIMMUNG_ECON","messageCodeVersion":"01.12","requestId":"ScYyHPx","ecId":"AT00300000000TC000015000000000001","responseData":[{"meteringPoint":"AT0032000000000000000000000011490","responseCode":[175]}]}`,
			meterId:       meterId,
			expectedState: model.S_INIT,
			prepareMsg: func(t *testing.T, m string) model.SubscribeMessage {
				codes := []int16{CONSENT_GRANTED}
				msg, err := prepareNotificationMessage(
					m,
					model.EBMS_ONLINE_REG_APPROVAL,
					model.EC_REQ_ONL,
					"TE000002",
					"AT00300000000TC000002000000000001",
					codes, nil, &model.Meter{
						MeteringPoint: meterId,
						Direction:     "CONSUMPTION",
					})
				require.NoError(t, err)
				return msg
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				require.Equal(t, meterId, m.MeteringPoint)
			},
		},
		{
			name:          "Zustimmung with consent-Id",
			msg:           `{"conversationId":"RC102925202505291748545670000769433","messageId":"AT003200202506052018051920000038938","sender":"AT003200","receiver":"RC102925","messageCode":"ZUSTIMMUNG_ECON","messageCodeVersion":"01.12","requestId":"ScYyHPx","ecId":"AT00300000000TC000015000000000001","responseData":[{"meteringPoint":"AT0032000000000000000000000011490","responseCode":[175],"consentId":"AT003200202506052017576180000052415"}]}`,
			meterId:       meterId,
			expectedState: model.S_INIT,
			prepareMsg: func(t *testing.T, m string) model.SubscribeMessage {
				codes := []int16{CONSENT_GRANTED}
				consentId := "1726617600000"
				msg, err := prepareNotificationMessage(
					m,
					model.EBMS_ONLINE_REG_APPROVAL,
					model.EC_REQ_ONL,
					"TE000002",
					"AT00300000000TC000002000000000001",
					codes, &consentId, &model.Meter{
						MeteringPoint: meterId,
						Direction:     "CONSUMPTION",
					})
				require.NoError(t, err)
				return msg
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				require.Equal(t, meterId, m.MeteringPoint)
			},
		},
		{
			name:          "Antwort",
			msg:           `{"conversationId":"RC102728202506051749149940000821663","messageId":"AT003000202506052059070020432540055","sender":"AT003000","receiver":"RC102728","messageCode":"ANTWORT_ECON","messageCodeVersion":"01.12","requestId":"NEpTmuH","ecId":"AT00300000000TC000015000000000001","responseData":[{"meteringPoint":"AT0030000000000000000000000350193","responseCode":[99]}]}`,
			meterId:       meterId,
			expectedState: model.S_INIT,
			prepareMsg: func(t *testing.T, m string) model.SubscribeMessage {
				codes := []int16{MESSAGE_RECEIVED}
				msg, err := prepareNotificationMessage(
					m,
					model.EBMS_ONLINE_REG_REJECTION,
					model.EC_REQ_ONL,
					"TE000002",
					"AT00300000000TC000002000000000001",
					codes, nil, &model.Meter{
						MeteringPoint: meterId,
						Direction:     "CONSUMPTION",
					})
				require.NoError(t, err)
				return msg
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				require.Equal(t, meterId, m.MeteringPoint)
			},
		},
		{
			name:          "Abschluss",
			msg:           `{"conversationId":"RC102925202505291748545670000769433","messageId":"AT003200202506052023341700000031909","sender":"AT003200","receiver":"RC102925","messageCode":"ABSCHLUSS_ECON","messageCodeVersion":"02.00","ecId":"AT00300000000TC000015000000000001","meterList":[{"meteringPoint":"AT0032000000000000000000000011490","direction":"CONSUMPTION","activation":1749160800000,"partFact":100,"from":1749160800000,"to":253402210800000,"consentId":"AT002000202504071118158864B2hYwr"}]}`,
			meterId:       meterId,
			expectedState: model.S_INIT,
			prepareMsg: func(t *testing.T, m string) model.SubscribeMessage {
				msg, err := prepareNotificationMessage(
					m,
					model.EBMS_ONLINE_REG_COMPLETION,
					model.EC_REQ_ONL,
					"TE000002",
					"AT00300000000TC000002000000000001",
					[]int16{}, nil, &model.Meter{
						MeteringPoint: meterId,
						Direction:     "CONSUMPTION",
					})
				require.NoError(t, err)
				return msg
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				require.Equal(t, meterId, m.MeteringPoint)
			},
		},
		{
			name:          "Ablehnung",
			meterId:       meterId,
			msg:           `{"conversationId":"RC100178202502081739045030000124643","messageId":"AT002000202502082103535826923380853","sender":"AT002000","receiver":"RC100178","messageCode":"ABLEHNUNG_ECON","messageCodeVersion":"01.11","requestId":"CES8r3q","ecId":"AT00200000000RC100178000000000208","responseData":[{"meteringPoint":"AT0020000000000000000000100280485","responseCode":[156]}]}`,
			expectedState: model.S_INIT,
			prepareMsg: func(t *testing.T, m string) model.SubscribeMessage {
				codes := []int16{104}
				msg, err := prepareNotificationMessage(
					m,
					model.EBMS_ONLINE_REG_REJECTION,
					model.EC_REQ_ONL,
					"TE000002",
					"AT00300000000TC000002000000000001",
					codes, nil, &model.Meter{
						MeteringPoint: meterId,
						Direction:     "CONSUMPTION",
					})
				require.NoError(t, err)
				return msg
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				require.Equal(t, meterId, m.MeteringPoint)
				assert.Nil(t, m.State.ActiveSince.Ptr())
				assert.Nil(t, m.State.InactiveSince.Ptr())
				//assert.Equal(t, civil.DateFor(2025, 4, 24), m.State.InactiveSince.Date)
				assert.Equal(t, model.INIT, m.ProcessState)
				assert.Equal(t, model.S_INIT, m.Status)
			},
		},
		{
			name:          "Ablehnung - competing process",
			msg:           `{"conversationId":"RC100178202502081739045030000124643","messageId":"AT002000202502082103535826923380853","sender":"AT002000","receiver":"RC100178","messageCode":"ABLEHNUNG_ECON","messageCodeVersion":"01.11","requestId":"CES8r3q","ecId":"AT00200000000RC100178000000000208","responseData":[{"meteringPoint":"AT0020000000000000000000100280485","responseCode":[156]}]}`,
			meterId:       meterId,
			expectedState: model.S_INIT,
			prepareMsg: func(t *testing.T, m string) model.SubscribeMessage {
				codes := []int16{PARTICIPATION_FACTOR_OF_100_WOULD_BE_EXCEEDED, COMPETING_PROCESSES}
				msg, err := prepareNotificationMessage(
					m,
					model.EBMS_ONLINE_REG_REJECTION,
					model.EC_REQ_ONL,
					"TE000002",
					"AT00300000000TC000002000000000001",
					codes, nil, &model.Meter{
						MeteringPoint: meterId,
						Direction:     "CONSUMPTION",
					})
				require.NoError(t, err)
				return msg
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				require.Equal(t, meterId, m.MeteringPoint)
			},
		},
	}

	ctx := context.Background()
	db, err := database.GetDB(ctx)
	require.NoError(t, err)

	for _, m := range tests {
		t.Run(m.name, func(t *testing.T) {
			msg := m.prepareMsg(t, m.msg)
			protocolEcReqOnlHandler(ctx, msg)

			meter, err := db.FindMeteringByStatus(ctx, msg.Tenant, m.meterId, m.expectedState)
			require.NoError(t, err)
			m.check(t, meter)
		})
	}
}

func TestProtocolEcReqOnlHandler1(t *testing.T) {

	type test struct {
		name    string
		message model.SubscribeMessage
	}

	tests := []test{
		{
			name:    "Ablehnung - competing process",
			message: prepareMessage(t, model.EBMS_ONLINE_REG_REJECTION, []int16{PARTICIPATION_FACTOR_OF_100_WOULD_BE_EXCEEDED, COMPETING_PROCESSES}),
		},
	}

	ctx := context.Background()
	for _, m := range tests {
		t.Run(m.name, func(t *testing.T) {
			protocolEcReqOnlHandler(ctx, m.message)
		})
	}
}

func TestProtocolCmRevImpHandler(t *testing.T) {
	type test struct {
		name        string
		message     string
		codes       []int16
		messageType model.EbMsMessageType
	}

	tests := []test{
		{
			name:        "Aufhebung CCMI",
			message:     `{"conversationId":"AT003000202403310311592520011775087","messageId":"AT003000202403310311592520262459850","sender":"AT003000","receiver":"RC100181","messageCode":"AUFHEBUNG_CCMI","responseData":[{"meteringPoint":"AT0030000000000000000000030042666","responseCode":[1099],"consentEnd":1720994400000}]}`,
			codes:       []int16{1099},
			messageType: model.EBMS_AUFHEBUNG_CCMI,
		},
	}
	for _, m := range tests {
		t.Run(m.name, func(t *testing.T) {
			//jsonString := `{"conversationId":"RC100298202308171692252620000000321","messageId":"AT003000202308170810324070187796715","sender":"AT003000","receiver":"RC100298","messageCode":"ZUSTIMMUNG_ECON","requestId":"XV3VFJN2","responseData":[{"meteringPoint":"AT0030000000000000000000000459143","responseCode":[175]}]}`
			msg := model.SubscribeMessage{
				MessageCode: m.messageType,
				Protocol:    model.CM_REV_IMP,
				Tenant:      "TE1000001",
				Payload:     model.EbmsMessage{},
			}
			err := json.Unmarshal([]byte(m.message), &msg.Payload)
			require.NoError(t, err)

			//mockDb.Mock.ExpectBegin()
			//stmt := "SELECT (.+) FROM \"base\".\"eeg\" WHERE (.+)"
			//rows := sqlmock.NewRows([]string{"tenant", "name", "description", "businessNr", "legal", "gridoperator_name", "communityId", "gridoperator_code", "rcNumber", "area", "allocationMode",
			//	"settlementInterval", "providerBusinessNr", "street", "streetNumber", "zip", "city", "phone", "email", "website", "iban", "owner", "sepa", "bankName",
			//	"taxNumber", "vatNumber", "online", "contactPerson"}).
			//	AddRow("TE1000001", "test-eeg", "", "", "verein", "Netz-Test", "CC00000000000002221212121212", "EE000001", "RC100130",
			//		"LOCAL", "DYNAMIC", "MONTHLY", 0, "Solargasse", "1", "1111", "Solarcity", "", "", "", "", "Max Mustermann", false, "Bankname", "", "", false, "Max Mustermann")
			//mockDb.Mock.ExpectQuery(stmt).WillReturnRows(rows)
			//
			//paRows := sqlmock.NewRows([]string{"participantNumber", "firstname", "lastname", "role", "businessRole", "titleBefore", "titleAfter", "participantSince",
			//	"vatNumber", "taxNumber", "status", "createdBy"}).
			//	AddRow("001", "Max", "Mustermann", "EEG_USER", "EEG_PRIVATE", "", "", time.Date(2024, 1, 1, 0, 0, 0, 0, time.Local),
			//		"1234", "5678", "ACTIVE", "test")
			//mockDb.Mock.ExpectQuery(`SELECT (.+) FROM "base"."participant" WHERE (.+)`).WillReturnRows(paRows)
			//mockDb.Mock.ExpectQuery(`SELECT (.+) FROM "base"."contactdetail" WHERE (.+)`).WillReturnRows(sqlmock.NewRows([]string{"phone"}).AddRow("11"))
			//mockDb.Mock.ExpectQuery(`SELECT (.+) FROM "base"."bankaccount" WHERE (.+)`).WillReturnRows(sqlmock.NewRows([]string{"iban"}).AddRow("11"))
			//mockDb.Mock.ExpectQuery(`SELECT (.+) FROM "base"."address" WHERE (.+)`).WillReturnRows(sqlmock.NewRows([]string{"type"}).AddRow("11"))
			//mockDb.Mock.ExpectQuery(`SELECT (.+) FROM "base"."address" WHERE (.+)`).WillReturnRows(sqlmock.NewRows([]string{"type"}).AddRow("11"))
			//mockDb.Mock.ExpectQuery(`SELECT (.+) FROM "base"."meteringpoint"(.+)`).WillReturnRows(sqlmock.NewRows([]string{"metering_point_id"}).AddRow("1212"))

			//mockDb.Mock.ExpectExec("UPDATE (.+)").WillReturnResult(sqlmock.NewResult(1, 1))

			//recorder.Mock.On("saveNotification", map[string]interface{}{
			//	"type":           msg.MessageCode,
			//	"meteringPoints": []string{"AT0030000000000000000000030042666"},
			//	"responseCodes":  m.codes,
			//}, msg.Tenant, "EDA_PROCESS", "ADMIN").Return(nil)
			//recorder.Mock.On("saveNotification", mock.Anything, "TE1000001", msg.MessageCode, []string{"AT0030000000000000000000030042666"}, m.codes, model.CM_REV_IMP).Return(nil)
			//
			//recorder.Mock.On("saveHistory", mock.Anything, "TE1000001", msg.MessageCode, "AT003000202403310311592520011775087", "ADMIN", "IN", model.CM_REV_IMP, msg.Payload).Return(nil)

			protocolCmRevImpHandler(context.Background(), msg)
			//recorder.AssertExpectations(t)

		})
	}

}

// Revocations that cannot be applied, and answers without code 176, only go to the history of the
// community: no notification, no change of the metering point.
func TestProtocolCmRevImpHandler_historyOnly(t *testing.T) {
	tests := []struct {
		name     string
		msg      string
		msgType  model.EbMsMessageType
		msgProto model.EdaProtocol
	}{
		{
			// the metering point is only in TE000015, with another consent id; the receiving
			// community TE000016 must not revoke it
			name:     "Aufhebung CCMI - not found in receiving community",
			msg:      `{"conversationId":"AT003000202610090000000000000000901","messageId":"AT003000202610090000000000000000902","sender":"AT003000","receiver":"RC100181","messageCode":"AUFHEBUNG_CCMI","responseData":[{"meteringPoint":"AT0030000000000000000000000153014","responseCode":[1099],"consentEnd":1759269600000,"consentId":"CONSENT-UNKNOWN"}]}`,
			msgType:  model.EBMS_AUFHEBUNG_CCMI,
			msgProto: model.CM_REV_IMP,
		},
		{
			name:     "Antwort CCMS - without 176",
			msg:      `{"conversationId":"AT003000202610090000000000000000903","messageId":"AT003000202610090000000000000000904","sender":"AT003000","receiver":"RC100181","messageCode":"ANTWORT_CCMS","ecId":"AT00300000000TC000016000000000001","responseData":[{"meteringPoint":"AT0030000000000000000000000153014","responseCode":[99]}],"consentEnd":1759269600000}`,
			msgType:  model.EBMS_ANTWORT_CCMS,
			msgProto: model.CM_REV_SP,
		},
	}

	count := func(t *testing.T, query string, args ...interface{}) int {
		var n int
		require.NoError(t, testDB.DbInstance.Get(&n, query, args...))
		return n
	}
	const meterState = `SELECT process_state FROM base.meteringpoint WHERE tenant = 'TE000015' AND metering_point_id = 'AT0030000000000000000000000153014'`

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := model.SubscribeMessage{
				MessageCode: tt.msgType,
				Protocol:    tt.msgProto,
				Tenant:      "te000016", // MQTT topic, lower case
				Payload:     model.EbmsMessage{},
			}
			require.NoError(t, json.Unmarshal([]byte(tt.msg), &msg.Payload))
			t.Cleanup(func() {
				_, err := testDB.DbInstance.Exec(`DELETE FROM base.processhistory WHERE "conversationId" = $1`, msg.Payload.ConversationId)
				require.NoError(t, err)
			})

			notifications := count(t, `SELECT count(*) FROM base.notification WHERE tenant = 'TE000016'`)
			var stateBefore string
			require.NoError(t, testDB.DbInstance.Get(&stateBefore, meterState))

			protocolCmRevImpHandler(context.Background(), msg)

			assert.Equal(t, 1, count(t, `SELECT count(*) FROM base.processhistory WHERE tenant = 'TE000016' AND "conversationId" = $1`, msg.Payload.ConversationId))
			assert.Equal(t, notifications, count(t, `SELECT count(*) FROM base.notification WHERE tenant = 'TE000016'`))
			var stateAfter string
			require.NoError(t, testDB.DbInstance.Get(&stateAfter, meterState))
			assert.Equal(t, stateBefore, stateAfter)
		})
	}
}

// GEA: the MQTT topic carries the RC number, which owns several tenants. A revocation reaches the
// notification and history of every tenant of that RC number — also when it cannot be applied.
func TestProtocolCmRevImpHandler_gea(t *testing.T) {
	const meter = "AT0030000000000000000000000153099"
	cleanupCommunities(t, "GC000030-001", "GC000030-002")
	for _, tenant := range []string{"GC000030-001", "GC000030-002"} {
		_, err := testDB.DbInstance.Exec(`INSERT INTO base.eeg
			(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
			 street, "streetNumber", city, zip, email)
			VALUES ($1, 'GEA-TEST', 'GEA-Test', 'GC000030', 'LOCAL', 'AT003000', 'Netz OÖ', $2,
			 'Weg', '1', 'Linz', '4020', 'gea-test@example.org')`, tenant, "AT00300000000TC0000300000000"+tenant[9:])
		require.NoError(t, err)
		_, err = testDB.DbInstance.Exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction,
			status, process_state, "modifiedAt", "modifiedBy", "registeredSince", activesince, inactivesince, consent_id)
			VALUES ($1, 'ea1142dc-03da-15ee-b82b-5a985b4b033a', $2, 'CONSUMPTION', 'ACTIVE', 'ACTIVE', now(), 'test',
			 '2025-01-01', '2025-01-01', '2999-12-31', 'CONSENT-GEA')`, meter, tenant)
		require.NoError(t, err)
	}
	count := func(query string, args ...interface{}) int {
		var n int
		require.NoError(t, testDB.DbInstance.Get(&n, query, args...))
		return n
	}
	revoke := func(conversationId, consentId string) {
		msg := model.SubscribeMessage{MessageCode: model.EBMS_AUFHEBUNG_CCMI, Protocol: model.CM_REV_IMP, Tenant: "gc000030"}
		require.NoError(t, json.Unmarshal([]byte(`{"conversationId":"`+conversationId+`","sender":"AT003000","receiver":"GC000030","messageCode":"AUFHEBUNG_CCMI","responseData":[{"meteringPoint":"`+meter+`","responseCode":[1099],"consentEnd":1759269600000,"consentId":"`+consentId+`"}]}`), &msg.Payload))
		protocolCmRevImpHandler(context.Background(), msg)
	}
	notifications := func() int {
		return count(`SELECT count(*) FROM base.notification WHERE tenant IN ('GC000030-001', 'GC000030-002')`)
	}

	before := notifications()
	revoke("AT003000202610090000000000000000911", "CONSENT-GEA")
	assert.Equal(t, 2, count(`SELECT count(*) FROM base.meteringpoint WHERE metering_point_id = $1 AND process_state = 'INACTIVE'`, meter))
	assert.Equal(t, 2, count(`SELECT count(DISTINCT tenant) FROM base.processhistory WHERE "conversationId" = 'AT003000202610090000000000000000911'`))
	assert.Equal(t, before+2, notifications())

	// not applicable (unknown consent, rows already carry one): history in both tenants, no notification
	before = notifications()
	revoke("AT003000202610090000000000000000912", "CONSENT-OTHER")
	assert.Equal(t, 2, count(`SELECT count(DISTINCT tenant) FROM base.processhistory WHERE "conversationId" = 'AT003000202610090000000000000000912'`))
	assert.Equal(t, before, notifications())
}

func TestMeteringPointRevokeActivationFlow(t *testing.T) {

	tests := []struct {
		name          string
		msg           string
		msgType       model.EbMsMessageType
		msgProto      model.EdaProtocol
		expectedState model.StatusType
		meter         string
		invoke        func(msg model.SubscribeMessage)
		check         func(t *testing.T, m *model.MeteringPoint)
	}{
		{
			// sending the deregistration leaves the metering point as it is (platform#238)
			name:          "Revoke - CCMS sent",
			msg:           `{"conversationId":"RC101607202504231745391480000487069","messageId":"RC101607202504231745391480000487068","sender":"RC101607","receiver":"AT004000","messageCode":"AUFHEBUNG_CCMS","messageCodeVersion":"","requestId":"JEZ6SG7","meter":{"meteringPoint":"AT0040000520100000000000010295156","consentId":"123456789015"},"ecId":"AT00300000000TC000015000000000001","consentEnd":1745445600000}`,
			msgType:       model.EBMS_AUFHEBUNG_CCMS,
			msgProto:      model.CM_REV_SP,
			meter:         "AT0030000000000000000000000153013",
			expectedState: model.S_ACTIVE,
			invoke: func(msg model.SubscribeMessage) {
				protocolCmRevImpHandler(context.Background(), msg)
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				assert.Equal(t, "AT0030000000000000000000000153013", m.MeteringPoint)
				assert.Equal(t, civil.DateFor(2023, 01, 01), m.State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2999, 12, 31), m.State.InactiveSince.Date)
				assert.Equal(t, model.ACTIVE, m.ProcessState)
				assert.Equal(t, model.S_ACTIVE, m.Status)
			},
		},
		{
			// the grid operator confirms: inactive since the consent end of the request
			name:          "Revoke - CCMS confirmed",
			msg:           `{"conversationId":"RC101607202504231745391480000487069","messageId":"AT004000202504231745391480000487070","sender":"AT004000","receiver":"RC101607","messageCode":"ANTWORT_CCMS","messageCodeVersion":"01.12","requestId":"JEZ6SG7","ecId":"AT00300000000TC000015000000000001","responseData":[{"meteringPoint":"AT0040000520100000000000010295156","responseCode":[176],"consentId":"123456789015"}],"consentEnd":1745445600000}`,
			msgType:       model.EBMS_ANTWORT_CCMS,
			msgProto:      model.CM_REV_SP,
			meter:         "AT0030000000000000000000000153013",
			expectedState: model.S_INACTIVE,
			invoke: func(msg model.SubscribeMessage) {
				protocolCmRevImpHandler(context.Background(), msg)
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				assert.Equal(t, "AT0030000000000000000000000153013", m.MeteringPoint)
				assert.Equal(t, civil.DateFor(2023, 01, 01), m.State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2025, 4, 24), m.State.InactiveSince.Date)
				assert.Equal(t, model.INACTIVE, m.ProcessState)
				assert.Equal(t, model.S_INACTIVE, m.Status)
			},
		},
		{
			name:          "Activate - Anforderung",
			msg:           `{"conversationId":"RC100104202408221102047770000147214","messageId":"RC100104202408221102047770000147213","sender":"RC100104","receiver":"AT002000","messageCode":"ANFORDERUNG_ECON","messageCodeVersion":"02.00","requestId":"2T4h42Q","meter":{"meteringPoint":"AT0020000000000000000000100437855","direction":"CONSUMPTION","partFact":100},"ecId":"AT00300000000TC000015000000000001"}`,
			msgType:       model.EBMS_ONLINE_REG_INIT,
			msgProto:      model.EC_REQ_ONL,
			meter:         "AT0030000000000000000000000153013",
			expectedState: model.S_INACTIVE,
			invoke: func(msg model.SubscribeMessage) {
				protocolEcReqOnlHandler(context.Background(), msg)
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				assert.Equal(t, "AT0030000000000000000000000153013", m.MeteringPoint)
				assert.Equal(t, civil.DateFor(2023, 01, 01), m.State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2025, 4, 24), m.State.InactiveSince.Date)
				assert.Equal(t, model.INIT, m.ProcessState)
				assert.Equal(t, model.S_INACTIVE, m.Status)
			},
		},
		{
			name:          "Activate - Antwort",
			msg:           `{"conversationId":"RC102728202506051749149940000821663","messageId":"AT003000202506052059070020432540055","sender":"AT003000","receiver":"RC102728","messageCode":"ANTWORT_ECON","messageCodeVersion":"01.12","requestId":"NEpTmuH","ecId":"AT00300000000TC000015000000000001","responseData":[{"meteringPoint":"AT0030000000000000000000000350193","responseCode":[99]}]}`,
			msgType:       model.EBMS_ONLINE_REG_ANSWER,
			msgProto:      model.EC_REQ_ONL,
			meter:         "AT0030000000000000000000000153013",
			expectedState: model.S_INACTIVE,
			invoke: func(msg model.SubscribeMessage) {
				protocolEcReqOnlHandler(context.Background(), msg)
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				assert.Equal(t, "AT0030000000000000000000000153013", m.MeteringPoint)
				assert.Equal(t, civil.DateFor(2023, 01, 01), m.State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2025, 4, 24), m.State.InactiveSince.Date)
				assert.Equal(t, model.PENDING, m.ProcessState)
				assert.Equal(t, model.S_INACTIVE, m.Status)
			},
		},
		{
			name:          "Activate - Zustimmung",
			msg:           `{"conversationId":"RC102925202505291748545670000769433","messageId":"AT003200202506052018051920000038938","sender":"AT003200","receiver":"RC102925","messageCode":"ZUSTIMMUNG_ECON","messageCodeVersion":"01.12","requestId":"ScYyHPx","ecId":"AT00300000000TC000015000000000001","responseData":[{"meteringPoint":"AT0032000000000000000000000011490","responseCode":[175],"consentId":"AT003200202506052017576180000052415"}]}`,
			msgType:       model.EBMS_ONLINE_REG_APPROVAL,
			msgProto:      model.EC_REQ_ONL,
			meter:         "AT0030000000000000000000000153013",
			expectedState: model.S_INACTIVE,
			invoke: func(msg model.SubscribeMessage) {
				protocolEcReqOnlHandler(context.Background(), msg)
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				assert.Equal(t, "AT0030000000000000000000000153013", m.MeteringPoint)
				assert.Equal(t, civil.DateFor(2023, 01, 01), m.State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2025, 4, 24), m.State.InactiveSince.Date)
				assert.Equal(t, model.APPROVED, m.ProcessState)
				assert.Equal(t, model.S_INACTIVE, m.Status)
			},
		},
		{
			name:          "Activate - Abschluss",
			msg:           `{"conversationId":"RC102925202505291748545670000769433","messageId":"AT003200202506052023341700000031909","sender":"AT003200","receiver":"RC102925","messageCode":"ABSCHLUSS_ECON","messageCodeVersion":"02.00","ecId":"AT00300000000TC000015000000000001","meterList":[{"meteringPoint":"AT0032000000000000000000000011490","direction":"CONSUMPTION","activation":1749160800000,"partFact":100,"from":1749160800000,"to":253402210800000,"consentId":"AT002000202504071118158864B2hYwr"}]}`,
			msgType:       model.EBMS_ONLINE_REG_COMPLETION,
			msgProto:      model.EC_REQ_ONL,
			meter:         "AT0030000000000000000000000153013",
			expectedState: model.S_ACTIVE,
			invoke: func(msg model.SubscribeMessage) {
				protocolEcReqOnlHandler(context.Background(), msg)
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				assert.Equal(t, "AT0030000000000000000000000153013", m.MeteringPoint)
				assert.Equal(t, civil.DateFor(2023, 01, 01), m.State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2999, 12, 31), m.State.InactiveSince.Date)
				assert.Equal(t, model.ACTIVE, m.ProcessState)
				assert.Equal(t, model.S_ACTIVE, m.Status)
			},
		},
		{
			name:          "Deactivate - CCMI",
			msg:           `{"conversationId":"AT002000202506170631212181027282501","messageId":"AT002000202506170631212187101145223","sender":"AT002000","receiver":"RC100713","messageCode":"AUFHEBUNG_CCMI","messageCodeVersion":"01.00","responseData":[{"meteringPoint":"AT0020000000000000000000021200356","responseCode":[1099],"consentEnd":1750024800000,"consentId":"AT002000202504071118158864B2hYwr"}]}`,
			msgType:       model.EBMS_AUFHEBUNG_CCMI,
			msgProto:      model.CM_REV_IMP,
			meter:         "AT0030000000000000000000000153013",
			expectedState: model.S_INACTIVE,
			invoke: func(msg model.SubscribeMessage) {
				protocolCmRevImpHandler(context.Background(), msg)
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				assert.Equal(t, "AT0030000000000000000000000153013", m.MeteringPoint)
				assert.Equal(t, civil.DateFor(2023, 01, 01), m.State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2025, 6, 16), m.State.InactiveSince.Date)
				assert.Equal(t, model.INACTIVE, m.ProcessState)
				assert.Equal(t, model.S_INACTIVE, m.Status)
				fmt.Printf("Date: %v\n", m.State.InactiveSince.Date)
			},
		},
		{
			name:          "Activate - Abschluss",
			msg:           `{"conversationId":"RC102925202505291748545670000769433","messageId":"AT003200202506052023341700000031909","sender":"AT003200","receiver":"RC102925","messageCode":"ABSCHLUSS_ECON","messageCodeVersion":"02.00","ecId":"AT00300000000TC000015000000000001","meterList":[{"meteringPoint":"AT0032000000000000000000000011490","direction":"CONSUMPTION","activation":1749160800000,"partFact":100,"from":1749160800000,"to":253402210800000,"consentId":"AT002000202504071118158864B2hYwr"}]}`,
			msgType:       model.EBMS_ONLINE_REG_COMPLETION,
			msgProto:      model.EC_REQ_ONL,
			meter:         "AT0030000000000000000000000153013",
			expectedState: model.S_ACTIVE,
			invoke: func(msg model.SubscribeMessage) {
				protocolEcReqOnlHandler(context.Background(), msg)
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				assert.Equal(t, "AT0030000000000000000000000153013", m.MeteringPoint)
				assert.Equal(t, civil.DateFor(2023, 01, 01), m.State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2999, 12, 31), m.State.InactiveSince.Date)
				assert.Equal(t, model.ACTIVE, m.ProcessState)
				assert.Equal(t, model.S_ACTIVE, m.Status)
			},
		},
		{
			name:          "Deactivate - CCMC",
			msg:           `{"conversationId":"AT007000202506161124320811006758410","messageId":"AT007000202506161124321786085283099","sender":"AT007000","receiver":"RC101254","messageCode":"AUFHEBUNG_CCMC","messageCodeVersion":"01.00","responseData":[{"meteringPoint":"AT007000094620000010190002684274A","responseCode":[1099],"consentEnd":1750111200000,"consentId":"AT002000202504071118158864B2hYwr"}]}`,
			msgType:       model.EBMS_AUFHEBUNG_CCMC,
			msgProto:      model.CM_REV_CUS,
			meter:         "AT0030000000000000000000000153013",
			expectedState: model.S_INACTIVE,
			invoke: func(msg model.SubscribeMessage) {
				protocolCmRevImpHandler(context.Background(), msg)
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				assert.Equal(t, "AT0030000000000000000000000153013", m.MeteringPoint)
				assert.Equal(t, civil.DateFor(2023, 01, 01), m.State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2025, 6, 17), m.State.InactiveSince.Date)
				assert.Equal(t, model.INACTIVE, m.ProcessState)
				assert.Equal(t, model.S_INACTIVE, m.Status)
				fmt.Printf("Date: %v\n", m.State.InactiveSince.Date)
			},
		},
		{
			name:          "Activate - Abschluss",
			msg:           `{"conversationId":"RC102925202505291748545670000769433","messageId":"AT003200202506052023341700000031909","sender":"AT003200","receiver":"RC102925","messageCode":"ABSCHLUSS_ECON","messageCodeVersion":"02.00","ecId":"AT00300000000TC000015000000000001","meterList":[{"meteringPoint":"AT0032000000000000000000000011490","direction":"CONSUMPTION","activation":1749160800000,"partFact":100,"from":1749160800000,"to":253402210800000,"consentId":"AT002000202504071118158864B2hYwr"}]}`,
			msgType:       model.EBMS_ONLINE_REG_COMPLETION,
			msgProto:      model.EC_REQ_ONL,
			meter:         "AT0030000000000000000000000153013",
			expectedState: model.S_ACTIVE,
			invoke: func(msg model.SubscribeMessage) {
				protocolEcReqOnlHandler(context.Background(), msg)
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				assert.Equal(t, "AT0030000000000000000000000153013", m.MeteringPoint)
				assert.Equal(t, civil.DateFor(2023, 01, 01), m.State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2999, 12, 31), m.State.InactiveSince.Date)
				assert.Equal(t, model.ACTIVE, m.ProcessState)
				assert.Equal(t, model.S_ACTIVE, m.Status)
			},
		},
		{
			name:          "Revoke - CCMS sent again",
			msg:           `{"conversationId":"RC103619202506161750092130000917203","messageId":"RC103619202506161750092130000917202","sender":"RC103619","receiver":"AT008000","messageCode":"AUFHEBUNG_CCMS","messageCodeVersion":"","requestId":"NZCUR5C3","meter":{"meteringPoint":"AT0080000885400000202006120074147","consentId":"AT002000202504071118158864B2hYwr"},"ecId":"AT00300000000TC000015000000000001","consentEnd":1751234400000}`,
			msgType:       model.EBMS_AUFHEBUNG_CCMS,
			msgProto:      model.CM_REV_SP,
			meter:         "AT0030000000000000000000000153013",
			expectedState: model.S_ACTIVE,
			invoke: func(msg model.SubscribeMessage) {
				protocolCmRevImpHandler(context.Background(), msg)
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				assert.Equal(t, civil.DateFor(2999, 12, 31), m.State.InactiveSince.Date)
				assert.Equal(t, model.ACTIVE, m.ProcessState)
				assert.Equal(t, model.S_ACTIVE, m.Status)
			},
		},
		{
			name:          "Antwort - CCMS",
			msg:           `{"conversationId":"RC103619202506161750092130000917203","messageId":"AT008000202506161842410660000066105","sender":"AT008000","receiver":"RC103619","messageCode":"ANTWORT_CCMS","messageCodeVersion":"01.12","requestId":"NZCUR5C3","ecId":"AT00300000000TC000015000000000001","responseData":[{"meteringPoint":"AT0080000885400000202006120074147","responseCode":[176],"consentId":"AT002000202504071118158864B2hYwr"}],"consentEnd":1751234400000}`,
			msgType:       model.EBMS_ANTWORT_CCMS,
			msgProto:      model.CM_REV_SP,
			meter:         "AT0030000000000000000000000153013",
			expectedState: model.S_INACTIVE,
			invoke: func(msg model.SubscribeMessage) {
				protocolCmRevImpHandler(context.Background(), msg)
			},
			check: func(t *testing.T, m *model.MeteringPoint) {
				assert.Equal(t, "AT0030000000000000000000000153013", m.MeteringPoint)
				assert.Equal(t, civil.DateFor(2023, 01, 01), m.State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2025, 6, 30), m.State.InactiveSince.Date)
				assert.Equal(t, model.INACTIVE, m.ProcessState)
				assert.Equal(t, model.S_INACTIVE, m.Status)
				fmt.Printf("Date: %v\n", m.State.InactiveSince.Date)
			},
		},
	}

	db, err := database.GetDB(context.Background())
	require.NoError(t, err)
	// the flow starts from the seeded active metering point, also on a repeated run (-count=2)
	_, err = testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET status = 'ACTIVE', process_state = 'ACTIVE',
		inactivesince = '2999-12-31' WHERE tenant = 'TE000015' AND metering_point_id = 'AT0030000000000000000000000153013'`)
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := model.SubscribeMessage{
				MessageCode: tt.msgType,
				Protocol:    tt.msgProto,
				Tenant:      "TE000015",
				Payload:     model.EbmsMessage{},
			}
			err := json.Unmarshal([]byte(tt.msg), &msg.Payload)
			require.NoError(t, err)

			switch tt.msgType {
			case model.EBMS_ONLINE_REG_INIT:
				msg.Payload.Meter.MeteringPoint = tt.meter
				msg.Payload.Receiver = "TE000015"
				break
			case model.EBMS_AUFHEBUNG_CCMS:
				msg.Payload.Meter.MeteringPoint = tt.meter
				msg.Payload.Sender = "TE000015"
			case model.EBMS_ONLINE_REG_COMPLETION:
				msg.Payload.MeterList[0].MeteringPoint = tt.meter
				msg.Payload.Receiver = "TE000015"
			default:
				msg.Payload.ResponseData[0].MeteringPoint = tt.meter
				msg.Payload.Receiver = "TE000015"
			}

			tt.invoke(msg)

			meter, err := db.FindMeteringByStatus(context.Background(), msg.Tenant, tt.meter, tt.expectedState)
			require.NoError(t, err)
			tt.check(t, meter)
		})
	}

}

// ZUSTIMMUNG_ECON with 172 (customer refused the data release) or 173 (customer did not respond)
// sets the metering point to INVALID with the code and notifies the community (platform#180).
func TestProtocolEcReqOnlHandler_approvalRefused(t *testing.T) {
	const tenant = "TE000172"
	const ecId = "AT00300000000TC000172000000000001"
	cleanupCommunities(t, tenant)
	_, err := testDB.DbInstance.Exec(`INSERT INTO base.eeg
		(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
		 street, "streetNumber", city, zip, email)
		VALUES ($1, 'EEG-172', 'Test 172', 'TE000172', 'LOCAL', 'AT003000', 'Netz OÖ', $2,
		 'Weg', '1', 'Linz', '4020', 'test-172@example.org')`, tenant, ecId)
	require.NoError(t, err)

	tests := []struct {
		name  string
		meter string
		code  int16
	}{
		{name: "172 refused", meter: "AT0030000000000000000000000172001", code: CUSTOMER_HAS_REFUSED_DATA_RELEASE},
		{name: "173 no response", meter: "AT0030000000000000000000000173001", code: CUSTOMER_DID_NOT_RESPOND_TO_DATA_RELEASE},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := testDB.DbInstance.Exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction,
				status, process_state, "modifiedAt", "modifiedBy", "registeredSince", flag)
				VALUES ($1, 'ea1142dc-03da-15ee-b82b-5a985b4b033a', $2, 'CONSUMPTION', 'INIT', 'PENDING', now(), 'test',
				 '2026-10-01', 1)`, tt.meter, tenant)
			require.NoError(t, err)

			msg := model.SubscribeMessage{MessageCode: model.EBMS_ONLINE_REG_APPROVAL, Protocol: model.EC_REQ_ONL, Tenant: strings.ToLower(tenant)}
			require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"conversationId":"RC000172202610210000000000000%d","messageId":"AT003000202610210000000000000%d","sender":"AT003000","receiver":"TE000172","messageCode":"ZUSTIMMUNG_ECON","messageCodeVersion":"01.12","requestId":"Z172","ecId":"%s","responseData":[{"meteringPoint":"%s","responseCode":[%d]}]}`,
				tt.code, tt.code, ecId, tt.meter, tt.code)), &msg.Payload))

			protocolEcReqOnlHandler(context.Background(), msg)

			var row struct {
				ProcessState string `db:"process_state"`
				StatusCode   *int16 `db:"statusCode"`
			}
			require.NoError(t, testDB.DbInstance.Get(&row, `SELECT process_state, "statusCode" FROM base.meteringpoint WHERE tenant = $1 AND metering_point_id = $2`, tenant, tt.meter))
			assert.Equal(t, string(model.INVALID), row.ProcessState)
			require.NotNil(t, row.StatusCode)
			assert.Equal(t, tt.code, *row.StatusCode)

			var n int
			require.NoError(t, testDB.DbInstance.Get(&n, `SELECT count(*) FROM base.notification WHERE tenant = $1
				AND notification->>'type' = 'ZUSTIMMUNG_ECON' AND (notification->'meteringPoints')::jsonb ? $2
				AND (notification->'responseCodes')::jsonb ? $3`, tenant, tt.meter, convertCodes2Strings([]int16{tt.code})[0]))
			assert.Equal(t, 1, n)
		})
	}
}
