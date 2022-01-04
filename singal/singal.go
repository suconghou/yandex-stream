package singal

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
	"yandex-stream/util"
	"yandex-stream/ws"

	"github.com/tidwall/gjson"
	"nhooyr.io/websocket"
)

type singalCenter struct {
	lock  *sync.RWMutex
	conns map[uint64]*singalUser
}

type singalUser struct {
	ctx context.Context
	c   *websocket.Conn
	uid string
}

var (
	ss = singalCenter{lock: &sync.RWMutex{}, conns: map[uint64]*singalUser{}}
)

func (s *singalCenter) status() map[uint64]string {
	var ids = map[uint64]string{}
	s.lock.RLock()
	for id, u := range s.conns {
		ids[id] = u.uid
	}
	s.lock.RUnlock()
	return ids
}

// if return , ws closed
func (s *singalCenter) subscribe(ctx context.Context, c *websocket.Conn, uid string) error {
	var (
		id            = util.Uqid()
		user          = &singalUser{ctx, c, uid}
		wsCtx, cancel = context.WithCancel(ctx)
	)
	s.lock.Lock()
	s.conns[id] = user
	s.lock.Unlock()

	defer func() {
		s.lock.Lock()
		delete(s.conns, id)
		s.lock.Unlock()
		cancel()
	}()

	var (
		data = map[string]interface{}{
			"event": "init",
			"ids":   s.collectids(uid),
		}
	)
	if bs, err := json.Marshal(data); err != nil {
		return err
	} else {
		if err = writeTimeout(wsCtx, websocket.MessageText, c, bs); err != nil {
			return err
		}
	}

	data = map[string]interface{}{
		"event": "online",
		"id":    uid,
	}
	if bs, err := json.Marshal(data); err != nil {
		return err
	} else {
		s.broadcastIf(wsCtx, websocket.MessageText, bs, func(u *singalUser) bool {
			return u.uid != uid
		})
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			t, data, err := c.Read(wsCtx)
			if err != nil {
				return err
			}
			var (
				j  = gjson.ParseBytes(data)
				to = j.Get("to").String()
			)
			if to != "" {
				s.broadcastIf(wsCtx, t, data, func(u *singalUser) bool {
					return u.uid == to
				})
			} else {
				util.Log.Print("unknow msg", data)
			}
		}
	}
}

func (s *singalCenter) broadcastIf(ctx context.Context, t websocket.MessageType, data []byte, cond func(*singalUser) bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	for _, u := range s.conns {
		if cond(u) {
			if err := writeTimeout(ctx, t, u.c, data); err != nil {
				util.Log.Print(err)
			}
			continue
		}
	}
}

func (s *singalCenter) collectids(except string) []string {
	var (
		uids   = []string{}
		uidmap = map[string]bool{}
	)
	s.lock.RLock()
	defer s.lock.RUnlock()
	for _, u := range s.conns {
		if u.uid == except {
			continue
		}
		if !uidmap[u.uid] {
			uids = append(uids, u.uid)
			uidmap[u.uid] = true
		}
	}
	return uids
}

func writeTimeout(ctx context.Context, msgType websocket.MessageType, c *websocket.Conn, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	return c.Write(ctx, msgType, msg)
}

func Handler(w http.ResponseWriter, r *http.Request, match []string) error {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return err
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	if err = ss.subscribe(r.Context(), c, match[1]); ws.IsError(err) {
		return err
	}
	return nil
}

func Status(w http.ResponseWriter, r *http.Request, match []string) error {
	var data = ss.status()
	_, err := util.JSONPut(w, data, http.StatusOK, 5)
	return err
}
