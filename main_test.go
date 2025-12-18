package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthHandler(t *testing.T) {
	w := httptest.NewRecorder()

	healthHandler(w, nil)
	desiredStatus := http.StatusOK
	if w.Code != desiredStatus {
		t.Errorf("Expected status %v, got %v and body %s",
			desiredStatus, w.Code, w.Body.String())
	}

}
