package agent_memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type HTTPReadinessProbe struct {
	URL, ServiceToken string
	Client            *http.Client
}

func (p *HTTPReadinessProbe) Probe(input ReadinessProbeRequest) error {
	if p == nil || p.URL == "" || p.ServiceToken == "" || input.RobotToken == "" || input.AttemptID == "" || input.BankID == "" || !knownCapability(input.Capability) {
		return errors.New("readiness unavailable")
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	body, _ := json.Marshal(map[string]interface{}{"attempt_id": input.AttemptID, "attempt_version": input.AttemptVersion, "bank_id": input.BankID, "capability": input.Capability})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, strings.TrimRight(p.URL, "/")+"/internal/readiness", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+input.RobotToken)
	req.Header.Set("X-Memory-Readiness-Token", p.ServiceToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("readiness failed")
	}
	return nil
}
