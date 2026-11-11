package platform

import (
	"errors"
	"reflect"
	"testing"
)

func TestTranslationAPICrostiniMarkers(t *testing.T) {
	for _, marker := range []string{"", "dmi", "file", "env"} {
		got := translationAPICrostini(func(path string) ([]byte, error) {
			if marker == "dmi" && path == "/sys/class/dmi/id/product_name" {
				return []byte("crosvm\n"), nil
			}
			if marker == "file" && path == "/dev/.cros_milestone" {
				return nil, nil
			}
			return nil, errors.New("absent")
		}, func(string) string {
			if marker == "env" {
				return "1"
			}
			return ""
		})
		if got != (marker != "") {
			t.Fatal(marker, got)
		}
	}
}
func TestTranslationAPIBindHosts(t *testing.T) {
	for _, tc := range []struct {
		cros bool
		ip   string
		want []string
	}{
		{false, "", []string{"127.0.0.1"}},
		{true, "100.115.92.2", []string{"127.0.0.1", "100.115.92.2"}},
		{true, "", []string{"127.0.0.1"}},
		{true, "127.0.0.1", []string{"127.0.0.1"}},
		{true, "0.0.0.0", []string{"127.0.0.1"}},
		{true, "::1", []string{"127.0.0.1"}},
	} {
		got, warning := translationAPIBindHosts(tc.cros, func() (string, error) { return tc.ip, nil })
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatal(got, tc.want)
		}
		for _, host := range got {
			if host == "0.0.0.0" {
				t.Fatal("wildcard bind", tc)
			}
		}
		if wantWarning := tc.cros && len(tc.want) == 1; (warning != "") != wantWarning {
			t.Fatal("warning", warning, tc)
		}
	}
}

func TestTranslationAPIBindHostsLookupError(t *testing.T) {
	got, warning := translationAPIBindHosts(true, func() (string, error) { return "", errors.New("no route") })
	if !reflect.DeepEqual(got, []string{"127.0.0.1"}) || warning == "" {
		t.Fatal(got, warning)
	}
}

func TestManualClipboardReadResult(t *testing.T) {
	for _, tc := range []struct {
		complete, hasValue bool
		text               string
		err                error
	}{
		{false, false, "", ErrClipboardPending},
		{true, false, "", ErrClipboardNoValue},
		{true, true, "", nil},
		{true, true, "Hello", nil},
	} {
		text, err := manualClipboardReadResult(tc.complete, tc.hasValue, tc.text)
		if text != tc.text || !errors.Is(err, tc.err) {
			t.Fatal(text, err, tc)
		}
	}
}
