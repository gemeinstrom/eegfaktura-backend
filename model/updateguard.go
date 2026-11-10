package model

import (
	"reflect"
	"strings"
)

// AllowedUpdateColumn resolves a client-supplied JSON field name to the database
// column of the matching struct field — but only when that field exists and is
// not write-protected (goqu tag "skipupdate", or db tag "-"). For anything else
// it returns ok=false.
//
// Partial-update endpoints take the field name from the request body and hand it
// to the SQL builder as an identifier. goqu quotes identifiers but does not
// escape them, so a caller-controlled name must be mapped to a known column
// first; an unknown name is rejected rather than passed through verbatim. It
// also blocks writing fields the model marks as not updatable (e.g. tenant,
// rcNumber), which must never be set through a generic partial update.
func AllowedUpdateColumn(model interface{}, jsonField string) (string, bool) {
	t := reflect.TypeOf(model)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return "", false
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		jsonTag := strings.TrimSpace(strings.Split(f.Tag.Get("json"), ",")[0])
		if jsonTag == "" || jsonTag != jsonField {
			continue
		}
		if hasTagOption(f.Tag.Get("goqu"), "skipupdate") {
			return "", false
		}
		dbTag := strings.TrimSpace(strings.Split(f.Tag.Get("db"), ",")[0])
		if dbTag == "-" {
			return "", false
		}
		if dbTag != "" {
			return dbTag, true
		}
		return jsonTag, true
	}
	return "", false
}

// ResolveFlatUpdateColumn resolves a client-supplied key of a flat partial
// update to the database column of the matching struct field. Unlike
// AllowedUpdateColumn it also descends into embedded (anonymous) structs, whose
// fields are stored as columns of the same table, and it accepts the column
// name as well as the JSON name — clients send both (e.g. "creditor_id").
// Fields marked "skipupdate" or db:"-" and unknown keys yield ok=false.
func ResolveFlatUpdateColumn(model interface{}, key string) (string, bool) {
	t := reflect.TypeOf(model)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return "", false
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			if hasTagOption(f.Tag.Get("goqu"), "skipupdate") {
				continue
			}
			if col, ok := ResolveFlatUpdateColumn(reflect.New(f.Type).Elem().Interface(), key); ok {
				return col, true
			}
			continue
		}
		jsonTag := strings.TrimSpace(strings.Split(f.Tag.Get("json"), ",")[0])
		dbTag := strings.TrimSpace(strings.Split(f.Tag.Get("db"), ",")[0])
		if jsonTag == "-" || dbTag == "-" {
			continue
		}
		col := dbTag
		if col == "" {
			col = jsonTag
		}
		if col == "" || (key != jsonTag && key != col) {
			continue
		}
		if hasTagOption(f.Tag.Get("goqu"), "skipupdate") {
			return "", false
		}
		return col, true
	}
	return "", false
}

// participantPartialSubModels maps the first segment of a dotted participant
// partial-update path to the struct that owns the second segment. The segments
// mirror updateParticipantPartial in the DAO.
func participantPartialSubModels(group string) (interface{}, bool) {
	switch group {
	case "billingAddress", "residentAddress":
		return Address{}, true
	case "contact":
		return ContactInfo{}, true
	case "accountInfo":
		return BankInfo{}, true
	default:
		return nil, false
	}
}

// IsAllowedParticipantUpdatePath reports whether a participant partial-update
// path ("field" or "group.field") addresses a real, updatable column. It mirrors
// the branches of updateParticipantPartial so an unknown or injected path never
// reaches the SQL builder.
func IsAllowedParticipantUpdatePath(path string) bool {
	parts := strings.Split(path, ".")
	switch len(parts) {
	case 1:
		_, ok := AllowedUpdateColumn(EegParticipantBase{}, parts[0])
		return ok
	case 2:
		sub, ok := participantPartialSubModels(parts[0])
		if !ok {
			return false
		}
		_, ok = AllowedUpdateColumn(sub, parts[1])
		return ok
	default:
		return false
	}
}

// SkipUpdateJSONKeys returns the JSON field names of the struct whose goqu tag
// marks them as not updatable ("skipupdate"). Used to strip write-protected
// fields from a client-supplied update map before it reaches the SQL builder.
func SkipUpdateJSONKeys(model interface{}) []string {
	t := reflect.TypeOf(model)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		jsonTag := strings.TrimSpace(strings.Split(f.Tag.Get("json"), ",")[0])
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		if hasTagOption(f.Tag.Get("goqu"), "skipupdate") {
			keys = append(keys, jsonTag)
		}
	}
	return keys
}

func hasTagOption(tag, opt string) bool {
	for _, p := range strings.Split(tag, ",") {
		if strings.TrimSpace(p) == opt {
			return true
		}
	}
	return false
}
