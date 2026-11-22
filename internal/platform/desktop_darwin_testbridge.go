//go:build darwin && cgo && mactest

package platform

/*
void ct_mac_test_menu(int id);
void ct_mac_test_hotkey(int pressed);
void ct_mac_test_cancel_setup_soon(void);
int ct_mac_notify_touched(void);
*/
import "C"

func macTestMenu(id int)         { C.ct_mac_test_menu(C.int(id)) }
func macTestHotkey(pressed bool) { C.ct_mac_test_hotkey(C.int(boolInt(pressed))) }
func macTestCancelSetupSoon()    { C.ct_mac_test_cancel_setup_soon() }
func macNotifyTouched() bool     { return C.ct_mac_notify_touched() != 0 }
