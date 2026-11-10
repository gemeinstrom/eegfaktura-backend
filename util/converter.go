package util

import (
	"reflect"
)

func ConvertMapToStruct(m map[string]interface{}, s interface{}) error {
	stValue := reflect.ValueOf(s).Elem()
	sType := stValue.Type()
	for i := 0; i < sType.NumField(); i++ {
		field := stValue.Field(i)
		switch field.Kind() {
		case reflect.Struct:
			fallthrough
		case reflect.Ptr:
			ConvertMapToStruct(m[sType.Field(i).Name].(map[string]interface{}), stValue.Interface())
		}
		if value, ok := m[sType.Field(i).Name]; ok {
			stValue.Field(i).Set(reflect.ValueOf(value))
		}
	}
	return nil
}

func ConvertStructToMap(s interface{}) map[string]interface{} {
	m := map[string]interface{}{}
	stValue := reflect.ValueOf(s)
	sType := stValue.Type()

	var data interface{}
	for i := 0; i < sType.NumField(); i++ {
		field := stValue.Field(i)
		switch field.Kind() {
		case reflect.Struct:
			fallthrough
		case reflect.Ptr:
			data = ConvertStructToMap(field.Interface())
		default:
			data = field.Interface()
		}
		m[sType.Field(i).Name] = data
	}
	return m
}
