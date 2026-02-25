package heartbeat

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type HeartbeatService struct {
	workspace   string
	onHeartbeat func(string) (string, error)
	interval    time.Duration
	enabled     bool
	running     bool
	mu          sync.RWMutex
	stopChan    chan struct{}
}

func NewHeartbeatService(workspace string, onHeartbeat func(string) (string, error), intervalS int, enabled bool) *HeartbeatService {
	return &HeartbeatService{
		workspace:   workspace,
		onHeartbeat: onHeartbeat,
		interval:    time.Duration(intervalS) * time.Second,
		enabled:     enabled,
		stopChan:    make(chan struct{}),
	}
}

func (hs *HeartbeatService) Start() error {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	if hs.running {
		return nil
	}

	if !hs.enabled {
		return fmt.Errorf("heartbeat service is disabled")
	}
	if hs.interval <= 0 {
		return fmt.Errorf("heartbeat interval must be greater than 0")
	}

	// Recreate stop channel on each start so Stop->Start works.
	hs.stopChan = make(chan struct{})
	hs.running = true
	go hs.runLoop(hs.stopChan)

	return nil
}

func (hs *HeartbeatService) Stop() {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	if !hs.running {
		return
	}

	hs.running = false
	if hs.stopChan != nil {
		close(hs.stopChan)
	}
}

func (hs *HeartbeatService) isRunning() bool {
	return hs.running
}

func (hs *HeartbeatService) runLoop(stopChan <-chan struct{}) {
	ticker := time.NewTicker(hs.interval)
	defer ticker.Stop()

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			hs.checkHeartbeat()
		}
	}
}

func (hs *HeartbeatService) checkHeartbeat() {
	hs.mu.RLock()
	if !hs.enabled || !hs.isRunning() {
		hs.mu.RUnlock()
		return
	}
	hs.mu.RUnlock()

	prompt := hs.buildPrompt()

	if hs.onHeartbeat != nil {
		_, err := hs.onHeartbeat(prompt)
		if err != nil {
			hs.log(fmt.Sprintf("Heartbeat error: %v", err))
		}
	}
}

func (hs *HeartbeatService) buildPrompt() string {
	notesFile := filepath.Join(hs.workspace, "HEARTBEAT.md")

	var notes string
	if data, err := os.ReadFile(notesFile); err == nil {
		notes = string(data)
	}

	now := time.Now().Format("2006-01-02 15:04")

	prompt := fmt.Sprintf(`# Heartbeat Check

Current time: %s

This is a background heartbeat run.

- If there is nothing actionable to tell the user, respond with exactly: HEARTBEAT_OK
- If there is something important, write the message you want to send to the user.
- Keep it short and concrete.
- Do NOT call the message tool; your assistant response will be delivered automatically.

%s
`, now, notes)

	return prompt
}

func (hs *HeartbeatService) log(message string) {
	logFile := filepath.Join(hs.workspace, "memory", "heartbeat.log")
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	f.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, message))
}
