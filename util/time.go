package util

import (
	"errors"
	"fmt"
	"github.com/jjeffery/civil"
	"regexp"
	"strconv"
	"time"
)

func TruncateToStartOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func TruncateToEndOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 23, 45, 00, 0, t.Location())
}

func MaxTimeStamp(x, y int64) int64 {
	if x < y {
		return y
	}
	return x
}

func MinTimeStamp(x, y int64) int64 {
	if x < y {
		return x
	}
	return y
}

var dateTimeLine = regexp.MustCompile(`^[0-9]{2}.[0-9]{2}.[0-9]{4}\s[0-9]{2}:[0-9]{2}:[0-9]{2}$`)
var dateLine = regexp.MustCompile(`^[0-9]{2}.[0-9]{2}.[0-9]{4}$`)

func isDateString(cell string) bool {
	if dateTimeLine.MatchString(cell) {
		return true
	}
	return false
}

func ParseTimeString(cell string) (civil.Date, error) {
	var d, m, y int
	if _, err := fmt.Sscanf(cell, "%d.%d.%d", &d, &m, &y); err == nil {
		return civil.DateFor(y, time.Month(m), d), nil
	}
	return civil.Date{}, errors.New("invalid time")
}

func getExcelDate(cell string) (int, int, int, int, int, int) {
	excelTime := parseExcelDate(cell).Round(15 * time.Minute)
	return excelTime.Day(), int(excelTime.Month()), excelTime.Year(), excelTime.Hour(), excelTime.Minute(), excelTime.Second()
}

func parseExcelDate(cell string) time.Time {
	if isDateString(cell) {
		return StringToTime(cell)
	} else {
		var excelEpoch = time.Date(1899, time.December, 30, 0, 0, 0, 0, time.UTC)
		var days, _ = strconv.ParseFloat(cell, 64)
		return excelEpoch.Add(time.Second * time.Duration(days*86400))
	}
}

func StringToTime(date string) time.Time {
	var d, m, y, hh, mm, ss int
	if _, err := fmt.Sscanf(date, "%d.%d.%d %d:%d:%d", &d, &m, &y, &hh, &mm, &ss); err == nil {
		return time.Date(y, time.Month(m), d, hh, mm, ss, 0, time.Local)
	}
	return time.Now()
}

func CalcProcessDate(date civil.Date) int64 {
	diff := date.Sub(civil.Today())
	days := int(diff.Hours() / 24)

	if days < 1 {
		return civil.Today().AddDate(0, 0, 1).Unix() * 1000
	}
	return date.Unix() * 1000
}

func CalcProcessNullDate(date civil.NullDate) int64 {
	if date.Valid {
		return CalcProcessDate(date.Date)
	}
	return civil.Today().AddDate(0, 0, 1).Unix() * 1000
}
