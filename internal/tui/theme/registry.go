package theme

import (
	"fmt"
	"sync"
)

var (
	mu      sync.RWMutex
	themes  = make(map[string]Theme)
	defName string
)

// Register adds a named theme to the global theme registry.
func Register(name string, theme Theme) {
	mu.Lock()
	defer mu.Unlock()
	themes[name] = theme
	if defName == "" {
		defName = name
	}
}

// Get returns a registered theme by name, falling back to the steiner theme.
func Get(name string) (Theme, error) {
	mu.RLock()
	defer mu.RUnlock()
	theme, ok := themes[name]
	if !ok {
		// Fall back to steiner theme
		theme = themes["steiner"]
		if theme == nil {
			return nil, fmt.Errorf("theme not found: %s", name)
		}
		return theme, nil
	}
	return theme, nil
}

// Default returns the first registered theme.
func Default() Theme {
	mu.RLock()
	defer mu.RUnlock()
	if defName == "" || themes[defName] == nil {
		return nil
	}
	return themes[defName]
}
