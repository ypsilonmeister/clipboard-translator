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
		hosts, _ := TranslationAPIBindHosts()
		if len(hosts) == 0 || (len(hosts) == 1 && hosts[0] == "127.0.0.1") {
			t.Fatal(hosts)
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
