package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	_ "github.com/lib/pq"
)

type ImageMetadata struct {
	ID          int       `json:"id"`
	Filename    string    `json:"filename"`
	Size        int64     `json:"size"`
	ObjectKey   string    `json:"object_key"` // S3 Object Key
	ContentType string    `json:"content_type"`
	CreatedAt   time.Time `json:"created_at"`
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
	db       *sql.DB
	s3Client *s3.Client
	bucket   string
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
		size BIGINT,
		object_key text not null,
		content_type text,
		created_at TIMESTAMP NOT NULL
	)`)
	if err != nil {
		log.Fatal("Could not create table:", err)
	}

	// 1. Load the Default Config (Just Credentials & Region)
	// We do NOT set the endpoint here anymore.
	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion("us-east-1"), // MinIO needs a region, even if fake
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			os.Getenv("S3_ACCESS_KEY"),
			os.Getenv("S3_SECRET_KEY"),
			"", // Session Token (empty)
		)),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 2. Create the S3 Client with MinIO Specific Options
	s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		// --- THE MODERN WAY ---
		// Use BaseEndpoint instead of a custom resolver
		s3Endpoint := "http://" + os.Getenv("S3_ENDPOINT")
		o.BaseEndpoint = aws.String(s3Endpoint)

		// Required for MinIO (Forces http://host/bucket/file instead of http://bucket.host/file)
		o.UsePathStyle = true
	})

	// 3. Create Bucket (Quick check)
	bucketName := os.Getenv("S3_BUCKET")
	_, _ = s3Client.CreateBucket(context.TODO(), &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})

	app := &App{
		db:       db,
		s3Client: s3Client,
		bucket:   bucketName,
	}

	// 1. The Router (ServeMux)
	// In Go std lib, we use a "Mux" (Multiplexer) to match URLs to functions.
	mux := http.NewServeMux()

	// 2. Register Routes
	// We map the URL path to a handler function
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/images/", app.imagesHandler)
	mux.HandleFunc("/images", app.imagesHandler)
	mux.HandleFunc("/download/", app.downloadImage)

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
	case http.MethodDelete:
		app.deleteImage(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// Logic for GET
func (app *App) listImages(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	rows, err := app.db.Query("select id, filename, size, object_key, content_type, created_at from images order by id")
	if err != nil {
		http.Error(w, "Failed to query database", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var images []ImageMetadata
	for rows.Next() {
		var img ImageMetadata
		if err := rows.Scan(&img.ID, &img.Filename, &img.Size, &img.ObjectKey, &img.ContentType, &img.CreatedAt); err != nil {
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
	// 1. Parse Multipart Form (Max 10MB)
	r.ParseMultipartForm(10 << 20)

	// 2. Retrieve file
	file, handler, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "Error retrieving file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fmt.Printf("Uploading File: %+v\n", handler.Filename)

	fmt.Printf("Body: File: %v\n", file)
	// 3. Upload to S3 (MinIO) - V2 Syntax
	_, err = app.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:      aws.String(app.bucket),
		Key:         aws.String(handler.Filename),
		Body:        file,
		ContentType: aws.String(handler.Header.Get("Content-Type")),
	})

	if err != nil {
		http.Error(w, "Failed to upload to S3", http.StatusInternalServerError)
		fmt.Println("S3 Upload Error:", err)
		return
	}

	// 4. Save Metadata to DB (Same as before)
	var id int
	var createdAt time.Time = time.Now()
	err = app.db.QueryRow(`INSERT INTO images (filename, size, object_key, content_type, created_at) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		handler.Filename, handler.Size, handler.Filename, handler.Header.Get("Content-Type"), createdAt).Scan(&id)

	if err != nil {
		http.Error(w, "Database insert failed", http.StatusInternalServerError)
		return
	}

	// 5. Respond
	response := ImageMetadata{
		ID:          id,
		Filename:    handler.Filename,
		Size:        handler.Size,
		ObjectKey:   handler.Filename,
		ContentType: handler.Header.Get("Content-Type"),
		CreatedAt:   createdAt,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Logic for delete

func (app *App) deleteImage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/images/")
	if id == "" {
		http.Error(w, "Image ID is required", http.StatusBadRequest)
		return
	}
	imageID, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "Invalid Image ID", http.StatusBadRequest)
		return
	}
	// 1. Get Object Key from DB
	var objectKey string
	err = app.db.QueryRow("SELECT object_key FROM images WHERE id=$1", imageID).Scan(&objectKey)
	if err != nil {
		http.Error(w, "Image not found", http.StatusNotFound)
		return
	}
	// 2. Delete from S3
	_, err = app.s3Client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
		Bucket: aws.String(app.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		http.Error(w, "Failed to delete from S3", http.StatusInternalServerError)
		return
	}
	// 3. Delete from DB
	_, err = app.db.Exec("DELETE FROM images WHERE id=$1", imageID)
	if err != nil {
		http.Error(w, "Failed to delete from database", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)

}

func (app *App) downloadImage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/download/")
	if id == "" {
		http.Error(w, "Image ID is required", http.StatusBadRequest)
		return
	}
	imageID, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "Invalid Image ID", http.StatusBadRequest)
		return
	}
	var objectKey string
	err = app.db.QueryRow("SELECT object_key FROM images WHERE id=$1", imageID).Scan(&objectKey)
	if err != nil {
		http.Error(w, "Image not found", http.StatusNotFound)
		return
	}
	resp, err := app.s3Client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(app.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		http.Error(w, "Failed to download from S3", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	ct := "application/octet-stream"
	if resp.ContentType != nil && *resp.ContentType != "" {
		ct = *resp.ContentType
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", objectKey))
	io.Copy(w, resp.Body)
}
