package sysinfo

import "testing"

func TestContainerMemoryLeavesOutTheFileCache(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  uint64
	}{
		{"cgroup v2", map[string]string{
			"sys/fs/cgroup/memory.current": "2000000000\n",
			"sys/fs/cgroup/memory.stat":    "anon 30000000\nfile 1950000000\ninactive_file 1900000000\nactive_file 8000000\n",
		}, 100000000},
		{"cgroup v1", map[string]string{
			"sys/fs/cgroup/memory/memory.usage_in_bytes": "500000000\n",
			"sys/fs/cgroup/memory/memory.stat":           "cache 400000000\ntotal_inactive_file 300000000\n",
		}, 200000000},
		{"no stat file", map[string]string{"sys/fs/cgroup/memory.current": "123\n"}, 123},
		{"cache larger than the usage", map[string]string{
			"sys/fs/cgroup/memory.current": "100\n",
			"sys/fs/cgroup/memory.stat":    "inactive_file 900\n",
		}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for rel, body := range tt.files {
				writeFile(t, root, rel, body)
			}
			s, _ := fakeSampler(root, 4)
			got, ok := s.containerMemory()
			if !ok || got != tt.want {
				t.Fatalf("containerMemory = %d, %v; want %d", got, ok, tt.want)
			}
		})
	}
}

func TestNoCgroupFilesLeavesTheGoMemoryFigure(t *testing.T) {
	s, _ := fakeSampler(t.TempDir(), 4)
	if _, ok := s.containerMemory(); ok {
		t.Fatal("no cgroup files must report nothing, so the Go heap figure stays")
	}
}

func TestNewCountsUptimeFromTheProgramStart(t *testing.T) {
	if got := New().started; !got.Equal(processStart) {
		t.Fatalf("started = %v, want the program's start %v", got, processStart)
	}
}
