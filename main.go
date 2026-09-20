package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
	"github.com/joho/godotenv"
)

type ChatRequest struct {
	Prompt string `json:"prompt"`
}

type RunPodResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Output struct {
		Output string `json:"output"`
		Error  string `json:"error"`
	} `json:"output"`
}

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
	client := &http.Client{Timeout: 120 * time.Second}

	runReq, _ := http.NewRequest(http.MethodPost,
		"https://api.runpod.ai/v2/"+endpointID+"/runsync",
		bytes.NewBuffer(postBody))
	runReq.Header.Set("Content-Type", "application/json")
	runReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(runReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	log.Printf("runpod runsync: status=%d body=%s", resp.StatusCode, body)

	if resp.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("runpod returned status %d", resp.StatusCode), http.StatusBadGateway)
		return
	}

	var rpResp RunPodResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&rpResp); err != nil {
		http.Error(w, "unexpected response from generation service", http.StatusBadGateway)
		return
	}

	// runsync didn't finish in its own wait window (cold start) — poll the same job by id
	if rpResp.Status != "COMPLETED" && rpResp.Status != "FAILED" {
		if rpResp.ID == "" {
			http.Error(w, "job did not complete and no id was returned: "+string(body), http.StatusGatewayTimeout)
			return
		}
		rpResp, err = pollRunPod(client, endpointID, apiKey, rpResp.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusGatewayTimeout)
			return
		}
	}

	if rpResp.Status == "FAILED" {
		http.Error(w, "job failed: "+rpResp.Output.Error, http.StatusBadGateway)
		return
	}
	if rpResp.Output.Error != "" {
		http.Error(w, rpResp.Output.Error, http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"reply": rpResp.Output.Output})
}

func pollRunPod(client *http.Client, endpointID, apiKey, id string) (RunPodResponse, error) {
	statusURL := "https://api.runpod.ai/v2/" + endpointID + "/status/" + id
	deadline := time.Now().Add(3 * time.Minute)

	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)

		statusReq, _ := http.NewRequest(http.MethodGet, statusURL, nil)
		statusReq.Header.Set("Authorization", "Bearer "+apiKey)

		sResp, err := client.Do(statusReq)
		if err != nil {
			return RunPodResponse{}, err
		}
		body, _ := io.ReadAll(sResp.Body)
		sResp.Body.Close()

		if sResp.StatusCode != http.StatusOK {
			log.Printf("runpod poll: status=%d body=%s", sResp.StatusCode, body)
			continue
		}

		var rpResp RunPodResponse
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&rpResp); err != nil {
			continue
		}
		if rpResp.Status == "COMPLETED" || rpResp.Status == "FAILED" {
			return rpResp, nil
		}
	}

	return RunPodResponse{}, fmt.Errorf("job timed out waiting for RunPod")
}

func main() {
	godotenv.Load()
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