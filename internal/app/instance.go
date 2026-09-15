package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const runningFile = "running.json"

type runningInfo struct {
	BaseURL string `json:"base_url"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
}

func writeRunning(dataDir string, info runningInfo) error {
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, runningFile), b, 0o600)
}

func removeRunning(dataDir string) { _ = os.Remove(filepath.Join(dataDir, runningFile)) }

// existingInstance returns the status page URL of a LANdlord that is already running.
func existingInstance(dataDir string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(dataDir, runningFile))
	if err != nil {
		return "", false
	}
	var info runningInfo
	if json.Unmarshal(b, &info) != nil || info.BaseURL == "" || info.PID == os.Getpid() || !processAlive(info.PID) {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.BaseURL+"/healthz", nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("X-Landlord-Token", info.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	return info.BaseURL + "/?t=" + info.Token, true
}
