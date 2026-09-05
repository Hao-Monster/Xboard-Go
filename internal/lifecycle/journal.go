package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	stateDirectoryMode = 0o700
	stateFileMode      = 0o600
	maxJournalBytes    = 1 << 20
)

var ErrNoState = errors.New("lifecycle state does not exist")

type Journal struct {
	path string
}

type DeploymentJournal struct {
	path string
}

type Lock struct {
	path string
}

func AcquireLock(path string, now time.Time) (*Lock, error) {
	if now.IsZero() {
		return nil, errors.New("lifecycle lock time is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), stateDirectoryMode); err != nil {
		return nil, fmt.Errorf("create lifecycle lock directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), stateDirectoryMode); err != nil {
		return nil, fmt.Errorf("restrict lifecycle lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, stateFileMode)
	if errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("lifecycle operation is locked by %s; inspect the recorded state before removing a stale lock", path)
	}
	if err != nil {
		return nil, fmt.Errorf("create lifecycle lock: %w", err)
	}
	payload := fmt.Sprintf("pid=%d\nstarted_at=%s\n", os.Getpid(), now.UTC().Format(time.RFC3339Nano))
	if _, err := file.WriteString(payload); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write lifecycle lock: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("sync lifecycle lock: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close lifecycle lock: %w", err)
	}
	return &Lock{path: path}, nil
}

func (l *Lock) Release() error {
	if l == nil || l.path == "" {
		return nil
	}
	if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove lifecycle lock: %w", err)
	}
	l.path = ""
	return nil
}

func NewJournal(path string) *Journal {
	return &Journal{path: path}
}

func NewDeploymentJournal(path string) *DeploymentJournal {
	return &DeploymentJournal{path: path}
}

func (journal *DeploymentJournal) Append(state DeploymentState) error {
	if err := validateDeploymentState(state); err != nil {
		return err
	}
	return appendJournalRecord(journal.path, state)
}

func (journal *DeploymentJournal) Load() (DeploymentState, error) {
	var state DeploymentState
	if err := loadJournalRecord(journal.path, &state); err != nil {
		return DeploymentState{}, err
	}
	if err := validateDeploymentState(state); err != nil {
		return DeploymentState{}, fmt.Errorf("validate deployment lifecycle journal: %w", err)
	}
	return state, nil
}

func (j *Journal) Path() string {
	return j.path
}

func (j *Journal) Append(state State) error {
	if err := validateState(state); err != nil {
		return err
	}
	return appendJournalRecord(j.path, state)
}

func appendJournalRecord(path string, record any) error {
	if err := os.MkdirAll(filepath.Dir(path), stateDirectoryMode); err != nil {
		return fmt.Errorf("create lifecycle state directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), stateDirectoryMode); err != nil {
		return fmt.Errorf("restrict lifecycle state directory: %w", err)
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode lifecycle state: %w", err)
	}
	payload = append(payload, '\n')
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, stateFileMode)
	if err != nil {
		return fmt.Errorf("open lifecycle journal: %w", err)
	}
	defer file.Close()
	if err := file.Chmod(stateFileMode); err != nil {
		return fmt.Errorf("restrict lifecycle journal: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect lifecycle journal: %w", err)
	}
	if info.Size()+int64(len(payload)) > maxJournalBytes {
		return fmt.Errorf("lifecycle journal append would exceed %d bytes", maxJournalBytes)
	}
	if _, err := file.Write(payload); err != nil {
		return fmt.Errorf("append lifecycle journal: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync lifecycle journal: %w", err)
	}
	return nil
}

func (j *Journal) Load() (State, error) {
	var state State
	if err := loadJournalRecord(j.path, &state); err != nil {
		return State{}, err
	}
	if err := validateState(state); err != nil {
		return State{}, fmt.Errorf("validate lifecycle journal: %w", err)
	}
	return state, nil
}

func loadJournalRecord(path string, target any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNoState
	}
	if err != nil {
		return fmt.Errorf("read lifecycle journal: %w", err)
	}
	if len(data) > maxJournalBytes {
		return fmt.Errorf("lifecycle journal exceeds %d bytes", maxJournalBytes)
	}
	lastNewline := bytes.LastIndexByte(data, '\n')
	if lastNewline < 0 {
		return errors.New("lifecycle journal has no complete state record")
	}
	lines := bytes.Split(data[:lastNewline], []byte{'\n'})
	for index := len(lines) - 1; index >= 0; index-- {
		if len(bytes.TrimSpace(lines[index])) == 0 {
			continue
		}
		if err := json.Unmarshal(lines[index], target); err != nil {
			return fmt.Errorf("decode lifecycle journal record %d: %w", index+1, err)
		}
		return nil
	}
	return ErrNoState
}
