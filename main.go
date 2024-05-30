package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// BackendResponse struct untuk menangani respons dari backend
type BackendResponse struct {
	UUID     string `json:"uuid"`
	Hostname string `json:"hostname"`
	ExecTime string `json:"exec_time"`
}

// AggregatedResponse struct untuk respons dari client API
type AggregatedResponse struct {
	Responses     []BackendResponse `json:"responses"`
	Backend1Count int               `json:"backend1_count"`
	Backend2Count int               `json:"backend2_count"`
	TotalTime     string            `json:"total_time"`
}

func main() {
	http.HandleFunc("/aggregate", aggregateHandler)
	log.Println("Starting client API server on :8082")
	if err := http.ListenAndServe(":8082", nil); err != nil {
		log.Fatalf("Could not start server: %s\n", err.Error())
	}
}

func aggregateHandler(w http.ResponseWriter, r *http.Request) {
	backend01 := "GoBackend01"
	backend02 := "GoBackend02"

	query := r.URL.Query()
	countStr := query.Get("count")
	if countStr == "" {
		http.Error(w, "count parameter is required", http.StatusBadRequest)
		return
	}

	count, err := strconv.Atoi(countStr)
	if err != nil {
		http.Error(w, "invalid count parameter", http.StatusBadRequest)
		return
	}

	backendURL := "http://localhost:8081/uuid"
	var responses []BackendResponse
	var backend1Count, backend2Count int

	var wg sync.WaitGroup
	var mu sync.Mutex

	totalStartTime := time.Now()

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			startTime := time.Now()
			resp, err := http.Get(backendURL)
			if err != nil {
				fmt.Println("Error while calling backend:", err)
				return
			}
			defer resp.Body.Close()

			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				fmt.Println("Error while reading response body:", err)
				return
			}

			endTime := time.Now()

			var backendResp BackendResponse
			err = json.Unmarshal(body, &backendResp)
			if err != nil {
				fmt.Println("Error while unmarshalling JSON response:", err)
				return
			}

			mu.Lock()
			responses = append(responses, BackendResponse{
				UUID:     backendResp.UUID,
				Hostname: backendResp.Hostname,
				ExecTime: fmt.Sprintf("%d ms", endTime.Sub(startTime).Milliseconds()),
			})
			if backendResp.Hostname == backend01 {
				backend1Count++
			} else if backendResp.Hostname == backend02 {
				backend2Count++
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	totalEndTime := time.Now()

	// // Calculate time taken for each request
	// for i := range responses {
	// 	responses[i].StartTime = responses[i].StartTime / int64(time.Millisecond)
	// 	responses[i].EndTime = responses[i].EndTime / int64(time.Millisecond)
	// }

	aggregatedResponse := AggregatedResponse{
		Responses:     responses,
		Backend1Count: backend1Count,
		Backend2Count: backend2Count,
		TotalTime:     fmt.Sprintf("%d ms", totalEndTime.Sub(totalStartTime).Milliseconds()),
	}

	jsonResponse, err := json.Marshal(aggregatedResponse)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonResponse)
}
