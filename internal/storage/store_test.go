package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStore_CheckWritable_CreatesAndVerifies(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does", "not", "exist", "yet")
	s := New(root)
	if err := s.CheckWritable(); err != nil {
		t.Fatalf("CheckWritable: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("expected root to be created: %v", err)
	}
}

func TestStore_CheckWritable_FailsOnUnwritablePath(t *testing.T) {
	// A root path underneath a regular file can never be created.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(filepath.Join(blocker, "data"))
	if err := s.CheckWritable(); err == nil {
		t.Fatal("expected an error")
	}
}

func TestStore_CheckWritable_FailsWhenDirNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission-based test is unreliable")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(root, 0o755) // allow t.TempDir()'s own cleanup to succeed

	s := New(root)
	if err := s.CheckWritable(); err == nil {
		t.Fatal("expected an error writing into a read-only directory")
	}
}

func TestStore_SaveMeta_WriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission-based test is unreliable")
	}
	s := New(t.TempDir())
	if err := s.CreateJob(JobMeta{ID: "job1"}); err != nil {
		t.Fatal(err)
	}
	// Overwriting an existing file's *content* only needs write
	// permission on the file itself, not its containing directory — so
	// make meta.json itself read-only to block the rewrite.
	if err := os.Chmod(s.metaPath("job1"), 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(s.metaPath("job1"), 0o644)

	if err := s.SaveMeta(JobMeta{ID: "job1", Name: "changed"}); err == nil {
		t.Fatal("expected a write error")
	}
}

