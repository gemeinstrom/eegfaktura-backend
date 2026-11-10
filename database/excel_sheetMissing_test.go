package database

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

// A6: a sheet name that is not in the workbook answered "sheet … does not exist" under "Bad Process". Now code 1064
// with a German text naming the sheet; nothing is read.
func TestImportMasterdataFromExcel_sheetMissing(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)
	f := excelize.NewFile()
	buf, err := f.WriteToBuffer()
	require.NoError(t, err)

	err = db.ImportMasterdataFromExcel(context.Background(), buf, "test.xlsx", "EEG Stammdaten", "TE000009")

	var ve *model.VfeegError
	require.True(t, errors.As(err, &ve), "%v", err)
	assert.Equal(t, 1064, ve.Code)
	assert.Equal(t, fmt.Sprintf(model.ImportSheetMissingText, "EEG Stammdaten"), model.UserText(ve))
}
