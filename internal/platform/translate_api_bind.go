package platform

import (
	"log"
	"net"
)

// translationAPIBindHosts lists the addresses to listen on. It never falls back
// to the wildcard: when the Crostini container address cannot be read it listens
// on loopback only and returns a warning, so a transient lookup failure cannot
// expose the endpoint on every interface.
func translationAPIBindHosts(crostini bool, primary func() (string, error)) (hosts []string, warning string) {
	if !crostini {
		return []string{"127.0.0.1"}, ""
	}
	ip, err := primary()
	parsed := net.ParseIP(ip)
	if err != nil || parsed.To4() == nil || parsed.IsLoopback() || parsed.IsUnspecified() {
		log.Printf("translation endpoint: cannot determine Crostini container IPv4 (%v, %q); listening on loopback only", err, ip)
		return []string{"127.0.0.1"}, "The container network address could not be read, so ChromeOS port forwarding cannot reach the endpoint. Turn the endpoint off and on again once the network is up."
	}
	return []string{"127.0.0.1", ip}, ""
}

// TranslationAPIBindHosts resolves the addresses each time the endpoint is
// started, so a changed container address is picked up when it is turned on again.
func TranslationAPIBindHosts() ([]string, string) {
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
