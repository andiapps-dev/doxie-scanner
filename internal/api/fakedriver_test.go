package api

import (
	"context"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
)

// fakeSession is a minimal scriptable driver.Session for handler tests
// that need a running scan (StartScan drives it through scanjobs.Manager).
type fakeSession struct {
	pages      []driver.Page
	idx        int
	hasNextErr error
	scanErr    error
	closeErr   error
	closed     bool
	// block, if non-nil, is read from (and so blocks) at the top of
	// HasNextPage — used to hold a scan "running" indefinitely so tests
	// can deterministically observe an in-progress job without a timing
	// race against the goroutine finishing on its own.
	block chan struct{}
}

func (s *fakeSession) HasNextPage(ctx context.Context) (bool, error) {
	if s.block != nil {
		<-s.block
	}
	if s.hasNextErr != nil {
		return false, s.hasNextErr
	}
	return s.idx < len(s.pages), nil
}

func (s *fakeSession) ScanPage(ctx context.Context, opts driver.ScanOptions) (driver.Page, error) {
	if s.scanErr != nil {
		return driver.Page{}, s.scanErr
	}
	p := s.pages[s.idx]
	s.idx++
	return p, nil
}

func (s *fakeSession) Close() error {
	s.closed = true
	return s.closeErr
}

// fakeDriver is a scriptable driver.Driver for handler tests.
type fakeDriver struct {
	info      driver.Info
	session   *fakeSession
	detectErr error
	openErr   error
}

func (d *fakeDriver) Info() driver.Info { return d.info }

func (d *fakeDriver) Detect(ctx context.Context) error { return d.detectErr }

func (d *fakeDriver) Open(ctx context.Context) (driver.Session, error) {
	if d.openErr != nil {
		return nil, d.openErr
	}
	return d.session, nil
}
