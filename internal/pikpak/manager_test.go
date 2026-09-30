package pikpak

import "testing"

func TestMapOfflineTask(t *testing.T) {
	task := offlineTaskAPI{
		ID:      "remote-1",
		FileID:  "file-1",
		Phase:    PhaseRunning,
		Progress: 37,
		Message:  "Saving",
	}
	got := mapOfflineTask(task)
	if got.ID != task.ID || got.RootFileID != task.FileID || got.Status != task.Phase || got.Progress != task.Progress || got.Error != task.Message {
		t.Fatalf("unexpected mapped task: %#v", got)
	}
}
