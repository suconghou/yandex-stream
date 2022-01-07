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

func InArray(x int64, arr []int64) bool {
	for _, n := range arr {
		if x == n {
			return true
		}
	}
	return false
}

func removeDuplication(arr []int64) []int64 {
	set := make(map[int64]struct{}, len(arr))
	j := 0
	for _, v := range arr {
		_, ok := set[v]
		if ok {
			continue
		}
		set[v] = struct{}{}
		arr[j] = v
		j++
	}

	return arr[:j]
}

func SplitGroup(arr []int64) [][]int64 {
	arr = removeDuplication(arr)
	var (
		start = arr[0]
		n     = 0
		group = []int64{}
		ret   = [][]int64{}
	)
	for _, x := range arr {
		if x == start+int64(n) {
			group = append(group, x)
			n++
		} else {
			ret = append(ret, group)
			group = []int64{x}
			start = x
			n = 1
		}
	}
	ret = append(ret, group)
	return ret
}
