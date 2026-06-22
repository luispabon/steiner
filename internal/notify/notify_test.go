package notify

import (
	"context"
	"testing"
	"time"
)

// fakeDriver records all calls for testing.
type fakeDriver struct {
	notifyCalls []struct {
		n Notification
		d time.Duration
		e error
	}
	avail    bool
	availMsg string
}

func (d *fakeDriver) notify(ctx context.Context, n Notification, dur time.Duration) error {
	d.notifyCalls = append(d.notifyCalls, struct {
		n Notification
		d time.Duration
		e error
	}{n, dur, nil})
	return nil
}

func (d *fakeDriver) available() (bool, string) {
	return d.avail, d.availMsg
}

func TestDisabledService(t *testing.T) {
	opts := Options{
		Enabled:  false,
		Duration: 5 * time.Second,
		AppName:  "steiner",
	}
	svc := New(opts)

	ctx := context.Background()
	n := Notification{
		Project: "test",
		Branch:  "main",
		Reason:  "Build completed",
	}

	err := svc.Notify(ctx, n)
	if err != nil {
		t.Errorf("Notify returned error: %v, expected nil", err)
	}

	avail, msg := svc.Availability()
	if avail {
		t.Error("Availability should return false for disabled service")
	}
	if msg != "desktop notifications are disabled" {
		t.Errorf("Availability message = %q, expected %q", msg, "desktop notifications are disabled")
	}
}

func TestPassthroughDriver(t *testing.T) {
	fake := &fakeDriver{
		avail:    true,
		availMsg: "available",
	}
	svc := &Service{
		opts: Options{
			Enabled:  true,
			Duration: 3 * time.Second,
			AppName:  "steiner",
		},
		drv: fake,
	}

	ctx := context.Background()
	n := Notification{
		Project: "myproject",
		Branch:  "feature-xyz",
		Reason:  "Test passed",
	}

	err := svc.Notify(ctx, n)
	if err != nil {
		t.Errorf("Notify returned error: %v, expected nil", err)
	}

	if len(fake.notifyCalls) != 1 {
		t.Fatalf("Expected 1 notify call, got %d", len(fake.notifyCalls))
	}

	call := fake.notifyCalls[0]
	if call.n != n {
		t.Errorf("Passed notification = %+v, expected %+v", call.n, n)
	}
	if call.d != 3*time.Second {
		t.Errorf("Passed duration = %v, expected %v", call.d, 3*time.Second)
	}

	avail, msg := svc.Availability()
	if !avail {
		t.Error("Availability should return true")
	}
	if msg != "available" {
		t.Errorf("Availability message = %q, expected %q", msg, "available")
	}
}

func TestNotificationTitle(t *testing.T) {
	tests := []struct {
		name     string
		input    Notification
		expected string
	}{
		{
			name: "simple project",
			input: Notification{
				Project: "myapp",
				Branch:  "main",
				Reason:  "Build done",
			},
			expected: "steiner — myapp",
		},
		{
			name: "project with spaces",
			input: Notification{
				Project: "my awesome app",
				Branch:  "main",
				Reason:  "Build done",
			},
			expected: "steiner — my awesome app",
		},
		{
			name: "empty project",
			input: Notification{
				Project: "",
				Branch:  "main",
				Reason:  "Build done",
			},
			expected: "steiner — ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := notificationTitle(tt.input)
			if got != tt.expected {
				t.Errorf("notificationTitle = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestNotificationBody(t *testing.T) {
	tests := []struct {
		name     string
		input    Notification
		expected string
	}{
		{
			name: "simple body",
			input: Notification{
				Project: "myapp",
				Branch:  "main",
				Reason:  "Build passed",
			},
			expected: "Build passed\nmain",
		},
		{
			name: "multiline reason",
			input: Notification{
				Project: "myapp",
				Branch:  "dev/feature",
				Reason:  "All tests passed",
			},
			expected: "All tests passed\ndev/feature",
		},
		{
			name: "empty fields",
			input: Notification{
				Project: "myapp",
				Branch:  "",
				Reason:  "",
			},
			expected: "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := notificationBody(tt.input)
			if got != tt.expected {
				t.Errorf("notificationBody = %q, expected %q", got, tt.expected)
			}
		})
	}
}
