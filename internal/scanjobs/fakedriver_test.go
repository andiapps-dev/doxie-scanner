package scanjobs

import (
	"context"
	"errors"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
)

// fakeSession is an in-memory driver.Session used to drive Manager's
// orchestration logic without a real scanner.
type fakeSession struct {
	pages []driver.Page
	idx   int

	hasNextErr error
	scanErr    error
	scanErrAt  int // 1-based page index at which ScanPage returns scanErr; 0 = never

	closeErr error
	closed   bool
}

func (s *fakeSession) HasNextPage(ctx context.Context) (bool, error) {
	if s.hasNextErr != nil {
		return false, s.hasNextErr
	}
	return s.idx < len(s.pages), nil
}

func (s *fakeSession) ScanPage(ctx context.Context, opts driver.ScanOptions) (driver.Page, error) {
	s.idx++
	if s.scanErr != nil && s.idx == s.scanErrAt {
		return driver.Page{}, s.scanErr
	}
	if s.idx-1 >= len(s.pages) {
		return driver.Page{}, errors.New("fakeSession: ScanPage called with no page scripted")
	}
	return s.pages[s.idx-1], nil
}

func (s *fakeSession) Close() error {
	s.closed = true
	return s.closeErr
}

// fakeDriver is an in-memory driver.Driver used to drive Manager's
// orchestration logic without a real scanner.
type fakeDriver struct {
	info    driver.Info
	session *fakeSession
	openErr error
}

func (d *fakeDriver) Info() driver.Info { return d.info }

func (d *fakeDriver) Detect(ctx context.Context) error { return nil }

func (d *fakeDriver) Open(ctx context.Context) (driver.Session, error) {
	if d.openErr != nil {
		return nil, d.openErr
	}
	return d.session, nil
}
