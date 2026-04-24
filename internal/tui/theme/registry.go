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

func Register(name string, theme Theme) {
	mu.Lock()
	defer mu.Unlock()
	themes[name] = theme
	if defName == "" {
		defName = name
	}
}

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

func Default() Theme {
	mu.RLock()
	defer mu.RUnlock()
	if defName == "" || themes[defName] == nil {
		return nil
	}
	return themes[defName]
}
