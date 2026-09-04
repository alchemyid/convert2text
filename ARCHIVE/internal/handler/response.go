package handler

import (
	"encoding/json"
	"net/http"
)

// StandardResponse is the unified JSON envelope for API responses.
type StandardResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// JSONSuccess writes a successful JSON response.
func JSONSuccess(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(StandardResponse{
		Success: true,
		Data:    data,
	})
}

// JSONError writes an error JSON response.
func JSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(StandardResponse{
		Success: false,
		Error:   message,
	})
}
