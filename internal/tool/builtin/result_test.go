package builtin

import "testing"

func TestWasMutated(t *testing.T) {
	tests := []struct {
		name     string
		result   *MutateResult
		expected bool
	}{
		{
			name:     "nil receiver",
			result:   nil,
			expected: false,
		},
		{
			name: "clean success",
			result: &MutateResult{
				OperationsApplied: 1,
				OperationsFailed:  0,
			},
			expected: true,
		},
		{
			name: "clean failure with no operations applied",
			result: &MutateResult{
				OperationsApplied: 0,
				OperationsFailed:  1,
				Paths:             []string{},
			},
			expected: false,
		},
		{
			name: "rollback failure with dirty paths",
			result: &MutateResult{
				OperationsApplied: 0,
				OperationsFailed:  1,
				Paths:             []string{"a.go"},
				Modified:          []string{"a.go"},
			},
			expected: true,
		},
		{
			name: "plan failure with no applied operations",
			result: &MutateResult{
				OperationsApplied: 0,
				OperationsFailed:  2,
				Paths:             []string{},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.result.WasMutated()
			if got != tt.expected {
				t.Errorf("WasMutated() = %v, want %v", got, tt.expected)
			}
		})
	}
}
