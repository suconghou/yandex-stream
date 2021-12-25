package request

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

var (
	client = &http.Client{
		Timeout: time.Minute * 2,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			Proxy:           http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}

	fwdHeaders = []string{
		"User-Agent",
		"Accept",
		"Accept-Encoding",
		"Accept-Language",
		"If-Modified-Since",
		"If-None-Match",
		"Range",
		"Content-Length",
		"Content-Type",
	}
	exposeHeaders = []string{
		"Accept-Ranges",
		"Content-Range",
		"Content-Length",
		"Content-Type",
		"Content-Encoding",
		"Date",
		"Expires",
		"Last-Modified",
		"Etag",
		"Cache-Control",
	}
)

// RequestJSON return json response
func RequestJSON(target string, method string, body io.ReadCloser, headers http.Header) ([]byte, error) {
	res, err := Request(target, method, body, headers)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	bs, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return bs, fmt.Errorf("%s : %s", target, res.Status)
	}
	return bs, nil
}

// Request return json response
func Request(target string, method string, body io.ReadCloser, headers http.Header) (*http.Response, error) {
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return nil, err
	}
	req.Header = headers
	if l := headers.Get("Content-Length"); l != "" {
		length, err := strconv.Atoi(l)
		if err != nil {
			return nil, err
		}
		req.ContentLength = int64(length)
	}
	return client.Do(req)
}

// Proxy url to download
func Proxy(w http.ResponseWriter, r *http.Request, url string) error {
	var (
		client    = &http.Client{Timeout: time.Hour}
		reqHeader = http.Header{}
	)
	req, err := http.NewRequest(r.Method, url, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}
	req.Header = copyHeader(r.Header, reqHeader, fwdHeaders)
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}
	defer resp.Body.Close()
	to := w.Header()
	copyHeader(resp.Header, to, exposeHeaders)
	to.Set("Cache-Control", "public, max-age=604800")
	w.WriteHeader(resp.StatusCode)
	_, err = io.Copy(w, resp.Body)
	return err
}

func copyHeader(from http.Header, to http.Header, headers []string) http.Header {
	for _, k := range headers {
		if v := from.Get(k); v != "" {
			to.Set(k, v)
		}
	}
	return to
}
