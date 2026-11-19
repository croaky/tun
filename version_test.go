package tun

import (
	"runtime/debug"
	"testing"

	"github.com/croaky/is"
)

func TestRevision(t *testing.T) {
	const sha = "1215dd91b61baaa3d29522fefe153d5a40404987"
	for _, tt := range []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"no build info", nil, false, "unknown"},
		{"clone build", &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: sha},
			{Key: "vcs.modified", Value: "false"},
		}}, true, "1215dd91b61b"},
		{"modified clone", &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: sha},
			{Key: "vcs.modified", Value: "true"},
		}}, true, "1215dd91b61b-dirty"},
		{"go install at main", &debug.BuildInfo{Main: debug.Module{
			Version: "v0.1.2-0.20261119133951-1215dd91b61b",
		}}, true, "v0.1.2-0.20261119133951-1215dd91b61b"},
		{"build with no vcs", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true, "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			is.New(t).Eq(revision(tt.info, tt.ok), tt.want)
		})
	}
}
