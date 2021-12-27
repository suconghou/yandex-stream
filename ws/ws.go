package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"
	"yandex-stream/request"
	"yandex-stream/util"

	"github.com/tidwall/gjson"
	"nhooyr.io/websocket"
)

type ActionType int

// ActionType constants.
const (
	ActionBreak ActionType = iota + 1
	ActionDo
)

type wsCenter struct {
}

func New() *wsCenter {
	return &wsCenter{}
}

func (w *wsCenter) Subscribe(ctx context.Context, c *websocket.Conn, url string) error {
	var (
		signal = make(chan ActionType, 1)
		lock   = sync.Mutex{}
		tasks  = [...]int64{0, 0}
	)
	fn_read := func() error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				msgType, data, err := c.Read(ctx)
				if err != nil {
					return err
				}
				var (
					j      = gjson.ParseBytes(data)
					action = j.Get("type").String()
				)
				switch action {
				case "req":
					signal <- ActionBreak
					var (
						start = j.Get("start").Int()
						end   = j.Get("end").Int()
					)
					lock.Lock()
					tasks[0] = start
					tasks[1] = end
					lock.Unlock()
					signal <- ActionDo
				default:
					util.Log.Print(msgType, action, data)
				}
			}
		}
	}
	go func() {
		if err := fn_read(); IsError(err) {
			util.Log.Print(err)
		}
	}()
	for {
		select {
		case msg := <-signal:
			if msg == ActionBreak {
				continue
			}
			var (
				start = tasks[0]
				end   = tasks[1]
			)
			if r, status, total, err := getResponse(url, start, end); err == nil {
				var buffer = make([]byte, 1048576)
				var stream_copy = func() error {
					defer r.Close()
					var offset = start
					for {
						select {
						case f := <-signal:
							fmt.Println(f)
						default:
							n, err := r.Read(buffer)

							// TODO binary send
							var data = buildMsg(total, offset, offset+int64(n), buffer[:n])
							offset += int64(n)
							writeTimeout(ctx, websocket.MessageBinary, c, data)
							if err != nil {
								return err
							}
						}
					}
				}
				if err = stream_copy(); err != nil {
					util.Log.Print(err)
				}
			} else {
				var v = map[string]interface{}{
					"status": status,
					"msg":    err.Error(),
				}
				if bs, err := json.Marshal(v); err == nil {
					err = writeTimeout(ctx, websocket.MessageText, c, bs)
					if err != nil {
						return err
					}
				} else {
					util.Log.Print(err)
				}
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func IsError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var er = websocket.CloseStatus(err)
	if er == websocket.StatusNormalClosure || er == websocket.StatusGoingAway {
		return false
	}
	return true
}

func writeTimeout(ctx context.Context, msgType websocket.MessageType, c *websocket.Conn, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	return c.Write(ctx, msgType, msg)
}

// TODO context
func getResponse(url string, start int64, end int64) (io.ReadCloser, int, int64, error) {
	var headers = http.Header{
		"range": []string{fmt.Sprintf("bytes=%d-", start)},
	}
	if end > 0 && end >= start {
		headers.Set("range", fmt.Sprintf("bytes=%d-%d", start, end))
	}
	var (
		r      io.ReadCloser
		total  int64
		err    error
		status int
		times  = 0
	)
	for {
		r, status, total, err = getRetry(url, headers)
		if err == nil {
			break
		}
		times++
		if times > 5 {
			break
		}
	}
	return r, status, total, err
}

// the url resource must accept range
func getRetry(url string, headers http.Header) (io.ReadCloser, int, int64, error) {
	resp, err := request.Request(url, http.MethodGet, nil, headers)
	if err != nil {
		return nil, 0, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, 0, fmt.Errorf("%s : %s", url, resp.Status)
	}
	var (
		filesize    = resp.ContentLength
		cr          = resp.Header.Get("Content-Range")
		rangeResReg = regexp.MustCompile(`\d+/(\d+)`)
	)
	if rangeResReg.MatchString(cr) {
		matches := rangeResReg.FindStringSubmatch(cr)
		filesize, _ = strconv.ParseInt(matches[1], 10, 64)
	}
	if filesize < 1 {
		return resp.Body, resp.StatusCode, filesize, fmt.Errorf("invalid file size %d", filesize)
	}
	return resp.Body, resp.StatusCode, filesize, nil
}

// TODO
func buildMsg(total int64, start int64, end int64, data []byte) []byte {
	return nil
}
