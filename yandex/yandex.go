package yandex

import (
	"fmt"
	"net/http"

	"yandex-stream/request"
)

// api document https://yandex.ru/dev/disk/poligon

// Drive instance
type YandexDrive struct {
	base         string
	access_token string
}

// New instance
func New(auth string) *YandexDrive {
	d := &YandexDrive{
		base:         "https://cloud-api.yandex.net/v1/disk",
		access_token: auth,
	}
	return d
}

func (d *YandexDrive) Auth(access_token string) {
	d.access_token = access_token
}

func (d *YandexDrive) List() ([]byte, error) {
	return d.requestJSON("/resources/files?limit=200")
}

func (d *YandexDrive) requestJSON(path string) ([]byte, error) {
	var (
		method  = http.MethodGet
		headers = http.Header{
			"Accept":        {"application/json"},
			"Authorization": {fmt.Sprintf("OAuth %s", d.access_token)},
		}
	)
	return request.RequestJSON(d.base+path, method, nil, headers)
}
