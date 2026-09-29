package pikpak

import "testing"

func TestFindOfflineTaskBySource(t *testing.T) {
	tasks := []offlineTaskAPI{
		{ID: "other"},
		{ID: "expected", Phase: PhaseRunning},
	}
	tasks[0].Params.URL = "https://example.invalid/other"
	tasks[1].Params.URL = " magnet:?xt=urn:btih:ABC "

	got, ok := findOfflineTaskBySource(tasks, "magnet:?xt=urn:btih:ABC")
	if !ok {
		t.Fatal("expected existing task")
	}
	if got.ID != "expected" {
		t.Fatalf("want expected, got %s", got.ID)
	}
}
