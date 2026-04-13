package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Configuration constants
const (
	defaultPort                = "8080"
	upstreamAPIURL             = "https://api.genderize.io/"
	upstreamAPITimeout         = 5 * time.Second
	minConfidenceScore         = 0.7
	minSampleSizeForConfidence = 100
)

// ---- Response shapes --------------------------------------------------------

// successResponse is the wrapper for successful classification results
type successResponse struct {
	Status string       `json:"status"` // Always "success"
	Data   classifyData `json:"data"`   // Gender classification data
}

// classifyData contains the gender prediction result and metadata
type classifyData struct {
	Name        string  `json:"name"`         // Input name (normalized by API)
	Gender      string  `json:"gender"`       // "male" or "female"
	Probability float64 `json:"probability"`  // Likelihood between 0.0 and 1.0
	SampleSize  int     `json:"sample_size"`  // Number of samples used for prediction
	IsConfident bool    `json:"is_confident"` // True if probability >= 0.7 AND sample_size >= 100
	ProcessedAt string  `json:"processed_at"` // ISO 8601 timestamp of processing
}

// errorResponse is returned for failed requests
type errorResponse struct {
	Status  string `json:"status"`  // Always "error"
	Message string `json:"message"` // Human-readable error explanation
}

// ---- Genderize API shape ----------------------------------------------------

// genderizeResponse is the structure returned by upstream Genderize.io API
type genderizeResponse struct {
	Name        string  `json:"name"`        // Name echoed back from the API
	Gender      *string `json:"gender"`      // NULL if no prediction available
	Probability float64 `json:"probability"` // Confidence of the prediction
	Count       int     `json:"count"`       // Sample size from the API database
}

// ---- Helpers ----------------------------------------------------------------

// writeJSON writes a JSON response with proper headers and CORS support
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// writeError writes a structured error response
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Status: "error", Message: message})
}

// validateNameParameter validates the "name" query parameter
// Returns the name if valid, or an error status and message if invalid
func validateNameParameter(r *http.Request) (string, int, string, bool) {
	nameParam, ok := r.URL.Query()["name"]

	// Check if parameter exists and is not empty
	if !ok || len(nameParam) == 0 {
		return "", http.StatusBadRequest, "missing required query parameter: name", false
	}

	name := nameParam[0]

	// Reject whitespace-only names
	if strings.TrimSpace(name) == "" {
		return "", http.StatusBadRequest, "name parameter must not be empty", false
	}

	// Reject JSON/array-like inputs (security + user-friendliness)
	if strings.HasPrefix(name, "{") || strings.HasPrefix(name, "[") {
		return "", http.StatusUnprocessableEntity, "name must be a string, not a structured value", false
	}

	return name, http.StatusOK, "", true
}

// callGenderizeAPI makes a request to the upstream Genderize.io service
// Returns the parsed response or an error status and message
func callGenderizeAPI(name string) (*genderizeResponse, int, string, bool) {
	// Build the API URL with proper URL encoding
	apiURL := fmt.Sprintf("%s?name=%s", upstreamAPIURL, url.QueryEscape(name))

	// Use a timeout to avoid hanging on slow upstream responses
	client := &http.Client{Timeout: upstreamAPITimeout}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, http.StatusBadGateway, "failed to reach upstream API", false
	}
	defer resp.Body.Close()

	// Ensure the upstream API returned success
	if resp.StatusCode != http.StatusOK {
		return nil, http.StatusBadGateway, "upstream API returned an unexpected status", false
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, http.StatusInternalServerError, "failed to read upstream response", false
	}

	// Parse the JSON response
	var gResp genderizeResponse
	if err := json.Unmarshal(body, &gResp); err != nil {
		return nil, http.StatusInternalServerError, "failed to parse upstream response", false
	}

	return &gResp, http.StatusOK, "", true
}

// isConfidentPrediction determines if a prediction is reliable enough
// A prediction is confident if: probability >= 0.7 AND sample_size >= 100
func isConfidentPrediction(probability float64, sampleSize int) bool {
	return probability >= minConfidenceScore && sampleSize >= minSampleSizeForConfidence
}

// ---- Handler ----------------------------------------------------------------

// classifyHandler processes gender classification requests
// GET /api/classify?name=<string>
func classifyHandler(w http.ResponseWriter, r *http.Request) {
	// Handle CORS preflight requests
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Only accept GET requests
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Step 1: Validate the "name" query parameter
	name, errorStatus, errorMsg, valid := validateNameParameter(r)
	if !valid {
		writeError(w, errorStatus, errorMsg)
		return
	}

	// Step 2: Call the upstream Genderize API
	gResp, errorStatus, errorMsg, success := callGenderizeAPI(name)
	if !success {
		writeError(w, errorStatus, errorMsg)
		return
	}

	// Step 3: Validate that we got a prediction from the API
	// (Gender is nil or Count is 0 means no data available for this name)
	if gResp.Gender == nil || gResp.Count == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no prediction available for the provided name")
		return
	}

	// Step 4: Build and send the response
	isConfident := isConfidentPrediction(gResp.Probability, gResp.Count)

	response := successResponse{
		Status: "success",
		Data: classifyData{
			Name:        gResp.Name,
			Gender:      *gResp.Gender,
			Probability: gResp.Probability,
			SampleSize:  gResp.Count,
			IsConfident: isConfident,
			ProcessedAt: time.Now().UTC().Format(time.RFC3339),
		},
	}

	writeJSON(w, http.StatusOK, response)
}

// ---- Main -------------------------------------------------------------------

// main initializes the HTTP server and starts listening for requests
func main() {
	// Read PORT from environment, or use the default
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	// Create a multiplexer and register routes
	mux := http.NewServeMux()
	mux.HandleFunc("/api/classify", classifyHandler)

	// Health check endpoint (useful for cloud platforms like Railway, Fly.io, Heroku)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Start the server
	addr := fmt.Sprintf(":%s", port)
	log.Printf("🚀 Server listening on %s", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("❌ Server failed: %v", err)
	}
}
