package impact

import (
	"bufio"
	"encoding/json"
	"github.com/wyw14/cry-161/internal/model"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// ReportStore archives validated impact reports. Reports are appended to a
// JSONL file so the audit history survives restarts, mirroring the event
// journal's storage model.
type ReportStore struct {
	mu  sync.Mutex
	dir string
}

func NewReportStore(dir string) *ReportStore {
	if dir == "" {
		dir = "data"
	}
	_ = os.MkdirAll(dir, 0o755)
	return &ReportStore{dir: dir}
}

func (s *ReportStore) path() string { return filepath.Join(s.dir, "impact_reports.jsonl") }

// Save validates and appends a report. An invalid report (for example one that
// does not cite frozen revisions) can never enter the audit record.
func (s *ReportStore) Save(report Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// List returns archived reports, optionally restricted to one recipe, newest
// last (append order).
func (s *ReportStore) List(recipeID model.ID) ([]Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(s.path())
	if os.IsNotExist(err) {
		return []Report{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 8*1024*1024)
	result := make([]Report, 0)
	for scanner.Scan() {
		var report Report
		if json.Unmarshal(scanner.Bytes(), &report) != nil {
			continue
		}
		if recipeID != "" && report.From.RecipeID != recipeID {
			continue
		}
		result = append(result, report)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].GeneratedAt.Before(result[j].GeneratedAt) })
	return result, scanner.Err()
}

// Get fetches one archived report by id.
func (s *ReportStore) Get(id model.ID) (Report, bool, error) {
	reports, err := s.List("")
	if err != nil {
		return Report{}, false, err
	}
	for _, report := range reports {
		if report.ID == id {
			return report, true, nil
		}
	}
	return Report{}, false, nil
}
