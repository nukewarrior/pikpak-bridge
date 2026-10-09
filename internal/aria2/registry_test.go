package aria2

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/nukewarrior/pikpak-bridge/internal/config"
)

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

func TestRegistryOverwriteOptionsOnlyForConfirmedRepeat(t *testing.T) {
    var received []map[string]string
    handler:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
        var req rpcRequest
        if err:=json.NewDecoder(r.Body).Decode(&req);err!=nil {t.Errorf("RPC request: %v",err);return}
        if req.Method!="aria2.addUri" {t.Errorf("unexpected method %q",req.Method);return}
        opts,ok:=req.Params[1].(map[string]any)
        if !ok {t.Errorf("missing aria2 options: %#v",req.Params);return}
        mapped:=make(map[string]string)
        for key,v:=range opts {mapped[key],_=v.(string)}
        received=append(received,mapped)
        w.Header().Set("Content-Type","application/json")
        _,_=w.Write([]byte(`{"jsonrpc":"2.0","result":"0123456789abcdef"}`))
    }))
    defer handler.Close()
    registry:=NewRegistry([]config.Aria2Instance{{ID:"a1",Name:"main",URL:handler.URL}})
    ctx:=context.Background()
    for _,overwrite:=range []bool{false,true} {
        if _,err:=registry.Add(ctx,"a1","/downloads","https://example.invalid/image.png",
            "0123456789abcdef","Album/image.png",overwrite);err!=nil {t.Fatal(err)}
    }
    if len(received)!=2 {t.Fatalf("expected two RPCs, got %d",len(received))}
    first,second:=received[0],received[1]
    if first["dir"]!="/downloads/Album" || second["dir"]!="/downloads/Album" ||
        first["out"]!="image.png" || second["out"]!="image.png" {
        t.Fatalf("wrong destination: %+v %+v",first,second)
    }
    if first["continue"]!="true" || first["allow-overwrite"]!="" {
        t.Fatalf("initial download settings changed: %+v",first)
    }
    if second["continue"]!="false" || second["allow-overwrite"]!="true" ||
       second["auto-file-renaming"]!="false" {
        t.Fatalf("repeat must overwrite directly: %+v",second)
    }
}
