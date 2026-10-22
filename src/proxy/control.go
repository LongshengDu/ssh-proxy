package proxy

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type StateFile struct {
	PID     int    `json:"pid"`
	Address string `json:"address"`
	Token   string `json:"token"`
	Config  string `json:"config"`
	Log     string `json:"log,omitempty"`
}
type Status struct {
	Service     bool      `json:"service"`
	Phase       string    `json:"phase"`
	PID         int       `json:"pid,omitempty"`
	SSHPID      int       `json:"ssh_pid,omitempty"`
	Config      string    `json:"config"`
	Started     time.Time `json:"started,omitempty"`
	Endpoint    string    `json:"endpoint,omitempty"`
	Destination string    `json:"destination,omitempty"`
	SOCKS       string    `json:"socks,omitempty"`
	HTTP        string    `json:"http,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	Log         string    `json:"log"`
}

func newState(p Paths, address string) (StateFile, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return StateFile{}, err
	}
	return StateFile{PID: os.Getpid(), Address: address, Token: hex.EncodeToString(secret), Config: p.Config, Log: p.Log}, nil
}
func writeState(p Paths, s StateFile) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.State + ".tmp"
	if err = os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, p.State)
}
func readState(p Paths) (StateFile, error) {
	var s StateFile
	data, err := os.ReadFile(p.State)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(data, &s); err != nil {
		return s, err
	}
	host, _, err := net.SplitHostPort(s.Address)
	if err != nil || host != "127.0.0.1" || len(s.Token) != 64 || !strings.EqualFold(s.Config, p.Config) {
		return s, fmt.Errorf("invalid local control state")
	}
	return s, nil
}
func controlCall(p Paths, method, path string) (Status, error) {
	var result Status
	s, err := readState(p)
	if err != nil {
		return result, err
	}
	req, err := http.NewRequest(method, "http://"+s.Address+path, bytes.NewReader(nil))
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return result, fmt.Errorf("control API: %s", resp.Status)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&result); err != nil {
		return result, err
	}
	if result.PID != s.PID || !strings.EqualFold(result.Config, p.Config) {
		return result, fmt.Errorf("control instance identity mismatch")
	}
	return result, nil
}
func ControlHandler(token string, getStatus func() Status, stop func()) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "Unauthorized", 401)
			return
		}
		if (r.URL.Path != "/status" || r.Method != http.MethodGet) && (r.URL.Path != "/stop" || r.Method != http.MethodPost) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(getStatus())
		if r.URL.Path == "/stop" {
			// Deliver the acknowledgement before shutdown can close this server.
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			go stop()
		}
	})
}

// QueryStatus is also usable without a live configuration file.
func QueryStatus(p Paths) Status {
	st, err := controlCall(p, "GET", "/status")
	if err == nil {
		return st
	}
	st = Status{Phase: "stopped", Config: p.Config, Log: latestLog(p.Logs, p.ID)}
	if state, e := readState(p); e == nil && ProcessAlive(state.PID) {
		st.Phase = "unresponsive"
		st.PID = state.PID
		st.LastError = err.Error()
		if state.Log != "" {
			st.Log = state.Log
		}
	}
	return st
}
