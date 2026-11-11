//go:build linux && cgo

package platform

import "testing"

func TestPortalWorkflowCapabilityStatus(t *testing.T) {
	oldProbe, oldWayland, oldUnavailable := probePortalWorkflow, linuxWayland, linuxPortalWorkflowUnavailable
	defer func() {
		probePortalWorkflow, linuxWayland, linuxPortalWorkflowUnavailable = oldProbe, oldWayland, oldUnavailable
	}()
	linuxWayland = false
	for _, available := range []bool{false, true} {
		probePortalWorkflow = func() bool { return available }
		if err := InitializeDesktop(t.TempDir()); err != nil {
			t.Fatal(err)
		}
		unavailable := linuxPortalWorkflowUnavailable
		x := &linuxClipboard{portalWorkflowUnavailable: unavailable}
		// No connection is needed to check the manual status.
		w := &portalDesktop{manual: true, portalWorkflowUnavailable: unavailable}
		for _, status := range []DesktopStatus{x.DesktopStatus(), w.DesktopStatus()} {
			if status.PortalWorkflowUnavailable != !available {
				t.Fatalf("available=%v: %+v", available, status)
			}
		}
	}
}
