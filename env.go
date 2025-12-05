package tun

import (
	"log"
	"os"
	"strings"
)

// LoadEnv loads TUN_* environment variables from a .env file.
// Only keys prefixed with "TUN_" are loaded; existing env vars are not overwritten.
func LoadEnv(name string) {
	data, err := os.ReadFile(name)
	if err != nil {
		return
	}
	for _, ln := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(ln)
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
		v = strings.Trim(v, "\"'")
		// Only import our own keys (plus PORT) to avoid clobbering app env
		if !strings.HasPrefix(k, "TUN_") && k != "PORT" {
			continue
		}
		if os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
}
