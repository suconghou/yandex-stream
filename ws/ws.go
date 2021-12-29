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
	lock  *sync.RWMutex
	conns map[uint64]*wsTask
}

type wsTask struct {
	ctx   context.Context
	c     *websocket.Conn
	url   string
	total int64 // cached total file size, will be set later
}

type rangeTask struct {
	part int64
	err  error
}

const (
	partLen  = 1048576 // 1MB
	chunkLen = 65536   // 64KB
)

func New() *wsCenter {
	return &wsCenter{
		lock:  &sync.RWMutex{},
		conns: map[uint64]*wsTask{},
	}
}

func (w *wsCenter) Status() []uint64 {
	var ids = []uint64{}
	w.lock.RLock()
	for id := range w.conns {
		ids = append(ids, id)
	}
	w.lock.RUnlock()
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
			if item.part != -1 {
				// we have work to do
				fetchCtx, cancel = context.WithCancel(ctx)
				go func() {
					if err := task.write(item.part, fetchCtx); IsError(err) {
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
				queue <- &rangeTask{-1, t.ctx.Err()}
				return
			default:
				_, data, err := t.c.Read(t.ctx)
				if err != nil {
					queue <- &rangeTask{-1, err}
					return
				}
				var (
					j      = gjson.ParseBytes(data)
					action = j.Get("type").String()
				)
				switch action {
				case "req":
					queue <- &rangeTask{j.Get("part").Int(), nil}
				case "quit":
					queue <- &rangeTask{-1, nil}
				}
			}
		}
	}()
	return queue
}

// do one range task
func (t *wsTask) write(part int64, ctx context.Context) error {
	start, end, err := calc(part, t.total)
	if err != nil {
		return t.writeErrorMsg(-1, err, ctx)
	}
	r, status, total, err := getResponse(t.url, start, end, ctx)
	if err != nil {
		if !IsError(err) {
			return nil
		}
		return t.writeErrorMsg(status, err, ctx)
	}
	defer r.Close()
	if t.total < 1 {
		t.total = total
	} else {
		if t.total != total {
			err = errors.New("total size changed,the url resource may changed")
			return t.writeErrorMsg(-2, err, ctx)
		}
	}
	buffer, err := io.ReadAll(io.LimitReader(r, partLen))
	if err != nil {
		return t.writeErrorMsg(-4, err, ctx)
	}
	for i, buf := range splitBuffer(buffer) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err = writeTimeout(ctx, websocket.MessageBinary, t.c, buildMsg(part, i, total, buf)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *wsTask) writeErrorMsg(status int, err error, ctx context.Context) error {
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
	var headers = http.Header{}
	headers.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
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

func calc(part int64, total int64) (int64, int64, error) {
	var (
		start = part * partLen
		end   = (part+1)*partLen - 1
	)
	if total < 1 {
		return start, end, nil
	}
	if start >= total {
		return start, end, fmt.Errorf("invalid range")
	}
	if end >= total {
		end = total - 1
	}
	return start, end, nil
}

// binary msg to send
func buildMsg(part int64, index int, total int64, data []byte) []byte {
	return append(chunkHeader(part, index, total), data...)
}

// ws binary chunk header,frontend to parse,header is 30 bytes
// [part,index,total]
func chunkHeader(part int64, index int, total int64) []byte {
	var header = fmt.Sprintf(`[%d,%d,%d]`, part, index, total)
	return []byte(fmt.Sprintf("%-30s", header))
}

func splitBuffer(bs []byte) [][]byte {
	var (
		buffers = [][]byte{}
		start   = 0
		end     = 0
		l       = len(bs)
		data    []byte
	)
	for {
		if start >= l {
			break
		}
		end = start + chunkLen
		if end > l {
			end = l
		}
		data = bs[start:end]
		buffers = append(buffers, data)
		start = end
	}
	return buffers
}
