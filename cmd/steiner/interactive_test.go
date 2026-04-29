package main

import (
	"context"
	"testing"
)

func TestActiveRunControllerInterruptCancelsCurrentRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	controller := &activeRunController{}
	controller.Set(cancel)

	controller.Interrupt()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("expected interrupt to cancel the active run")
	}
}