func TestStore_DeleteJob_Error(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission-based test is unreliable")
	}
	s := New(t.TempDir())
	if err := s.CreateJob(JobMeta{ID: "job1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePageFile("job1", "page-001.png", []byte("x")); err != nil {
		t.Fatal(err)
	}
	// Removing read permission on the job dir itself prevents RemoveAll
	// from listing/removing its contents.
	if err := os.Chmod(s.jobDir("job1"), 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(s.jobDir("job1"), 0o755)

	if err := s.DeleteJob("job1"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestStore_CreateAndLoadMeta(t *testing.T) {
	s := New(t.TempDir())
	meta := JobMeta{
		ID:     "20260714-153045-abcd",
		Name:   "Scan 2026-07-14 15:30",
		Driver: "doxie-dx400",
		Status: StatusRunning,
	}
	if err := s.CreateJob(meta); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	got, err := s.LoadMeta(meta.ID)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if got.ID != meta.ID || got.Name != meta.Name || got.Status != meta.Status {
		t.Errorf("got %+v, want %+v", got, meta)
	}
}

func TestStore_LoadMeta_NotFound(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.LoadMeta("does-not-exist"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestStore_LoadMeta_CorruptJSON(t *testing.T) {
	s := New(t.TempDir())
	meta := JobMeta{ID: "job1"}
	if err := s.CreateJob(meta); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.metaPath("job1"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadMeta("job1"); err == nil {
		t.Fatal("expected an error for corrupt JSON")
	}
}

func TestStore_SaveMeta_Overwrites(t *testing.T) {
	s := New(t.TempDir())
	meta := JobMeta{ID: "job1", Name: "original"}
	if err := s.CreateJob(meta); err != nil {
		t.Fatal(err)
	}
	meta.Name = "renamed"
	if err := s.SaveMeta(meta); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	got, err := s.LoadMeta("job1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "renamed" {
		t.Errorf("got %q, want %q", got.Name, "renamed")
	}
}

func TestStore_ListJobs_SortedNewestFirst(t *testing.T) {
	s := New(t.TempDir())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := []string{"job-a", "job-b", "job-c"}
	times := []time.Time{base, base.Add(2 * time.Hour), base.Add(1 * time.Hour)}
	for i, id := range ids {
		if err := s.CreateJob(JobMeta{ID: id, CreatedAt: times[i]}); err != nil {
			t.Fatal(err)
		}
	}

	jobs, err := s.ListJobs()
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("got %d jobs, want 3", len(jobs))
	}
	// job-b (base+2h) should be newest, then job-c (base+1h), then job-a (base).
	want := []string{"job-b", "job-c", "job-a"}
	for i, id := range want {
		if jobs[i].ID != id {
			t.Errorf("position %d: got %q, want %q", i, jobs[i].ID, id)
		}
	}
}

func TestStore_ListJobs_EmptyWhenNoJobsDir(t *testing.T) {
	s := New(t.TempDir())
	jobs, err := s.ListJobs()
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("expected 0 jobs, got %d", len(jobs))
	}
}

func TestStore_ListJobs_SkipsCorruptJobDirs(t *testing.T) {
	s := New(t.TempDir())
	if err := s.CreateJob(JobMeta{ID: "good-job"}); err != nil {
		t.Fatal(err)
	}
	// A directory with no meta.json at all.
	if err := os.MkdirAll(filepath.Join(s.jobsDir(), "broken-job"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A non-directory entry directly inside jobs/.
	if err := os.WriteFile(filepath.Join(s.jobsDir(), "stray-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	jobs, err := s.ListJobs()
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "good-job" {
		t.Errorf("expected only good-job, got %+v", jobs)
	}
}

func TestStore_ListJobs_Error(t *testing.T) {
	// Root itself is a file, not a directory: os.ReadDir on jobs/ fails
	// with something other than "not exist".
	root := filepath.Join(t.TempDir(), "root-as-file")
	if err := os.WriteFile(root, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(root)
	if _, err := s.ListJobs(); err == nil {
		t.Fatal("expected an error")
	}
}

func TestStore_PageFileRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	if err := s.CreateJob(JobMeta{ID: "job1"}); err != nil {
		t.Fatal(err)
	}
	data := []byte("fake png bytes")
	if err := s.SavePageFile("job1", "page-001.png", data); err != nil {
		t.Fatalf("SavePageFile: %v", err)
	}
	got, err := s.LoadPageFile("job1", "page-001.png")
	if err != nil {
		t.Fatalf("LoadPageFile: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}

	if err := s.DeletePageFile("job1", "page-001.png"); err != nil {
		t.Fatalf("DeletePageFile: %v", err)
	}
	if _, err := s.LoadPageFile("job1", "page-001.png"); err == nil {
		t.Fatal("expected an error after deletion")
	}
}

func TestStore_PageFilePath(t *testing.T) {
	s := New(t.TempDir())
	if err := s.CreateJob(JobMeta{ID: "job1"}); err != nil {
		t.Fatal(err)
	}
	data := []byte("fake png bytes")
	if err := s.SavePageFile("job1", "page-001.png", data); err != nil {
		t.Fatalf("SavePageFile: %v", err)
	}

	path := s.PageFilePath("job1", "page-001.png")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected PageFilePath to return a real, readable path: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

func TestStore_SavePageFile_Error(t *testing.T) {
	s := New(t.TempDir())
	// Job directory was never created, so pages/ doesn't exist.
	if err := s.SavePageFile("no-such-job", "page-001.png", []byte("x")); err == nil {
		t.Fatal("expected an error")
	}
}

func TestStore_DeletePageFile_Error(t *testing.T) {
	s := New(t.TempDir())
	if err := s.DeletePageFile("no-such-job", "page-001.png"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestStore_RenamePageFile(t *testing.T) {
	s := New(t.TempDir())
	if err := s.CreateJob(JobMeta{ID: "job1"}); err != nil {
		t.Fatal(err)
	}
	data := []byte("fake png bytes")
	if err := s.SavePageFile("job1", "page-002.png", data); err != nil {
		t.Fatalf("SavePageFile: %v", err)
	}

	if err := s.RenamePageFile("job1", "page-002.png", "page-001.png"); err != nil {
		t.Fatalf("RenamePageFile: %v", err)
	}

	got, err := s.LoadPageFile("job1", "page-001.png")
	if err != nil {
		t.Fatalf("LoadPageFile(renamed): %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
	if _, err := s.LoadPageFile("job1", "page-002.png"); err == nil {
		t.Error("expected the old filename to no longer exist")
	}
}

func TestStore_RenamePageFile_Error(t *testing.T) {
	s := New(t.TempDir())
	if err := s.CreateJob(JobMeta{ID: "job1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RenamePageFile("job1", "does-not-exist.png", "page-001.png"); err == nil {
		t.Fatal("expected an error renaming a nonexistent source file")
	}
}

func TestStore_DeleteJob(t *testing.T) {
	s := New(t.TempDir())
	if err := s.CreateJob(JobMeta{ID: "job1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteJob("job1"); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}
	if _, err := s.LoadMeta("job1"); err == nil {
		t.Fatal("expected job to be gone")
	}
}

func TestStore_CreateJob_Error(t *testing.T) {
	// Root itself is a file: MkdirAll for the pages dir must fail.
	root := filepath.Join(t.TempDir(), "root-as-file")
	if err := os.WriteFile(root, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(root)
	if err := s.CreateJob(JobMeta{ID: "job1"}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestStore_Root(t *testing.T) {
	s := New("/some/path")
	if s.Root() != "/some/path" {
		t.Errorf("got %q", s.Root())
	}
}
