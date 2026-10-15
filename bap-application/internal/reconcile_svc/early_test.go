package reconcilesvc

import (
	"strings"
	"testing"
	"time"
)

func TestBeforeWindow(t *testing.T) {
	due := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	f := &Figures{Basis: "AFTER_DELIVERY", Window: "PT2M"}
	cases := []struct {
		d     DueContract
		early bool
		has   string
	}{
		{DueContract{windowDue: true, Figures: f}, false, ""},
		{DueContract{Due: true, BasisEvent: "awaiting delivery", Figures: f}, true, "not delivered yet"}, // due only by the flag
		{DueContract{BasisEvent: "delivered", DueAt: &due, Figures: f}, true, "due at 2026-10-14T10:00:00Z"},
		{DueContract{BasisEvent: "awaiting delivery", Figures: f}, true, "not delivered yet"},
		{DueContract{BasisEvent: "awaiting return window", Figures: f}, false, ""},
	}
	for _, c := range cases {
		reason, early := beforeWindow(c.d)
		if early != c.early || !strings.Contains(reason, c.has) {
			t.Errorf("%s: %q %v, want early %v with %q", c.d.BasisEvent, reason, early, c.early, c.has)
		}
	}
}
