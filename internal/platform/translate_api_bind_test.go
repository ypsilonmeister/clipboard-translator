package platform

import (
	"errors"
	"reflect"
	"testing"
)

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
