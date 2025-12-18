package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// The same struct as before
type ImageMetadata struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

type StatusRecorder struct {
	http.ResponseWriter
	StatusCode int
}

func (rec *StatusRecorder) WriteHeader(code int) {
	rec.StatusCode = code
	rec.ResponseWriter.WriteHeader(code)
}

type App struct {
	db   []ImageMetadata
	lock sync.RWMutex // Read-Write Mutex: Allows many readers, but only one writer.
}

// --- THE CHALLENGE IS HERE ---
// 1. Middleware is just a function that takes a Handler and returns a Handler
func LoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO: Record the time right now (Start Timer)
		start := time.Now()
		// TODO: Call the 'next' handler (Pass the request down the chain)
		// Hint: next.ServeHTTP(w, r)
		recorder := &StatusRecorder{ResponseWriter: w, StatusCode: http.StatusOK}
		next.ServeHTTP(recorder, r)
		// TODO: Calculate how much time passed since the start
		elapsed := time.Since(start)
		color := "\033[32m" // Green
		if recorder.StatusCode >= 400 && recorder.StatusCode < 500 {
			color = "\033[31m" // Red
		}
		// TODO: Print the Method (GET/POST), Path, and Duration
		// Example: fmt.Printf("[%s] %s %v\n", r.Method, r.URL.Path, duration)
		fmt.Printf("%s %d Method [%s] path %s  duration %v\n", color, recorder.StatusCode, r.Method, r.URL.Path, elapsed)

	})
}

func main() {

	app := &App{
		db: []ImageMetadata{},
	}
	// 1. The Router (ServeMux)
	// In Go std lib, we use a "Mux" (Multiplexer) to match URLs to functions.
	mux := http.NewServeMux()

	// 2. Register Routes
	// We map the URL path to a handler function
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/images", app.imagesHandler)

	fmt.Println("Server starting on :8080...")

	loggerMiddleware := LoggerMiddleware(mux)
	// 3. Start the Server
	// ListenAndServe blocks forever, listening for requests
	if err := http.ListenAndServe(":8080", loggerMiddleware); err != nil {
		fmt.Println("Error starting server:", err)
	}
}

// HANDLER 1: Simple Health Check
// A "Handler" in Go always takes these two exact arguments.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	// Restrict to GET method
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("System is Running (Standard Lib)"))
}

// HANDLER 2: The "Router" for Images
// Since std lib (pre-Go 1.22) routes are simple, we often handle GET/POST inside one function.
func (app *App) imagesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		app.listImages(w, r)
	case http.MethodPost:
		app.createImage(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// Logic for GET
func (app *App) listImages(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	app.lock.RLock()
	defer app.lock.RUnlock()
	// Manually encode the Go slice into JSON
	encoder := json.NewEncoder(w)
	if err := encoder.Encode(app.db); err != nil {
		http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
	}
	fmt.Println(app.db)

}

// Logic for POST
func (app *App) createImage(w http.ResponseWriter, r *http.Request) {
	var newImage ImageMetadata

	// Manually decode the incoming JSON body into our struct
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&newImage); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	// Important: Always close the body to prevent memory leaks!
	defer r.Body.Close()
	app.lock.Lock()
	defer app.lock.Unlock()
	// "Save" to memory
	// images = append(images, newImage)
	app.db = append(app.db, newImage)

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "created"})
}
