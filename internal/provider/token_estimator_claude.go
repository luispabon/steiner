package provider

import (
	"fmt"
	"sync"

	"github.com/luispabon/ctok-go"
)

// ctokActiveModelIDs lists the exact, whitespace-trimmed model IDs that count
// with ctok. It is empty in production, so no model uses ctok yet.
var ctokActiveModelIDs = map[string]struct{}{}

// claudeCounter is the shared ctok text counter. Its zero value loads nothing;
// the vocabulary loads on the first Count call.
var claudeCounter = &claudeTextCounter{}

// claudeTextCounter counts content tokens with ctok CountContent, which excludes
// the message framing that ctok Count adds.
type claudeTextCounter struct {
	// loader overrides the ctok V48 load. Nil loads ctok.New(ctok.V48).
	loader  func() (*ctok.Counter, error)
	once    sync.Once
	counter *ctok.Counter
	err     error
}

// load loads the ctok V48 counter, or the loader override when set.
func (c *claudeTextCounter) load() (*ctok.Counter, error) {
	if c.loader != nil {
		return c.loader()
	}
	return ctok.New(ctok.V48)
}

// Count returns the ctok content token count of text. A failed load is returned
// on every call; Count never falls back to another tokenizer.
func (c *claudeTextCounter) Count(text string) (int, error) {
	c.once.Do(func() {
		counter, err := c.load()
		if err != nil {
			c.err = fmt.Errorf("load ctok counter: %w", err)
			return
		}
		c.counter = counter
	})
	if c.err != nil {
		return 0, c.err
	}
	return c.counter.CountContent(text), nil
}
