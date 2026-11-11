package platform

import (
	"log"
	"net"
)

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
	return translationAPIBindHosts(desktopCrostini, func() (string, error) {
		// A UDP route lookup selects the primary interface without sending packets.
		conn, err := net.Dial("udp4", "192.0.2.1:9")
		if err != nil {
			return "", err
		}
		defer conn.Close()
		return conn.LocalAddr().(*net.UDPAddr).IP.String(), nil
	})
}
