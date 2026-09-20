package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
	"os"
)

type ChatRequest struct {
	Prompt string `json:"prompt"`
}
// Handler for POST /chat
func chatWithWoble(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	postBody, err := json.Marshal(map[string]interface{}{
		"input": map[string]string{"prompt": req.Prompt},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	apiKey := os.Getenv("RUNPOD_KEY")
	endpointID := "qyjxyr0boao2ry"
	client := &http.Client{Timeout: 15 * time.Second}

	// 1. Submit the job (returns immediately with an id)
	submitReq, _ := http.NewRequest(http.MethodPost,
		"https://api.runpod.ai/v2/"+endpointID+"/run",
		bytes.NewBuffer(postBody))
	submitReq.Header.Set("Content-Type", "application/json")
	submitReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(submitReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	var submitResp struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&submitResp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 2. Poll for the result
	statusURL := "https://api.runpod.ai/v2/" + endpointID + "/status/" + submitResp.ID
	deadline := time.Now().Add(5 * time.Minute) // generous cold-start budget

	type RunPodResponse struct {
		Status string `json:"status"`
		Output struct {
			Output string `json:"output"`
			Error  string `json:"error"`
		} `json:"output"`
	}

	for time.Now().Before(deadline) {
		statusReq, _ := http.NewRequest(http.MethodGet, statusURL, nil)
		statusReq.Header.Set("Authorization", "Bearer "+apiKey)

		sResp, err := client.Do(statusReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		body, _ := io.ReadAll(sResp.Body)
		sResp.Body.Close()

		var rpResp RunPodResponse
		if err := json.Unmarshal(body, &rpResp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		switch rpResp.Status {
		case "COMPLETED":
			if rpResp.Output.Error != "" {
				http.Error(w, rpResp.Output.Error, http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"reply": rpResp.Output.Output})
			return
		case "FAILED":
			http.Error(w, "job failed: "+string(body), http.StatusBadGateway)
			return
		default: // IN_QUEUE, IN_PROGRESS
			time.Sleep(2 * time.Second)
		}
	}

	http.Error(w, "job timed out waiting for RunPod", http.StatusGatewayTimeout)
}


func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/chat", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		switch r.Method {
		case http.MethodOptions:
			w.WriteHeader(http.StatusOK)
		case http.MethodPost:
			chatWithWoble(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})


	fmt.Println("Server starting on :8080")
	http.ListenAndServe(":8080", mux)
}