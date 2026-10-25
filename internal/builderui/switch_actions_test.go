package builderui

import "testing"

func TestSwitchActionsSeparateIntegratedAndGraphicsOnly(t *testing.T) {
	actions := availableActions()
	integrated, diagnostic, fullPC, allVersions := 0, 0, 0, 0
	for _, a := range actions {
		switch a.Name {
		case "Switch: translated text + graphics + IPS candidate":
			if a.Run == nil {
				t.Fatal("combined candidate is not wired")
			}
			integrated++
		case "Switch: graphics-only diagnostic ZIPs":
			if a.Run == nil {
				t.Fatal("graphics diagnostic is not wired")
			}
			diagnostic++
		case "Full PC release wizard":
			fullPC++
		case "Full PC + Switch release wizard":
			if a.Run == nil {
				t.Fatal("all versions wizard not wired")
			}
			allVersions++
		}
	}
	if integrated != 1 || diagnostic != 1 || fullPC != 1 || allVersions != 1 {
		t.Fatalf("invalid action separation: combined=%d graphics=%d PC=%d", integrated, diagnostic, fullPC)
	}
}
