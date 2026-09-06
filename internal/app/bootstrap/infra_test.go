package bootstrap

import "testing"

// TestValidateBackgroundPoolSize covers the guard OpenBackgroundPool runs
// before opening the pool: the background pool must be non-empty and
// strictly smaller than the main request pool, or the connection-slot
// split the two pools exist to provide is defeated.
func TestValidateBackgroundPoolSize(t *testing.T) {
	tests := []struct {
		name       string
		background int32
		main       int32
		wantErr    bool
	}{
		{"smaller than main is fine", 5, 20, false},
		{"equal to main is rejected", 20, 20, true},
		{"larger than main is rejected", 25, 20, true},
		{"zero is rejected", 0, 20, true},
		{"negative is rejected", -1, 20, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBackgroundPoolSize(tt.background, tt.main)
			if tt.wantErr && err == nil {
				t.Errorf("validateBackgroundPoolSize(%d, %d) = nil, want error", tt.background, tt.main)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateBackgroundPoolSize(%d, %d) = %v, want nil", tt.background, tt.main, err)
			}
		})
	}
}
