package httputil

import (
	"bytes"
	"net/http"
	"net/url"
)

func PostFormUrlencoded(client *http.Client, url string, modifyRequest func(r *http.Request), values url.Values) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(values.Encode())))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if modifyRequest != nil {
		modifyRequest(req)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if err := Ensure2XX(resp); err != nil {
		return nil, err
	}
	return resp, nil
}
