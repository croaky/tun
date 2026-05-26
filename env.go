package tun

import (
	"log"
	"os"
	"strings"
)

// Load loads TUN_* environment variables from a .env file.
// Only keys prefixed with "TUN_" are loaded; existing env vars are not overwritten.
func Load(name string) {
	data, err := os.ReadFile(name)
	if err != nil {
		return
	}
	var openQuote rune
	for _, ln := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(ln)
		if openQuote != 0 {
			if strings.ContainsRune(line, openQuote) {
				openQuote = 0
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		i := strings.IndexByte(line, '=')
		if i <= 0 {
			log.Printf("env: malformed line: %s", line)
			continue
		}
		k := strings.TrimSpace(line[:i])
		v := strings.TrimSpace(line[i+1:])
		if len(v) > 0 && (v[0] == '"' || v[0] == '\'') {
			q := rune(v[0])
			if !strings.ContainsRune(v[1:], q) {
				openQuote = q
				continue
			}
		}
		v = strings.Trim(v, "\"'")
		// Only import our own keys (plus PORT) to avoid clobbering app env
		if !strings.HasPrefix(k, "TUN_") && k != "PORT" {
			continue
		}
		if os.Getenv(k) == "" {
			if err := os.Setenv(k, v); err != nil {
				log.Printf("env: failed to set %s: %v", k, err)
			}
		}
	}
}
