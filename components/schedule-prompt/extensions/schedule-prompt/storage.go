package schedule_prompt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// jobStore is what the scheduler, the tool and the views use of the storage (upstream: storage.ts CronStorage; the original's tests
// stand in an in-memory object with these methods).
type jobStore interface {
	HasJobWithName(name string) (bool, error)
	AddJob(job *jsobj.Object) error
	RemoveJob(id string) (bool, error)
	UpdateJob(id string, partial *jsobj.Object) (bool, error)
	GetJob(id string) (*jsobj.Object, error)
	GetAllJobs() ([]*jsobj.Object, error)
}

// logf is console.error and console.warn (the original prints on the console; stderr is the extension's log).
var logf = func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }

// fileStorage persists the scheduled prompts to <cwd>/.pi/schedule-prompts.json, re-reading the file on every operation and writing it
// atomically through a temporary file, as the original does. The mutex stands for JavaScript's single thread: one operation at a time.
type fileStorage struct {
	mu        sync.Mutex
	storePath string
	piDir     string
}

func newFileStorage(cwd string) *fileStorage {
	piDir := filepath.Join(cwd, ".pi")
	return &fileStorage{piDir: piDir, storePath: filepath.Join(piDir, "schedule-prompts.json")}
}

// store is the parsed file: the object (its key order and unknown members are kept) and its jobs array.
type store struct {
	root *jsobj.Object
	jobs []any
}

func emptyStore() *store {
	root := jsobj.New()
	jobs := []any{}
	root.Set("jobs", jobs)
	root.Set("version", float64(1))
	return &store{root: root, jobs: jobs}
}

// errBadStore: the file parses as JSON but is not {jobs: [...]} of objects. The original would throw a TypeError from the first use
// of the missing member and never overwrite the file; so does the port, with a message of its own (PORT.md D5).
func errBadStore(path string) error {
	return fmt.Errorf("%s is not a schedule store: expected a JSON object with a \"jobs\" array of objects", path)
}

func (s *fileStorage) load() (*store, error) {
	data, err := os.ReadFile(s.storePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyStore(), nil
		}
		logf("Failed to load scheduled prompts: %v", err)
		return emptyStore(), nil
	}
	v, err := jsobj.Parse(string(data))
	if err != nil {
		logf("Failed to load scheduled prompts: %v", err)
		return emptyStore(), nil
	}
	root, ok := v.(*jsobj.Object)
	if !ok {
		return nil, errBadStore(s.storePath)
	}
	jobs, ok := root.Get("jobs").([]any)
	if !ok {
		return nil, errBadStore(s.storePath)
	}
	for _, j := range jobs {
		if _, ok := j.(*jsobj.Object); !ok {
			return nil, errBadStore(s.storePath)
		}
	}
	return &store{root: root, jobs: jobs}, nil
}

func (s *fileStorage) save(st *store) error {
	st.root.Set("jobs", st.jobs)
	text, err := jsobj.Stringify(st.root)
	if err == nil {
		err = os.MkdirAll(s.piDir, 0o777)
	}
	if err == nil {
		tmp := s.storePath + ".tmp"
		if err = os.WriteFile(tmp, []byte(text), 0o666); err == nil {
			err = os.Rename(tmp, s.storePath)
		}
	}
	if err != nil {
		logf("Failed to save scheduled prompts: %v", err)
	}
	return err
}

func jobObjects(st *store) []*jsobj.Object {
	out := make([]*jsobj.Object, len(st.jobs))
	for i, j := range st.jobs {
		out[i] = j.(*jsobj.Object)
	}
	return out
}

func (s *fileStorage) HasJobWithName(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return false, err
	}
	for _, j := range jobObjects(st) {
		if strictEqualString(j.Get("name"), name) {
			return true, nil
		}
	}
	return false, nil
}

func (s *fileStorage) AddJob(job *jsobj.Object) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	st.jobs = append(st.jobs, job)
	return s.save(st)
}

func (s *fileStorage) RemoveJob(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return false, err
	}
	kept := make([]any, 0, len(st.jobs))
	for _, j := range jobObjects(st) {
		if !strictEqualString(j.Get("id"), id) {
			kept = append(kept, j)
		}
	}
	if len(kept) < len(st.jobs) {
		st.jobs = kept
		return true, s.save(st)
	}
	return false, nil
}

func (s *fileStorage) UpdateJob(id string, partial *jsobj.Object) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return false, err
	}
	for _, j := range jobObjects(st) {
		if strictEqualString(j.Get("id"), id) {
			j.Assign(partial)
			return true, s.save(st)
		}
	}
	return false, nil
}

func (s *fileStorage) GetJob(id string) (*jsobj.Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return nil, err
	}
	for _, j := range jobObjects(st) {
		if strictEqualString(j.Get("id"), id) {
			return j, nil
		}
	}
	return nil, nil
}

func (s *fileStorage) GetAllJobs() ([]*jsobj.Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return nil, err
	}
	return jobObjects(st), nil
}
