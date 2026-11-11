//go:build linux && cgo

package platform

import "testing"

func TestPortalWorkflowCapabilityStatus(t *testing.T) {
	oldEval, oldCached := evaluateCrostini, desktopCrostini
	t.Cleanup(func() { evaluateCrostini, desktopCrostini = oldEval, oldCached })
	evaluateCrostini = func() bool { return true }
	oldProbe, oldWayland, oldUnavailable := probePortalWorkflow, linuxWayland, linuxPortalCapabilitiesMissing
	defer func() {
		probePortalWorkflow, linuxWayland, linuxPortalCapabilitiesMissing = oldProbe, oldWayland, oldUnavailable
	}()
	linuxWayland = false
	for _, available := range []bool{false, true} {
		probePortalWorkflow = func() bool { return available }
		if err := InitializeDesktop(t.TempDir()); err != nil {
			t.Fatal(err)
		}
		unavailable := linuxPortalCapabilitiesMissing
		x := &linuxClipboard{portalCapabilitiesMissing: unavailable}
		// No connection is needed to check the manual status.
		w := &portalDesktop{manual: true, portalCapabilitiesMissing: unavailable}
		for _, status := range []DesktopStatus{x.DesktopStatus(), w.DesktopStatus()} {
			if status.PortalCapabilitiesMissing != !available {
				t.Fatalf("available=%v: %+v", available, status)
			}
		}
	}
}

func TestOrdinaryX11SkipsPortalProbe(t *testing.T) {
	oldProbe, oldWayland := probePortalWorkflow, linuxWayland
	oldCached := desktopCrostini
	oldMarker, oldMissing := evaluateCrostini, linuxPortalCapabilitiesMissing
	t.Cleanup(func() {
		desktopCrostini = oldCached
		probePortalWorkflow, linuxWayland = oldProbe, oldWayland
		evaluateCrostini, linuxPortalCapabilitiesMissing = oldMarker, oldMissing
	})
	evaluateCrostini = func() bool { return false }
	linuxWayland = false
	probePortalWorkflow = func() bool { t.Fatal("ordinary desktop probed portals"); return false }
	if err := InitializeDesktop(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if linuxPortalCapabilitiesMissing {
		t.Fatal("ordinary X11 reported missing capabilities")
	}
}

func TestCrostiniDetectionCached(t *testing.T) {
	oldEval, oldCached := evaluateCrostini, desktopCrostini
	oldProbe, oldWayland, oldMissing := probePortalWorkflow, linuxWayland, linuxPortalCapabilitiesMissing
	t.Cleanup(func() {
		evaluateCrostini, desktopCrostini = oldEval, oldCached
		probePortalWorkflow, linuxWayland, linuxPortalCapabilitiesMissing = oldProbe, oldWayland, oldMissing
	})
	calls := 0
	evaluateCrostini = func() bool { calls++; return true }
	linuxWayland = false
	probePortalWorkflow = func() bool { return false }
	if err := InitializeDesktop(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	evaluateCrostini = func() bool { t.Fatal("detection after initialization"); return false }
	for i := 0; i < 5; i++ {
		x := (&linuxClipboard{portalCapabilitiesMissing: linuxPortalCapabilitiesMissing}).DesktopStatus()
		w := (&portalDesktop{manual: true}).DesktopStatus()
		if !x.Crostini || !w.Crostini || !x.PortalCapabilitiesMissing {
			t.Fatal(x, w)
		}
		// The cached flag selects the scoped bind. The address lookup is stubbed:
		// the real one needs a network route, which a sandbox or CI runner may lack.
		hosts, warning := translationAPIBindHosts(desktopCrostini, func() (string, error) { return "100.115.92.26", nil })
		if len(hosts) != 2 || hosts[0] != "127.0.0.1" || hosts[1] != "100.115.92.26" || warning != "" {
			t.Fatal(hosts, warning)
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
