package skillmaintainers

import "testing"

func TestMaintainer_IsActive(t *testing.T) {
	m := Maintainer{}
	if !m.IsActive() {
		t.Error("nil RevokedAt should be active")
	}
	now := nowFn()
	m.RevokedAt = &now
	if m.IsActive() {
		t.Error("non-nil RevokedAt should not be active")
	}
}
