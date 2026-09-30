package aria2

import "testing"

func TestDestination(t *testing.T) {
	dir, out, err := destination("/downloads", "season/episode.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/downloads/season" || out != "episode.mkv" {
		t.Fatalf("unexpected destination %q %q", dir, out)
	}
}

func TestDestinationRejectsTraversal(t *testing.T) {
	if _, _, err := destination("/downloads", "../secret"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestDestinationRequiresTargetDir(t *testing.T) {
	if _, _, err := destination("", "movie.mkv"); err == nil {
		t.Fatal("expected empty target directory rejection")
	}
}
