package yandex

import (
	"fmt"
	"net/http"
	"time"

	"yandex-stream/request"
)

// api document https://yandex.ru/dev/disk/poligon

// Drive instance
type YandexDrive struct {
	base         string
	access_token string
	time         int64
	cache        []byte
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
	var current = time.Now().Unix()
	if current-d.time < 60 && len(d.cache) > 0 {
		return d.cache, nil
	}
	data, err := d.requestJSON("/resources/files?limit=200")
	if err == nil {
		d.time = current
		d.cache = data
	}
	return data, err
}

func (d *YandexDrive) requestJSON(path string) ([]byte, error) {
	var (
		headers = http.Header{
			"Accept":        {"application/json"},
			"Authorization": {fmt.Sprintf("OAuth %s", d.access_token)},
		}
	)
	return request.RequestJSON(d.base+path, headers)
}
