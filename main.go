package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"
)

type ImageMetadata struct {
	ID       int    `json:"id"`
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
	db *sql.DB
}

// 1. Middleware is just a function that takes a Handler and returns a Handler
func LoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		start := time.Now()

		recorder := &StatusRecorder{ResponseWriter: w, StatusCode: http.StatusOK}
		next.ServeHTTP(recorder, r)

		elapsed := time.Since(start)
		color := "\033[32m" // Green
		if recorder.StatusCode >= 400 && recorder.StatusCode < 500 {
			color = "\033[31m" // Red
		}

		fmt.Printf("%s %d Method [%s] path %s  duration %v\n", color, recorder.StatusCode, r.Method, r.URL.Path, elapsed)

	})
}

func main() {

	// 1. Get Config from Docker Environment
	connStr := fmt.Sprintf("host=%s user=%s password=%s dbname=%s sslmode=disable",
		os.Getenv("DB_HOST"),
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_NAME"),
	)
	// 2. Connect to Database
	var db *sql.DB
	var err error
	for i := 0; i < 5; i++ {
		db, err = sql.Open("postgres", connStr)
		if err == nil {
			err = db.Ping()
		}
		if err == nil {
			break // Successfully connected
		}
		fmt.Println("Waiting for DB to be ready...", err)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		log.Fatal("Could not connect to the database:", err)
	}
	// 3. Create the Table (Migration)
	_, err = db.Exec(`Create table if not exists images (
		id serial PRIMARY KEY,
		filename text not null, 
		size BIGINT
	)`)
	if err != nil {
		log.Fatal("Could not create table:", err)
	}
	app := &App{db: db}

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

	rows, err := app.db.Query("select id, filename, size from images order by id")
	if err != nil {
		http.Error(w, "Failed to query database", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var images []ImageMetadata
	for rows.Next() {
		var img ImageMetadata
		if err := rows.Scan(&img.ID, &img.Filename, &img.Size); err != nil {
			http.Error(w, "Failed to scan row", http.StatusInternalServerError)
			return
		}
		images = append(images, img)
	}

	// Manually encode the Go slice into JSON
	encoder := json.NewEncoder(w)
	if err := encoder.Encode(images); err != nil {
		http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
	}
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

	err := app.db.QueryRow(`insert into images (filename, size) values ($1, $2) returning id`,
		newImage.Filename, newImage.Size).
		Scan(&newImage.ID)
	if err != nil {
		http.Error(w, "Failed to insert into database", http.StatusInternalServerError)
		return
	}

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newImage)
}
