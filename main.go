package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"yandex-stream/request"
	"yandex-stream/util"
	"yandex-stream/ws"
	"yandex-stream/yandex"

	"github.com/tidwall/gjson"
	"nhooyr.io/websocket"
)

type resp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

var (
	disk = yandex.New(os.Getenv("YANDEX_ACCESS_TOKEN"))
	wser = ws.New()
)

type routeInfo struct {
	Reg     *regexp.Regexp
	Handler func(http.ResponseWriter, *http.Request, []string) error
}

var route = []routeInfo{
	{regexp.MustCompile(`^/ws/status$`), wsStatus},
	{regexp.MustCompile(`^/ws/(.*)$`), wsHander},
	{regexp.MustCompile(`^/yandex/(.*)$`), yandexServe},
	{regexp.MustCompile(`^/list/yandex/(.*)$`), yandexServe},
	{regexp.MustCompile(`^/part/(.+)$`), filePart},
}

func hooks(r *http.Request) {
	if v := r.URL.Query().Get("access_token"); v != "" {
		disk.Auth(v)
	}
	if v := r.Header.Get("access_token"); v != "" {
		disk.Auth(v)
	}
}
func wsStatus(w http.ResponseWriter, r *http.Request, match []string) error {
	var data = wser.Status()
	_, err := util.JSONPut(w, data, http.StatusOK, 5)
	return err
}

func wsHander(w http.ResponseWriter, r *http.Request, match []string) error {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return err
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	var url = "http://share.suconghou.cn/stream/mp4/9drLLoyt_bc.mp4"
	if err = wser.Subscribe(r.Context(), c, url); ws.IsError(err) {
		return err
	}
	return nil
}

// yandex drive list
func yandexServe(w http.ResponseWriter, r *http.Request, match []string) error {
	var path = match[1]
	data, err := disk.List()
	if err != nil {
		util.JSONPut(w, resp{-1, err.Error()}, http.StatusInternalServerError, 10)
		return err
	}
	if path == "" {
		_, err = util.JSONPut(w, data, http.StatusOK, 600)
		return err
	}
	var url string
	gjson.ParseBytes(data).Get("items").ForEach(func(key, value gjson.Result) bool {
		if strings.Contains(value.Get("path").String(), path) {
			url = value.Get("file").String()
			if url != "" {
				return false
			}
		}
		return true
	})
	if url == "" {
		http.NotFound(w, r)
		return nil
	}
	return request.Proxy(w, r, url)
}

func filePart(w http.ResponseWriter, r *http.Request, match []string) error {
	var (
		realpath = filepath.Join("./public", match[1])
		ran      = r.URL.Query().Get("range")
	)
	if r.Header.Get("range") == "" && ran != "" {
		r.Header.Set("range", fmt.Sprintf("bytes=%s", ran))
	}
	http.ServeFile(w, r, realpath)
	return nil
}

func main() {
	var (
		port = flag.Int("p", 6060, "listen port")
		host = flag.String("h", "", "bind address")
	)
	flag.Parse()
	util.Log.Fatal(serve(*host, *port))
}

func serve(host string, port int) error {
	http.HandleFunc("/", routeMatch)
	util.Log.Printf("Starting up on port %d", port)
	return http.ListenAndServe(fmt.Sprintf("%s:%d", host, port), nil)
}

func routeMatch(w http.ResponseWriter, r *http.Request) {
	hooks(r)
	for _, p := range route {
		if p.Reg.MatchString(r.URL.Path) {
			if err := p.Handler(w, r, p.Reg.FindStringSubmatch(r.URL.Path)); err != nil {
				util.Log.Print(err)
			}
			return
		}
	}
	fallback(w, r)
}

func fallback(w http.ResponseWriter, r *http.Request) {
	const index = "index.html"
	files := []string{index}
	if r.URL.Path != "/" {
		files = []string{r.URL.Path, path.Join(r.URL.Path, index)}
	}
	if !tryFiles(files, w, r) {
		http.NotFound(w, r)
	}
}

func tryFiles(files []string, w http.ResponseWriter, r *http.Request) bool {
	for _, file := range files {
		realpath := filepath.Join("./public", file)
		if f, err := os.Stat(realpath); err == nil {
			if f.Mode().IsRegular() {
				http.ServeFile(w, r, realpath)
				return true
			}
		}
	}
	return false
}
