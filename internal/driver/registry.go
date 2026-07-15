package driver

import (
	"fmt"
	"sort"
	"sync"
)

var (
	registryMu sync.Mutex
	registry   = map[string]func() Driver{}
)

// Register makes a driver constructor available under name. It's
// intended to be called from an init() function in a concrete driver
// package (e.g. internal/doxiedx400), analogous to database/sql drivers
// registering themselves via blank import.
func Register(name string, ctor func() Driver) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if ctor == nil {
		panic("driver: Register called with nil constructor for " + name)
	}
	if _, dup := registry[name]; dup {
		panic("driver: Register called twice for driver " + name)
	}
	registry[name] = ctor
}

// Get constructs the driver registered under name.
func Get(name string) (Driver, error) {
	registryMu.Lock()
	ctor, ok := registry[name]
	registryMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("driver: unknown driver %q (available: %v)", name, Names())
	}
	return ctor(), nil
}

// Names returns the sorted list of registered driver names.
func Names() []string {
	registryMu.Lock()
	defer registryMu.Unlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
