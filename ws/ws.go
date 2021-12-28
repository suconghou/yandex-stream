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
	ActionErr
)

type wsCenter struct {
	lock  *sync.Mutex
	conns map[uint64]*wsTask
}

type wsTask struct {
	ctx   context.Context
	c     *websocket.Conn
	url   string
	total int64 // cached total file size, will be set later
}

type rangeTask struct {
	start int64
	end   int64
	err   error
}

func New() *wsCenter {
	return &wsCenter{
		lock:  &sync.Mutex{},
		conns: map[uint64]*wsTask{},
	}
}

func (w *wsCenter) Status() []uint64 {
	var ids = []uint64{}
	w.lock.Lock()
	for id := range w.conns {
		ids = append(ids, id)
	}
	w.lock.Unlock()
	return ids
}

// keep runing , if return ws closed
func (w *wsCenter) Subscribe(ctx context.Context, c *websocket.Conn, url string) error {
	var (
		id   = util.Uqid()
		task = &wsTask{ctx, c, url, 0}
	)
	w.lock.Lock()
	w.conns[id] = task
	w.lock.Unlock()

	var (
		rtask            = task.read()
		fetchCtx, cancel = context.WithCancel(ctx)
	)

	defer func() {
		w.lock.Lock()
		delete(w.conns, id)
		w.lock.Unlock()
		cancel()
	}()

	for {
		select {
		case <-ctx.Done():
			cancel()
			return ctx.Err()
		case item, ok := <-rtask:
			cancel()
			if !ok {
				// channel closed, ws maybe closed
				return nil
			}
			if item.err != nil {
				// error occurred, it's ws connection error, we stoped
				return item.err
			}
			if item.start != -1 && item.end != -1 {
				// we have work to do
				fetchCtx, cancel = context.WithCancel(ctx)
				go func() {
					if err := task.write(item.start, item.end, fetchCtx); IsError(err) {
						if err != io.EOF {
							util.Log.Print(err)
						}
					}
				}()
			}
		}
	}
}

// the error maybe ctx.Err or c.Read , those errors all caused by ws connection
// so if error , we stop all the task
func (t *wsTask) read() chan *rangeTask {
	var queue = make(chan *rangeTask)
	go func() {
		defer close(queue)
		for {
			select {
			case <-t.ctx.Done():
				queue <- &rangeTask{-1, -1, t.ctx.Err()}
				return
			default:
				msgType, data, err := t.c.Read(t.ctx)
				if err != nil {
					queue <- &rangeTask{-1, -1, err}
					return
				}
				var (
					j      = gjson.ParseBytes(data)
					action = j.Get("type").String()
				)
				switch action {
				case "req":
					var (
						start = j.Get("start").Int()
						end   = j.Get("end").Int()
					)
					queue <- &rangeTask{start, end, nil}
				case "quit":
					queue <- &rangeTask{-1, -1, nil}
				default:
					util.Log.Print(msgType, action, data)
				}
			}
		}
	}()
	return queue
}

// do one range task
func (t *wsTask) write(start int64, end int64, ctx context.Context) error {
	r, status, total, err := getResponse(t.url, start, end, ctx)
	if err != nil {
		if !IsError(err) {
			return nil
		}
		var v = map[string]interface{}{
			"type":   "error",
			"status": status,
			"msg":    err.Error(),
		}
		if bs, err := json.Marshal(v); err == nil {
			return writeTimeout(ctx, websocket.MessageText, t.c, bs)
		} else {
			return err
		}
	}
	defer r.Close()
	if t.total < 1 {
		t.total = total
	} else {
		if t.total != total {
			return errors.New("total size changed,the url resource may changed")
		}
	}
	var buffer = make([]byte, 1048576)
	var offset = start
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			n, err := r.Read(buffer)
			if err != nil {
				return err
			}
			var data = buildMsg(total, offset, offset+int64(n), buffer[:n])
			offset += int64(n)
			err = writeTimeout(ctx, websocket.MessageBinary, t.c, data)
			if err != nil {
				return err
			}
		}
	}
}

func IsError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
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

func getResponse(url string, start int64, end int64, ctx context.Context) (io.ReadCloser, int, int64, error) {
	if end > 0 && end < start {
		return nil, 0, 0, fmt.Errorf("invalid range")
	}
	var (
		ran     = fmt.Sprintf("bytes=%d-", start)
		headers = http.Header{}
	)
	if end > 0 && end >= start {
		ran = fmt.Sprintf("bytes=%d-%d", start, end)
	}
	headers.Set("Range", ran)
	var (
		r      io.ReadCloser
		total  int64
		err    error
		status int
		times  = 0
	)
	for {
		r, status, total, err = getRetry(url, headers, ctx)
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
func getRetry(url string, headers http.Header, ctx context.Context) (io.ReadCloser, int, int64, error) {
	resp, err := request.GetWithContext(url, headers, ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	if !(resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent) {
		return nil, resp.StatusCode, 0, fmt.Errorf("%s : %s", url, resp.Status)
	}
	var (
		filesize    int64 = -1
		cr                = resp.Header.Get("Content-Range")
		rangeResReg       = regexp.MustCompile(`\d+/(\d+)`)
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

// binary msg to send
func buildMsg(total int64, start int64, end int64, data []byte) []byte {
	return append(chunkHeader(start, end, total), data...)
}

// ws binary chunk header,frontend to parse,header is 30 bytes
// [start,end,total]
func chunkHeader(start int64, end int64, total int64) []byte {
	var header = fmt.Sprintf(`[%d,%d,%d]`, start, end, total)
	return []byte(fmt.Sprintf("%-30s", header))
}
