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
		{true, "", []string{"0.0.0.0"}},
		{true, "127.0.0.1", []string{"0.0.0.0"}},
	} {
		if got := translationAPIBindHosts(tc.cros, func() (string, error) { return tc.ip, nil }); !reflect.DeepEqual(got, tc.want) {
			t.Fatal(got, tc.want)
		}
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
