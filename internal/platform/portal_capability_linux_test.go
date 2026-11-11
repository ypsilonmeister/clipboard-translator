//go:build linux && cgo

package platform

import "testing"

func TestPortalWorkflowCapabilityStatus(t *testing.T) {
	t.Setenv("SOMMELIER_VERSION", "test")
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
	oldMarker, oldMissing := desktopIsCrostini, linuxPortalCapabilitiesMissing
	t.Cleanup(func() {
		probePortalWorkflow, linuxWayland = oldProbe, oldWayland
		desktopIsCrostini, linuxPortalCapabilitiesMissing = oldMarker, oldMissing
	})
	desktopIsCrostini = func() bool { return false }
	linuxWayland = false
	probePortalWorkflow = func() bool { t.Fatal("ordinary desktop probed portals"); return false }
	if err := InitializeDesktop(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if linuxPortalCapabilitiesMissing {
		t.Fatal("ordinary X11 reported missing capabilities")
	}
}
