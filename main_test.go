package main

import (
	"context"
	"image"
	"testing"
	"time"

	"github.com/andiapps-dev/doxie-scanner/internal/driver"
	"github.com/andiapps-dev/doxie-scanner/internal/scanjobs"
	"github.com/andiapps-dev/doxie-scanner/internal/storage"
)

type idleSession struct{ block chan struct{} }

func (s *idleSession) HasNextPage(context.Context) (bool, error) { <-s.block; return false, nil }
func (s *idleSession) ScanPage(context.Context, driver.ScanOptions) (driver.Page, error) {
	return driver.Page{Front: image.NewNRGBA(image.Rect(0, 0, 1, 1))}, nil
}
func (s *idleSession) Close() error { return nil }

type idleDriver struct{ sess *idleSession }

func (d *idleDriver) Info() driver.Info                            { return driver.Info{Name: "fake"} }
func (d *idleDriver) Detect(context.Context) error                 { return nil }
func (d *idleDriver) Open(context.Context) (driver.Session, error) { return d.sess, nil }

func TestButtonScan_StartsOneScanAndIgnoresAPressDuringIt(t *testing.T) {
	sess := &idleSession{block: make(chan struct{})}
	mgr := scanjobs.NewManager(&idleDriver{sess}, storage.New(t.TempDir()))
	press := buttonScan(mgr, true)

	press()
	cur := mgr.CurrentJob()
	if cur == nil || cur.Status != storage.StatusRunning {
		t.Fatalf("a press should start a scan, got %+v", cur)
	}
	first := cur.ID
	press() // already running: ignored, not queued
	if got := mgr.CurrentJob().ID; got != first {
		t.Errorf("second press started another scan (%s)", got)
	}
	close(sess.block)
	for deadline := time.Now().Add(2 * time.Second); mgr.CurrentJob().Status == storage.StatusRunning; {
		if time.Now().After(deadline) {
			t.Fatal("scan never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestButtonScan_ReportsAStartThatFails(t *testing.T) {
	// A data directory that can't be written makes the job unstartable; the
	// press must be logged and survived, not crash the watcher.
	mgr := scanjobs.NewManager(&idleDriver{&idleSession{block: make(chan struct{})}}, storage.New("/proc/nonexistent/x"))
	buttonScan(mgr, false)()
}
