package api

import (
	"encoding/json"
	"net/http"
)

// response 是统一返回结构:{ "code": 0, "msg": "ok", "data": ... }
type response struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data,omitempty"`
}

func writeOK(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusOK, response{Code: 0, Msg: "ok", Data: data})
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, response{Code: status, Msg: msg})
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
