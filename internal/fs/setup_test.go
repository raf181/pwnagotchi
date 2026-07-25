package fs

import "testing"

func TestSetupMountsDisabledIsNoop(t *testing.T) {
	r := &fakeRunner{}
	stop := make(chan struct{})
	defer close(stop)
	mounts, err := SetupMounts(r, false, map[string]MountConfig{"root": {Enabled: true}}, stop, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 0 {
		t.Fatalf("expected no mounts when fs.memory.enabled=false, got %v", mounts)
	}
	if len(r.calls) != 0 {
		t.Fatalf("expected no commands run, got %v", r.calls)
	}
}

func TestSetupMountsSkipsDisabledEntries(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{}
	stop := make(chan struct{})
	defer close(stop)

	mounts, err := SetupMounts(r, true, map[string]MountConfig{
		"root": {Enabled: false, Mount: dir + "/root", Size: "40M"},
	}, stop, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 0 {
		t.Fatalf("expected disabled entry to be skipped, got %v", mounts)
	}
}

func TestSetupMountsBuildsAndMountsEnabledEntry(t *testing.T) {
	dir := t.TempDir()
	// "mountpoint" fails (reports not-yet-mounted) so SetupMounts proceeds
	// to actually call Mount()/Sync(); the fake Runner otherwise succeeds.
	r := &fakeRunner{failing: map[string]bool{"mountpoint": true}}
	stop := make(chan struct{})
	defer close(stop)

	mounts, err := SetupMounts(r, true, map[string]MountConfig{
		"root": {Enabled: true, Mount: dir + "/root", Size: "40M", Zram: false, Rsync: true, Sync: 0},
	}, stop, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d (%v)", len(mounts), mounts)
	}

	foundMountpointCheck := false
	foundBind := false
	for _, c := range r.calls {
		if len(c) > 0 && c[0] == "mountpoint" {
			foundMountpointCheck = true
		}
		if len(c) > 1 && c[0] == "mount" && c[1] == "--bind" {
			foundBind = true
		}
	}
	if !foundMountpointCheck || !foundBind {
		t.Fatalf("expected mountpoint check + bind mount calls, got %v", r.calls)
	}
}
