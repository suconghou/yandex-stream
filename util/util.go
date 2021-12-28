package util

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync/atomic"
)

var (
	ops uint64
	// Log write to stdout
	Log = log.New(os.Stdout, "", log.Ldate|log.Ltime|log.Lshortfile)
)

// JSONPut resp json,如果v是byte类型,我们应直接使用,byte类型再json.Marshal就是base64字符串了,string类型经json.Marshal后转为对于的byte
func JSONPut(w http.ResponseWriter, v interface{}, status int, age int) (int, error) {
	var (
		bs  []byte
		err error
	)
	if bb, ok := v.([]byte); !ok {
		if bs, err = json.Marshal(v); err != nil {
			return 0, err
		}
	} else {
		bs = bb
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Max-Age", "864000")
	h.Set("Cache-Control", fmt.Sprintf("public,max-age=%d", age))
	w.WriteHeader(status)
	return w.Write(bs)
}

// Uqid retrun counter
func Uqid() uint64 {
	return atomic.AddUint64(&ops, 1)
}
