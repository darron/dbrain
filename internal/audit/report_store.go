package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	reportRetentionDays  = 90
	reportRetentionBytes = int64(256 << 20)
	maxReportLineBytes   = 512 << 10
	maxAlertStateBytes   = 1 << 20
)

var reportFilePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\.jsonl$`)

type reportStoreFS interface {
	AppendReport(string, []byte) error
	ReadReport(string, int64) ([]byte, error)
	ListReports() ([]reportFileInfo, error)
	RemoveReport(string) error
	ReadState(int64) ([]byte, error)
	ReplaceState([]byte) error
}

type reportFileInfo struct {
	Name string
	Size int64
}

type ReportStore struct {
	mu  sync.Mutex
	fs  reportStoreFS
	now func() time.Time
}

func NewReportStore(logDir string) (*ReportStore, error) {
	fs, err := openReportStoreFS(strings.TrimSpace(logDir))
	if err != nil {
		return nil, err
	}
	return &ReportStore{fs: fs, now: time.Now}, nil
}

func (s *ReportStore) Save(report Report) error {
	if s == nil || s.fs == nil {
		return fmt.Errorf("audit report store is unavailable")
	}
	if err := ValidateReport(report); err != nil {
		return fmt.Errorf("validate audit report before persistence: %w", err)
	}
	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode audit report: %w", err)
	}
	if len(data)+1 > maxReportLineBytes {
		return fmt.Errorf("audit report exceeds private line limit")
	}
	data = append(data, '\n')
	name := report.CompletedAt.UTC().Format("2006-01-02") + ".jsonl"
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fs.AppendReport(name, data); err != nil {
		return fmt.Errorf("append private audit report: %w", err)
	}
	if err := s.enforceRetentionLocked(); err != nil {
		return fmt.Errorf("enforce audit report retention: %w", err)
	}
	return nil
}

func (s *ReportStore) Latest(profile Profile) (*Report, error) {
	history, err := s.History(profile, 1)
	if err != nil || len(history) == 0 {
		return nil, err
	}
	report := history[0]
	return &report, nil
}

func (s *ReportStore) History(profile Profile, limit int) ([]Report, error) {
	if s == nil || s.fs == nil {
		return nil, fmt.Errorf("audit report store is unavailable")
	}
	if !profile.Valid() {
		return nil, fmt.Errorf("invalid audit profile %q", profile)
	}
	if limit <= 0 {
		return []Report{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.fs.ListReports()
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name > files[j].Name })
	out := make([]Report, 0, limit)
	for _, file := range files {
		if !reportFilePattern.MatchString(file.Name) {
			continue
		}
		data, readErr := s.fs.ReadReport(file.Name, file.Size)
		if readErr != nil {
			return nil, fmt.Errorf("read private audit report: %w", readErr)
		}
		lines := bytes.Split(data, []byte{'\n'})
		for index := len(lines) - 1; index >= 0 && len(out) < limit; index-- {
			line := bytes.TrimSpace(lines[index])
			if len(line) == 0 || len(line) > maxReportLineBytes {
				continue
			}
			var report Report
			if err := decodeSingleJSON(line, &report); err != nil || report.Profile != profile || ValidateReport(report) != nil {
				continue
			}
			out = append(out, report)
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (s *ReportStore) LoadAlertState() (AlertState, error) {
	if s == nil || s.fs == nil {
		return AlertState{}, fmt.Errorf("audit report store is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.fs.ReadState(maxAlertStateBytes)
	if errors.Is(err, os.ErrNotExist) {
		return emptyAlertState(), nil
	}
	if err != nil {
		return AlertState{}, fmt.Errorf("read private alert state: %w", err)
	}
	var state AlertState
	if err := decodeSingleJSON(data, &state); err != nil {
		return AlertState{}, fmt.Errorf("decode private alert state: %w", err)
	}
	if err := ValidateAlertState(state); err != nil {
		return AlertState{}, err
	}
	return state, nil
}

func decodeSingleJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func (s *ReportStore) SaveAlertState(state AlertState) error {
	if s == nil || s.fs == nil {
		return fmt.Errorf("audit report store is unavailable")
	}
	if err := ValidateAlertState(state); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode private alert state: %w", err)
	}
	if len(data) > maxAlertStateBytes {
		return fmt.Errorf("private alert state exceeds byte limit")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fs.ReplaceState(append(data, '\n'))
}

func (s *ReportStore) enforceRetentionLocked() error {
	files, err := s.fs.ListReports()
	if err != nil {
		return err
	}
	type datedFile struct {
		reportFileInfo
		day time.Time
	}
	valid := make([]datedFile, 0, len(files))
	var total int64
	now := s.now().UTC()
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -reportRetentionDays)
	for _, file := range files {
		if !reportFilePattern.MatchString(file.Name) || file.Size < 0 {
			continue
		}
		day, parseErr := time.Parse("2006-01-02", strings.TrimSuffix(file.Name, ".jsonl"))
		if parseErr != nil {
			continue
		}
		if day.Before(cutoff) {
			if err := s.fs.RemoveReport(file.Name); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		valid = append(valid, datedFile{reportFileInfo: file, day: day})
		total += file.Size
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].day.Before(valid[j].day) })
	for _, file := range valid {
		if total <= reportRetentionBytes {
			break
		}
		if err := s.fs.RemoveReport(file.Name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		total -= file.Size
	}
	return nil
}

func readBoundedRegular(file *os.File, limit int64) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("private audit file is not a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(bufio.NewReader(file), limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("private audit file exceeds byte limit")
	}
	return data, nil
}

func appendIsolatedReportRecord(file *os.File, data []byte) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	payload := data
	if info.Size() > 0 {
		var tail [1]byte
		if _, err := file.ReadAt(tail[:], info.Size()-1); err != nil {
			return fmt.Errorf("inspect private audit report tail: %w", err)
		}
		if tail[0] != '\n' {
			payload = make([]byte, 0, len(data)+1)
			payload = append(payload, '\n')
			payload = append(payload, data...)
		}
	}
	if _, err := file.Write(payload); err != nil {
		return err
	}
	return file.Sync()
}
