package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func IsMockProfile(profile string) bool { return profile != "v2_langchain_bailian" }
func NewRequestID() string              { return fmt.Sprintf("v2-%d", time.Now().UnixNano()) }
func DecodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errors.New("request body too large")
	}
	return json.Unmarshal(data, dst)
}
func WriteJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(value)
}
func WriteError(w http.ResponseWriter, statusCode int, code, message string) {
	WriteJSON(w, statusCode, Error{Code: code, Message: message})
}

// WriteErrorCause adds a coarse cause identifier. Callers must pass an error
// class, never a message: reasons can carry DSNs, SQL or file paths.
func WriteErrorCause(w http.ResponseWriter, statusCode int, code, message, cause string) {
	WriteJSON(w, statusCode, map[string]any{"code": code, "message": message, "cause": cause})
}
