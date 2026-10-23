// Package control is the supervisor's authenticated loopback API and its state file.
package control

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

	"ssh_proxy/src/config"
	"ssh_proxy/src/platform"
)

type StateFile struct {
	PID     int         `json:"pid"`
	Address string      `json:"address"`
	Token   string      `json:"token"`
	Config  string      `json:"config"`
	Log     string      `json:"log,omitempty"`
	Exit    *ExitRecord `json:"exit,omitempty"`
}

// ExitRecord occupies the same state file after an unsuccessful run.
type ExitRecord struct {
	Time        time.Time `json:"time"`
	Error       string    `json:"error"`
	StartFailed bool      `json:"start_failed,omitempty"`
}

// Phase is the proxy state in status JSON, read by the tray and launcher scripts.
type Phase string

const (
	PhaseStarting     Phase = "starting"
	PhaseConnecting   Phase = "connecting"
	PhaseRunning      Phase = "running"
	PhaseDegraded     Phase = "degraded"
	PhaseReconnecting Phase = "reconnecting"
	PhaseStopping     Phase = "stopping"
	PhaseStopped      Phase = "stopped"
	PhaseUnresponsive Phase = "unresponsive"
)

type Status struct {
	Autostart   bool      `json:"autostart"`
	UncleanExit bool      `json:"-"` // Internal tray classification; last_error is the public diagnostic.
	Phase       Phase     `json:"phase"`
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

// NewState creates the state for a supervisor run with a fresh bearer token.
func NewState(p config.Paths, address string) (StateFile, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return StateFile{}, err
	}
	return StateFile{PID: os.Getpid(), Address: address, Token: hex.EncodeToString(secret), Config: p.Config, Log: p.Log}, nil
}

// WriteState publishes the state file atomically.
func WriteState(p config.Paths, s StateFile) error {
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

// WriteExit requires the installation's instance lock, just like WriteState.
func WriteExit(p config.Paths, err error, startFailed bool) error {
	return WriteState(p, StateFile{PID: os.Getpid(), Config: p.Config, Log: p.Log,
		Exit: &ExitRecord{Time: time.Now(), Error: err.Error(), StartFailed: startFailed}})
}

// ReadState reads and validates the state file.
func ReadState(p config.Paths) (StateFile, error) {
	var s StateFile
	data, err := os.ReadFile(p.State)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(data, &s); err != nil {
		return s, err
	}
	if s.Exit != nil && strings.EqualFold(s.Config, p.Config) && s.Log != "" {
		return s, nil
	}
	host, _, err := net.SplitHostPort(s.Address)
	if err != nil || host != "127.0.0.1" || len(s.Token) != 64 || !strings.EqualFold(s.Config, p.Config) {
		return s, fmt.Errorf("invalid local control state")
	}
	return s, nil
}

// Call sends one authenticated request to the running supervisor.
func Call(p config.Paths, method, path string) (Status, error) {
	var result Status
	s, err := ReadState(p)
	if err != nil {
		return result, err
	}
	if s.Exit != nil {
		return result, fmt.Errorf("proxy is stopped: %s", s.Exit.Error)
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
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
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
func QueryStatus(p config.Paths) (result Status) {
	defer func() {
		enabled, _, err := p.AutostartEntry().Status()
		result.Autostart = enabled
		if err != nil {
			if result.LastError != "" {
				result.LastError += "; "
			}
			result.LastError += "autostart registration: " + err.Error()
		}
	}()
	st, err := Call(p, "GET", "/status")
	if err == nil {
		return st
	}
	st = Status{Phase: PhaseStopped, Config: p.Config, Log: config.LatestLog(p.Logs, p.ID)}
	if state, e := ReadState(p); e == nil {
		if state.Log != "" {
			st.Log = state.Log
		}
		switch {
		case state.Exit != nil:
			st.UncleanExit = true
			st.LastError = fmt.Sprintf("The proxy stopped unexpectedly at %s: %s; see %s", state.Exit.Time.Format(time.RFC3339), state.Exit.Error, st.Log)
			if state.Exit.StartFailed {
				st.LastError = fmt.Sprintf("Last start failed: %s; see %s", state.Exit.Error, st.Log)
			}
		case platform.SupervisorAlive(p.ID, state.PID):
			st.Phase, st.PID, st.LastError = PhaseUnresponsive, state.PID, err.Error()
		default:
			st.UncleanExit = true
			st.LastError = "The proxy stopped unexpectedly; see " + st.Log
		}
	}
	return st
}
