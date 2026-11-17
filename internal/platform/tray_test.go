package platform

import (
	"testing"
)

func TestTrayState(t *testing.T) {
	cases := []struct {
		state TrayState
		want  string
	}{
		{TrayIdle, "idle"},
		{TrayBusy, "busy"},
		{TrayWorking, "busy"},
		{TrayReady, "ready"},
		{TrayDone, "ready"},
		{TrayProblem, "problem"},
		{TrayState(99), "idle"},
	}

	for _, tc := range cases {
		if got := tc.state.String(); got != tc.want {
			t.Errorf("TrayState(%d).String() = %q, want %q", tc.state, got, tc.want)
		}
	}
}

func TestNoopTraySetState(t *testing.T) {
	var tr Tray = noopTray{}
	tr.SetState(TrayBusy)
	tr.SetState(TrayReady)
	tr.SetState(TrayProblem)
	tr.SetState(TrayIdle)
}
