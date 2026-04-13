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

// ---- Response shapes --------------------------------------------------------

type successResponse struct {
	Status string      `json:"status"`
	Data   classifyData `json:"data"`
}

type classifyData struct {
	Name        string  `json:"name"`
	Gender      string  `json:"gender"`
	Probability float64 `json:"probability"`
	SampleSize  int     `json:"sample_size"`
	IsConfident bool    `json:"is_confident"`
	ProcessedAt string  `json:"processed_at"`
}

type errorResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// ---- Genderize API shape ----------------------------------------------------

type genderizeResponse struct {
	Name        string   `json:"name"`
	Gender      *string  `json:"gender"`       // nullable
	Probability float64  `json:"probability"`
	Count       int      `json:"count"`
}

// ---- Helpers ----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Status: "error", Message: message})
}

// ---- Handler ----------------------------------------------------------------

func classifyHandler(w http.ResponseWriter, r *http.Request) {
	// CORS preflight
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// --- 1. Validate query parameter -----------------------------------------

	nameParam, ok := r.URL.Query()["name"]

	// Missing key entirely
	if !ok || len(nameParam) == 0 {
		writeError(w, http.StatusBadRequest, "missing required query parameter: name")
		return
	}

	name := nameParam[0]

	// Empty string
	if strings.TrimSpace(name) == "" {
		writeError(w, http.StatusBadRequest, "name parameter must not be empty")
		return
	}

	// Non-string check: if the value contains characters that cannot be a name
	// (e.g. JSON objects/arrays passed as query values), treat as unprocessable.
	// A simple heuristic: reject values that start with { or [
	if strings.HasPrefix(name, "{") || strings.HasPrefix(name, "[") {
		writeError(w, http.StatusUnprocessableEntity, "name must be a string, not a structured value")
		return
	}

	// --- 2. Call Genderize API -----------------------------------------------

	apiURL := fmt.Sprintf("https://api.genderize.io/?name=%s", url.QueryEscape(name))

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to reach upstream API")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, "upstream API returned an unexpected status")
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read upstream response")
		return
	}

	var gResp genderizeResponse
	if err := json.Unmarshal(body, &gResp); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse upstream response")
		return
	}

	// --- 3. Handle edge cases: null gender or zero count ---------------------

	if gResp.Gender == nil || gResp.Count == 0 {
		writeError(w, http.StatusUnprocessableEntity, "No prediction available for the provided name")
		return
	}

	// --- 4. Process & respond ------------------------------------------------

	isConfident := gResp.Probability >= 0.7 && gResp.Count >= 100

	writeJSON(w, http.StatusOK, successResponse{
		Status: "success",
		Data: classifyData{
			Name:        gResp.Name,
			Gender:      *gResp.Gender,
			Probability: gResp.Probability,
			SampleSize:  gResp.Count,
			IsConfident: isConfident,
			ProcessedAt: time.Now().UTC().Format(time.RFC3339),
		},
	})
}

// ---- Main -------------------------------------------------------------------

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/classify", classifyHandler)

	// Health check — useful for platforms like Railway/Fly
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	addr := fmt.Sprintf(":%s", port)
	log.Printf("Server listening on %s", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
