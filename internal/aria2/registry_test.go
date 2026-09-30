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

func TestDestinationRejectsParentTraversal(t *testing.T) {
	if _, _, err := destination("/downloads", "season/../secret.bin"); err == nil {
		t.Fatal("expected parent traversal rejection")
	}
}

func TestDestinationRequiresTargetDir(t *testing.T) {
	if _, _, err := destination("", "movie.mkv"); err == nil {
		t.Fatal("expected empty target directory rejection")
	}
}


func TestDestinationPreservesPikPakRootFolder(t *testing.T) {
	dir, out, err := destination("/downloads/movies", "Movie.Collection/Disc 1/video.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/downloads/movies/Movie.Collection/Disc 1" || out != "video.mkv" {
		t.Fatalf("unexpected destination %q %q", dir, out)
	}
}
