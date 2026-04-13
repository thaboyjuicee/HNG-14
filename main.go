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

const (
	defaultPort                = "8080"
	upstreamAPIURL             = "https://api.genderize.io/"
	upstreamAPITimeout         = 5 * time.Second
	minConfidenceScore         = 0.7
	minSampleSizeForConfidence = 100
)

type successResponse struct {
	Status string       `json:"status"`
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

type genderizeResponse struct {
	Name        string  `json:"name"`
	Gender      *string `json:"gender"`
	Probability float64 `json:"probability"`
	Count       int     `json:"count"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Status: "error", Message: message})
}

func validateNameParameter(r *http.Request) (string, int, string, bool) {
	nameParam, ok := r.URL.Query()["name"]
	if !ok || len(nameParam) == 0 {
		return "", http.StatusBadRequest, "missing required query parameter: name", false
	}

	name := nameParam[0]
	if strings.TrimSpace(name) == "" {
		return "", http.StatusBadRequest, "name parameter must not be empty", false
	}

	if strings.HasPrefix(name, "{") || strings.HasPrefix(name, "[") {
		return "", http.StatusUnprocessableEntity, "name must be a string, not a structured value", false
	}

	return name, http.StatusOK, "", true
}

func callGenderizeAPI(name string) (*genderizeResponse, int, string, bool) {
	apiURL := fmt.Sprintf("%s?name=%s", upstreamAPIURL, url.QueryEscape(name))
	client := &http.Client{Timeout: upstreamAPITimeout}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, http.StatusBadGateway, "failed to reach upstream API", false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, http.StatusBadGateway, "upstream API returned an unexpected status", false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, http.StatusInternalServerError, "failed to read upstream response", false
	}

	var gResp genderizeResponse
	if err := json.Unmarshal(body, &gResp); err != nil {
		return nil, http.StatusInternalServerError, "failed to parse upstream response", false
	}

	return &gResp, http.StatusOK, "", true
}

func isConfidentPrediction(probability float64, sampleSize int) bool {
	return probability >= minConfidenceScore && sampleSize >= minSampleSizeForConfidence
}

func classifyHandler(w http.ResponseWriter, r *http.Request) {
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

	name, errorStatus, errorMsg, valid := validateNameParameter(r)
	if !valid {
		writeError(w, errorStatus, errorMsg)
		return
	}

	gResp, errorStatus, errorMsg, success := callGenderizeAPI(name)
	if !success {
		writeError(w, errorStatus, errorMsg)
		return
	}

	if gResp.Gender == nil || gResp.Count == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no prediction available for the provided name")
		return
	}

	response := successResponse{
		Status: "success",
		Data: classifyData{
			Name:        gResp.Name,
			Gender:      *gResp.Gender,
			Probability: gResp.Probability,
			SampleSize:  gResp.Count,
			IsConfident: isConfidentPrediction(gResp.Probability, gResp.Count),
			ProcessedAt: time.Now().UTC().Format(time.RFC3339),
		},
	}

	writeJSON(w, http.StatusOK, response)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/classify", classifyHandler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	addr := fmt.Sprintf(":%s", port)
	log.Printf("🚀 Server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("❌ Server failed: %v", err)
	}
}
