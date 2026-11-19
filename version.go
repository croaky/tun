package tun

import "runtime/debug"

// RevisionHeader is the handshake header in which the client sends its
// Revision. tund logs it, so the server log shows which build each
// client runs.
const RevisionHeader = "X-Tun-Revision"

// Revision returns the source revision of the running binary, so an
// operator can see that a deploy or an install has a fix.
func Revision() string {
	info, ok := debug.ReadBuildInfo()
	return revision(info, ok)
}

// revision prefers vcs.revision, which go build records in a clone,
// such as on Render. go install pkg@main records no vcs settings, but
// the module version holds the commit, as in
// v0.1.2-0.20261119133951-1215dd91b61b.
func revision(info *debug.BuildInfo, ok bool) string {
	if !ok {
		return "unknown"
	}
	var rev, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if rev != "" {
		rev = rev[:min(len(rev), 12)]
		if modified == "true" {
			rev += "-dirty"
		}
		return rev
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	return "unknown"
}
