package notify

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

type stateFS interface {
	ReadState(int64) ([]byte, error)
	ReplaceState([]byte) error
}

type stateStoreFS interface {
	stateFS
	ValidateStateLeaf() error
}

type Store struct {
	mu sync.Mutex
	fs stateFS
}

type Reader struct {
	mu sync.Mutex
	fs stateFS
}

var syncNotificationStateFile = func(file *os.File) error { return file.Sync() }

var syncNotificationStateDirectory = func(sync func() error) error { return sync() }

func OpenStore(logDir string) (*Store, error) {
	fs, err := openStateStoreFS(strings.TrimSpace(logDir))
	if err != nil {
		return nil, err
	}
	if err := fs.ValidateStateLeaf(); err != nil {
		return nil, fmt.Errorf("validate private notification state: %w", err)
	}
	return &Store{fs: fs}, nil
}

func OpenReader(logDir string) (*Reader, error) {
	fs, err := openStateReaderFS(strings.TrimSpace(logDir))
	if err != nil {
		return nil, err
	}
	return &Reader{fs: fs}, nil
}

func (s *Store) Load() (State, error) {
	if s == nil || s.fs == nil {
		return State{}, fmt.Errorf("notification state store is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return loadState(s.fs)
}

func (r *Reader) Load() (State, error) {
	if r == nil || r.fs == nil {
		return State{}, fmt.Errorf("notification state reader is unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return loadState(r.fs)
}

func loadState(fs stateFS) (State, error) {
	data, err := fs.ReadState(maxStateBytes)
	if errors.Is(err, os.ErrNotExist) {
		return EmptyState(), nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read private notification state: %w", err)
	}
	state, err := decodeState(data)
	if err != nil {
		return State{}, fmt.Errorf("decode private notification state: %w", err)
	}
	return state, nil
}

func (s *Store) Replace(state State) error {
	if s == nil || s.fs == nil {
		return fmt.Errorf("notification state store is unavailable")
	}
	data, err := encodeState(state)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fs.ReplaceState(data); err != nil {
		return fmt.Errorf("replace private notification state: %w", err)
	}
	return nil
}

func encodeState(state State) ([]byte, error) {
	if err := ValidateState(state); err != nil {
		return nil, fmt.Errorf("validate notification state before persistence: %w", err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("encode notification state: %w", err)
	}
	if len(data) > maxStateBytes {
		return nil, fmt.Errorf("notification state exceeds byte limit")
	}
	return data, nil
}

func decodeState(data []byte) (State, error) {
	if len(data) > maxStateBytes {
		return State{}, fmt.Errorf("notification state exceeds byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return State{}, fmt.Errorf("trailing JSON value")
		}
		return State{}, err
	}
	if state.Schema == StateSchemaV1 {
		if state.LastDeliveries != nil {
			return State{}, fmt.Errorf("invalid v1 notification delivery summaries")
		}
		if err := validateDeliverySummary(state.LastDelivery); err != nil {
			return State{}, err
		}
		state.LastDeliveries = map[string]DeliverySummary{}
		if state.LastDelivery != (DeliverySummary{}) {
			state.LastDeliveries[state.LastDelivery.Provider] = state.LastDelivery
		}
		state.LastDelivery = DeliverySummary{}
		state.Schema = StateSchemaV2
	}
	if err := ValidateState(state); err != nil {
		return State{}, err
	}
	return state, nil
}

func readBoundedState(file *os.File, limit int64) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("private notification state is not a regular file")
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("private notification state exceeds byte limit")
	}
	data, err := io.ReadAll(io.LimitReader(bufio.NewReader(file), limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("private notification state exceeds byte limit")
	}
	return data, nil
}
