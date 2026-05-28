package vmstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	DirName            = ".docker-vm-runner"
	FileName           = "state.json"
	LifecycleInstalled = "installed"
)

type State struct {
	Version   int       `json:"version"`
	Lifecycle string    `json:"lifecycle"`
	UpdatedAt time.Time `json:"updated_at"`
}

func Path(vmDir string) string {
	return filepath.Join(vmDir, DirName, FileName)
}

func Read(vmDir string) (State, error) {
	content, err := os.ReadFile(Path(vmDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("read VM state: %w", err)
	}
	var state State
	if err := json.Unmarshal(content, &state); err != nil {
		return State{}, fmt.Errorf("parse VM state: %w", err)
	}
	return state, nil
}

func MarkInstalled(vmDir string, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return Write(vmDir, State{Version: 1, Lifecycle: LifecycleInstalled, UpdatedAt: now.UTC()})
}

func Write(vmDir string, state State) error {
	path := Path(vmDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create VM state directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), FileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary VM state: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(state); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("encode VM state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary VM state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace VM state: %w", err)
	}
	return nil
}

func IsInstalled(state State) bool {
	return state.Lifecycle == LifecycleInstalled
}
