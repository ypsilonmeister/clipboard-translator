package platform

import (
	"log"
	"net"
	"os"
	"strings"
)

// These markers are used only to choose the optional translation API bind scope.
// This is deliberately not general desktop/session detection.
func translationAPICrostini(read func(string) ([]byte, error), getenv func(string) string) bool {
	product, _ := read("/sys/class/dmi/id/product_name")
	if strings.EqualFold(strings.TrimSpace(string(product)), "crosvm") {
		return true
	}
	if _, err := read("/dev/.cros_milestone"); err == nil {
		return true
	}
	return getenv("SOMMELIER_VERSION") != ""
}

func translationAPIBindHosts(crostini bool, primary func() (string, error)) []string {
	if !crostini {
		return []string{"127.0.0.1"}
	}
	ip, err := primary()
	parsed := net.ParseIP(ip)
	if err != nil || parsed.To4() == nil || parsed.IsLoopback() || parsed.IsUnspecified() {
		log.Printf("translation endpoint: cannot determine Crostini container IPv4 (%v, %q); falling back to wildcard for ChromeOS forwarding", err, ip)
		return []string{"0.0.0.0"}
	}
	return []string{"127.0.0.1", ip}
}

// TranslationAPIBindHosts resolves addresses at endpoint startup.
func TranslationAPIBindHosts() []string {
	return translationAPIBindHosts(translationAPICrostini(os.ReadFile, os.Getenv), func() (string, error) {
		// A UDP route lookup selects the primary interface without sending packets.
		conn, err := net.Dial("udp4", "192.0.2.1:9")
		if err != nil {
			return "", err
		}
		defer conn.Close()
		return conn.LocalAddr().(*net.UDPAddr).IP.String(), nil
	})
}
