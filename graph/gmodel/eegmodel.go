package gmodel

import (
	"at.ourproject/vfeeg-backend/model"
	"encoding/json"
	"fmt"
	"github.com/99designs/gqlgen/graphql"
	log "github.com/sirupsen/logrus"
	"io"
)

func UnmarshalEeg(v interface{}) (model.Eeg, error) {
	byteData, err := json.Marshal(v)
	if err != nil {
		return model.Eeg{}, fmt.Errorf("FAIL WHILE MARSHAL SCHEME")
	}
	tmp := model.Eeg{}
	err = json.Unmarshal(byteData, &tmp)
	if err != nil {
		return model.Eeg{}, fmt.Errorf("FAIL WHILE UNMARSHAL SCHEME")
	}
	return tmp, nil
}

func MarshalEeg(e model.Eeg) graphql.Marshaler {
	return graphql.WriterFunc(func(w io.Writer) {
		byteData, err := json.Marshal(e)
		if err != nil {
			log.Printf("FAIL WHILE MARSHAL JSON %v\n", err)
		}
		_, err = w.Write(byteData)
		if err != nil {
			log.Printf("FAIL WHILE WRITE DATA %v\n", err)
		}
	})
}
