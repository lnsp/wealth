package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       any
		wantStatus int
	}{
		{
			name:       "200 with map",
			status:     http.StatusOK,
			body:       map[string]string{"message": "ok"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "201 with struct",
			status:     http.StatusCreated,
			body:       struct{ ID int }{ID: 42},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "404 with error",
			status:     http.StatusNotFound,
			body:       map[string]string{"error": "not found"},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeJSON(w, tt.status, tt.body)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}

			var decoded map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
				t.Errorf("response is not valid JSON: %v", err)
			}
		})
	}
}

func TestWriteError(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		message    string
	}{
		{name: "bad request", status: http.StatusBadRequest, message: "invalid input"},
		{name: "not found", status: http.StatusNotFound, message: "resource not found"},
		{name: "internal error", status: http.StatusInternalServerError, message: "something went wrong"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeError(w, tt.status, tt.message)

			if w.Code != tt.status {
				t.Errorf("status = %d, want %d", w.Code, tt.status)
			}

			var body map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			if body["error"] != tt.message {
				t.Errorf("error = %q, want %q", body["error"], tt.message)
			}
		})
	}
}
